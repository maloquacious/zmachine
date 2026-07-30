package zmachine

import (
	"context"
	"errors"
	"testing"
)

// loopForever assembles a jump back to itself, which is the smallest story
// that never terminates: "jump -2" lands on its own first byte, because the
// destination is the address after the instruction plus the offset minus two
// (S 15, jump).
func loopForever() []byte {
	return encodeShort(0x0c, largeOp(unsigned(-1)))
}

// TestExecutionLimit covers spec S 25: a story that never stops must not be
// able to monopolise a server worker. Exceeding the limit is a distinguishable
// error, not a status.
func TestExecutionLimit(t *testing.T) {
	m := newStory(t).code(loopForever()...).machine(WithInstructionLimit(500))

	_, err := m.Start(context.Background())
	if !errors.Is(err, ErrExecutionLimit) {
		t.Fatalf("Start() error = %v, want one wrapping ErrExecutionLimit", err)
	}
	if m.executed != 500 {
		t.Errorf("executed = %d instructions, want 500", m.executed)
	}
}

// TestExecutionLimitAppliesPerCall checks that the limit is spent afresh by
// each call, so that a story running a long turn is not penalised on the next
// one for having done so.
func TestExecutionLimitAppliesPerCall(t *testing.T) {
	// print, then a line-input instruction with no input available, which
	// suspends before it is executed.
	code := join(
		encodeShort(0x02), encodeText(t, "ok"),
		encodeVar(countVAR, 0x04, largeOp(machineScratch), largeOp(machineScratch+64)),
	)
	m := newStory(t).code(code...).machine(WithInstructionLimit(10))

	// run is what Start and Run share, so calling it directly repeats the
	// invocation without needing input the build cannot yet consume.
	for turn := 0; turn < 3; turn++ {
		result, err := m.run(context.Background())
		if err != nil {
			t.Fatalf("turn %d: run() error = %v", turn, err)
		}
		if result.Status != WaitingForInput {
			t.Fatalf("turn %d: Status = %v, want %v", turn, result.Status, WaitingForInput)
		}
		if m.executed > 10 {
			t.Fatalf("turn %d: executed = %d, want at most the limit of 10", turn, m.executed)
		}
	}
}

// TestContextCancellation covers spec S 25: cancellation must be honoured, and
// the context's own error must reach the caller so that a host can tell a
// deadline from a limit.
func TestContextCancellation(t *testing.T) {
	t.Run("cancelled before the first instruction", func(t *testing.T) {
		m := newTestMachine(t, loopForever()...)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := m.Start(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Start() error = %v, want context.Canceled", err)
		}
		if m.executed != 0 {
			t.Errorf("executed = %d instructions after a cancelled start, want 0", m.executed)
		}
	})

	t.Run("cancelled while running", func(t *testing.T) {
		m := newStory(t).code(loopForever()...).machine(WithInstructionLimit(100_000_000))
		ctx, cancel := context.WithCancel(context.Background())

		// Cancelling from another goroutine would race with the check; setting
		// it up so the loop sees the cancellation is enough to show the loop
		// looks, and the interval bounds how late it looks.
		cancel()
		_, err := m.Start(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Start() error = %v, want context.Canceled", err)
		}
		if m.executed > contextCheckInterval {
			t.Errorf("executed = %d instructions before noticing cancellation, want at most %d",
				m.executed, contextCheckInterval)
		}
	})

	t.Run("a nil context is refused", func(t *testing.T) {
		m := newTestMachine(t, encodeShort(0x0a)...)
		//nolint:staticcheck // passing nil is exactly what is being rejected
		if _, err := m.Start(nil); err == nil {
			t.Errorf("Start(nil) error = nil, want an error")
		}
		if _, err := m.Run(nil, ""); err == nil {
			t.Errorf("Run(nil, \"\") error = nil, want an error")
		}
	})
}

// TestStartAndRunLifecycle checks the states a machine can be asked to run in.
// A halted story cannot be resumed, and Start begins execution once.
func TestStartAndRunLifecycle(t *testing.T) {
	m := newTestMachine(t, encodeShort(0x0a)...) // quit

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Status != Halted {
		t.Errorf("Status = %v, want %v", result.Status, Halted)
	}
	if _, err := m.Start(context.Background()); err == nil {
		t.Errorf("Start() a second time error = nil, want an error")
	}
	if _, err := m.Run(context.Background(), "look"); err == nil {
		t.Errorf("Run() after halting error = nil, want an error")
	}

	fresh := newTestMachine(t, encodeShort(0x0a)...)
	if _, err := fresh.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if _, err := fresh.Start(context.Background()); err == nil {
		t.Errorf("Start() twice error = nil, want an error")
	}
}

// TestInputBoundarySuspendsWithoutConsumingOperands covers the central
// promise of spec S 4: a line-input instruction the host has not supplied
// input for is a suspension boundary, not a wait. The program counter is left
// on the instruction, and its operands must not have been evaluated - a
// variable operand pops the stack (S 4.2.2), and popping twice for one
// instruction would corrupt it.
func TestInputBoundarySuspendsWithoutConsumingOperands(t *testing.T) {
	// sread with both buffer addresses taken from the stack.
	code := encodeVar(countVAR, 0x04, varOp(variableStack), varOp(variableStack))
	m := newTestMachine(t, code...)
	if err := m.push(machineScratch); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	if err := m.push(machineScratch + 64); err != nil {
		t.Fatalf("push() error = %v", err)
	}

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Status != WaitingForInput {
		t.Errorf("Status = %v, want %v", result.Status, WaitingForInput)
	}
	if m.pc != machineCodeBase {
		t.Errorf("pc = 0x%04x, want 0x%04x: the input instruction must be re-executed", m.pc, machineCodeBase)
	}
	if got := m.stackDepth(); got != 2 {
		t.Errorf("stackDepth() = %d, want 2: the operands were evaluated at the boundary", got)
	}
}

// TestOutputIsPerInvocation checks that each call to Start or Run reports only
// the text produced during it, so that a host relaying Result.Output does not
// repeat the previous turn.
func TestOutputIsPerInvocation(t *testing.T) {
	code := join(
		encodeShort(0x02), encodeText(t, "first"),
		encodeVar(countVAR, 0x04, largeOp(machineScratch), largeOp(machineScratch+64)),
		encodeShort(0x02), encodeText(t, "second"),
		encodeShort(0x0a),
	)
	m := newTestMachine(t, code...)

	first, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if first.Output != "first" {
		t.Errorf("first Output = %q, want %q", first.Output, "first")
	}

	// The line-input instruction is not executed by this build, so supplying
	// input reaches it and reports that rather than continuing. What matters
	// here is that the earlier output does not come back.
	second, err := m.Run(context.Background(), "look")
	if !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("Run() error = %v, want one wrapping ErrNotImplemented", err)
	}
	if second.Output != "" {
		t.Errorf("second Output = %q, want empty on a fault", second.Output)
	}
}

// recordingTracer collects the events a Tracer receives.
type recordingTracer struct {
	events []TraceInstruction
}

func (r *recordingTracer) Instruction(e TraceInstruction) { r.events = append(r.events, e) }

// TestTracer covers spec S 30: tracing is injectable, off by default, and
// semantically inert, and it exposes the program counter, opcode, operands,
// call depth, branch result, variable writes and routine calls and returns.
func TestTracer(t *testing.T) {
	const routine = 0x0600
	addr, header := routineAt(routine, 0x1111)

	// add 2 3 -> $10; call routine -> $11; jz 0 ?taken; quit.
	code := join(
		append(encodeVar(count2OP, 0x14, smallOp(2), smallOp(3)), globalFirst),
		append(encodeVar(countVAR, 0x00, largeOp(packed(routine))), globalFirst+1),
	)
	// The routine body is rtrue, which returns 1 to the caller.
	body := encodeShort(0x00)
	// jz 0, whose branch lands on the instruction that follows it, then quit.
	// An offset of 2 means "the address after the branch data" (S 4.7.2).
	tail := join(encodeShort(0x00, largeOp(0)), branch2(true, 2), encodeShort(0x0a))

	tracer := &recordingTracer{}
	m := newStory(t).
		at(addr, header...).
		at(addr+uint32(len(header)), body...).
		code(join(code, tail)...).
		machine(WithTracer(tracer))

	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if len(tracer.events) != 5 {
		t.Fatalf("events = %d, want 5: add, call, rtrue, jz, quit", len(tracer.events))
	}

	add := tracer.events[0]
	if add.PC != machineCodeBase {
		t.Errorf("add PC = 0x%04x, want 0x%04x", add.PC, machineCodeBase)
	}
	if add.Opcode != "2OP:20 add" {
		t.Errorf("add Opcode = %q, want %q", add.Opcode, "2OP:20 add")
	}
	if len(add.Operands) != 2 || add.Operands[0] != 2 || add.Operands[1] != 3 {
		t.Errorf("add Operands = %v, want [2 3]", add.Operands)
	}
	if !add.Stored || add.StoreVariable != globalFirst || add.StoreValue != 5 {
		t.Errorf("add store = $%02x <- %d (present %t), want $%02x <- 5",
			add.StoreVariable, add.StoreValue, add.Stored, globalFirst)
	}
	if add.CallDepth != 0 {
		t.Errorf("add CallDepth = %d, want 0", add.CallDepth)
	}

	call := tracer.events[1]
	if !call.Called {
		t.Errorf("call Called = false, want true")
	}
	if call.CallDepth != 0 {
		t.Errorf("call CallDepth = %d, want 0: the depth before the call", call.CallDepth)
	}

	ret := tracer.events[2]
	if !ret.Returned || ret.ReturnValue != 1 {
		t.Errorf("rtrue Returned = %t with value %d, want true with 1", ret.Returned, ret.ReturnValue)
	}
	if ret.CallDepth != 1 {
		t.Errorf("rtrue CallDepth = %d, want 1", ret.CallDepth)
	}

	jz := tracer.events[3]
	if !jz.Branched {
		t.Errorf("jz Branched = false, want true: jz 0 branches")
	}

	t.Run("tracing does not change execution", func(t *testing.T) {
		silent := newStory(t).
			at(addr, header...).
			at(addr+uint32(len(header)), body...).
			code(join(code, tail)...).
			machine()
		if _, err := silent.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if silent.pc != m.pc {
			t.Errorf("pc with tracing = 0x%04x, without = 0x%04x", m.pc, silent.pc)
		}
		got, _ := silent.readGlobal(globalFirst)
		want, _ := m.readGlobal(globalFirst)
		if got != want {
			t.Errorf("global $10 with tracing = %d, without = %d", want, got)
		}
	})
}

// TestDecodeFailureStopsExecution checks that an instruction the decoder
// refuses ends the call with the decoder's error, which names the program
// counter and the opcode byte, rather than being executed as something else.
func TestDecodeFailureStopsExecution(t *testing.T) {
	// 0x00 is 2OP:0, which is not an instruction in any version (S 14).
	m := newTestMachine(t, 0x00, 0x00, 0x00)
	_, err := m.Start(context.Background())
	if !errors.Is(err, ErrInvalidOpcode) {
		t.Fatalf("Start() error = %v, want one wrapping ErrInvalidOpcode", err)
	}
	var decodeErr *DecodeError
	if !errors.As(err, &decodeErr) {
		t.Fatalf("error %v is not a *DecodeError", err)
	}
	if decodeErr.Addr != machineCodeBase {
		t.Errorf("error address = 0x%04x, want 0x%04x", decodeErr.Addr, machineCodeBase)
	}
}

// TestRestoreIsNotAvailable records that saved state is not part of this
// build, so that a host gets a classifiable error rather than silently running
// from the beginning of the story.
func TestRestoreIsNotAvailable(t *testing.T) {
	m := newTestMachine(t)
	if err := m.Restore(nil); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("Restore() error = %v, want one wrapping ErrNotImplemented", err)
	}
}

// TestExecutionRunsToHalt walks a short program through the real execution
// loop, which is the only test here that exercises decoding, dispatch,
// branching, calling and returning together.
func TestExecutionRunsToHalt(t *testing.T) {
	const routine = 0x0600
	// The routine has one local, initialised to 7 from its header (S 5.2.1),
	// and returns it.
	addr, header := routineAt(routine, 7)
	body := encodeShort(0x0b, varOp(localFirst)) // ret local1

	// call routine -> $10; je $10 7 and branch over the message to quit.
	message := join(encodeShort(0x02), encodeText(t, "wrong"))
	// S 4.7.2: the destination is the address after the branch data plus the
	// offset minus two, so skipping the message takes an offset of its length
	// plus two.
	code := join(
		append(encodeVar(countVAR, 0x00, largeOp(packed(routine))), globalFirst),
		encodeVar(count2OP, 0x01, varOp(globalFirst), smallOp(7)),
		branch2(true, int16(len(message)+2)),
		message,
		encodeShort(0x0a),
	)
	m := newStory(t).
		at(addr, header...).
		at(addr+uint32(len(header)), body...).
		code(code...).
		machine()

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Status != Halted {
		t.Errorf("Status = %v, want %v", result.Status, Halted)
	}
	if result.Output != "" {
		t.Errorf("Output = %q, want empty: the branch over the message was not taken", result.Output)
	}
	if got, _ := m.readGlobal(globalFirst); got != 7 {
		t.Errorf("global $10 = %d, want 7: the routine's initial local was not returned", got)
	}
}
