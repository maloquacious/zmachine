package zmachine

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Layout of the story built by newStory. It is the story from
// validTestHeader enlarged to 4K so that hand-built code and routines have
// room, with the program counter starting well inside high memory:
//
//	0x0000 header
//	0x0040 global variables table (480 bytes, S 6.2)
//	0x0220 object table
//	0x0280 abbreviations table
//	0x0300 spare dynamic memory, used as scratch by tests
//	0x0340 base of static memory
//	0x0348 dictionary
//	0x0360 base of high memory
//	0x0400 initial program counter; hand-built code
//	0x1000 end of file
const (
	machineCodeBase  = 0x0400
	machineScratch   = 0x0300 // scratch address in dynamic memory
	machineStaticEnd = 0x1000
)

// storyBuilder assembles a story image byte by byte. Tests use it instead of a
// real story so that each one proves a rule with the smallest machine state
// that can express it.
type storyBuilder struct {
	t    *testing.T
	data []byte
}

// newStory starts a story whose initial program counter is machineCodeBase.
func newStory(t *testing.T) *storyBuilder {
	t.Helper()
	h := validTestHeader()
	h.size = machineStaticEnd
	h.initialPC = machineCodeBase
	return &storyBuilder{t: t, data: h.build()}
}

// at writes bytes into the image at addr.
func (b *storyBuilder) at(addr uint32, bytes ...byte) *storyBuilder {
	b.t.Helper()
	if int(addr)+len(bytes) > len(b.data) {
		b.t.Fatalf("%d byte(s) at 0x%04x do not fit in the %d-byte fixture", len(bytes), addr, len(b.data))
	}
	copy(b.data[addr:], bytes)
	return b
}

// code writes bytes at the initial program counter.
func (b *storyBuilder) code(bytes ...byte) *storyBuilder {
	b.t.Helper()
	return b.at(machineCodeBase, bytes...)
}

// flags1 sets Flags 1 in the header before the story is loaded.
func (b *storyBuilder) flags1(value uint8) *storyBuilder {
	b.data[hdrFlags1] = value
	return b
}

// checksum recomputes the header checksum over the image as it now stands, so
// that a story assembled by hand can pass verify (S 15, verify). The checksum
// field lies below $0040 and so is not part of the sum it holds.
func (b *storyBuilder) checksum() *storyBuilder {
	sum := computeChecksum(b.data)
	b.data[hdrChecksum] = uint8(sum >> 8)
	b.data[hdrChecksum+1] = uint8(sum)
	return b
}

// load validates the image and returns the immutable Story.
func (b *storyBuilder) load() *Story {
	b.t.Helper()
	story, err := LoadStory(b.data)
	if err != nil {
		b.t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	return story
}

// machine loads the story and creates a Machine from it. Every machine a test
// builds is seeded, so that no assertion depends on production entropy.
func (b *storyBuilder) machine(opts ...Option) *Machine {
	b.t.Helper()
	opts = append([]Option{WithRandomSeed(1)}, opts...)
	m, err := New(b.load(), opts...)
	if err != nil {
		b.t.Fatalf("New() error = %v, want nil", err)
	}
	return m
}

// newTestMachine builds a machine whose program counter is at the given code.
func newTestMachine(t *testing.T, code ...byte) *Machine {
	t.Helper()
	return newStory(t).code(code...).machine()
}

// Operand encoders. They build the instruction encodings of S 4.3 and S 4.4 so
// that a test can state an instruction rather than a run of bytes.
type opnd struct {
	kind  operandType
	value uint16
}

func smallOp(v uint8) opnd    { return opnd{kind: operandSmall, value: uint16(v)} }
func largeOp(v uint16) opnd   { return opnd{kind: operandLarge, value: v} }
func varOp(number uint8) opnd { return opnd{kind: operandVariable, value: uint16(number)} }

// appendOperand appends an operand's bytes (S 4.2).
func appendOperand(dst []byte, op opnd) []byte {
	if op.kind == operandLarge {
		return append(dst, uint8(op.value>>8), uint8(op.value))
	}
	return append(dst, uint8(op.value))
}

// encodeShort builds a short-form instruction: 0OP when no operand is given
// and 1OP when one is (S 4.3.1).
func encodeShort(number uint8, ops ...opnd) []byte {
	if len(ops) == 0 {
		return []byte{0x80 | uint8(operandOmitted)<<shortTypeShift | number&shortNumMask}
	}
	out := []byte{0x80 | uint8(ops[0].kind)<<shortTypeShift | number&shortNumMask}
	return appendOperand(out, ops[0])
}

// encodeLong builds a long-form 2OP instruction, whose two operands must each
// be a small constant or a variable (S 4.3.2, S 4.4.2).
func encodeLong(number uint8, a, b opnd) []byte {
	first := number & opcodeNumMask
	if a.kind == operandVariable {
		first |= longType1Bit
	}
	if b.kind == operandVariable {
		first |= longType2Bit
	}
	return []byte{first, uint8(a.value), uint8(b.value)}
}

// encodeVar builds a variable-form instruction (S 4.3.3), which is how a 2OP
// opcode takes a large constant or more than two operands.
func encodeVar(count operandCount, number uint8, ops ...opnd) []byte {
	first := uint8(formBitsVar) | number&opcodeNumMask
	if count == countVAR {
		first |= varCountBit
	}
	// S 4.4.3: four 2-bit type fields, bits 6 and 7 being the first. Unused
	// fields are $$11, omitted.
	types := uint8(0)
	for i := 0; i < operandTypeFields; i++ {
		kind := operandOmitted
		if i < len(ops) {
			kind = ops[i].kind
		}
		types |= uint8(kind) << (2 * (operandTypeFields - 1 - i))
	}
	out := []byte{first, types}
	for _, op := range ops {
		out = appendOperand(out, op)
	}
	return out
}

// branch1 builds a one-byte branch, whose offset is unsigned and so can only
// go forwards (S 4.7).
func branch1(onTrue bool, offset uint8) []byte {
	b := branchShort | offset&branchOffsetHi
	if onTrue {
		b |= branchOnTrue
	}
	return []byte{b}
}

// branch2 builds a two-byte branch, whose offset is a signed 14-bit number
// (S 4.7).
func branch2(onTrue bool, offset int16) []byte {
	value := uint16(offset) & 0x3fff
	first := uint8(value >> 8)
	if onTrue {
		first |= branchOnTrue
	}
	return []byte{first, uint8(value)}
}

// join concatenates encoded instruction fragments.
func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// step decodes and executes the instruction at the program counter, exactly as
// the execution loop does.
func step(t *testing.T, m *Machine) (control, error) {
	t.Helper()
	inst, err := decodeInstruction(m.mem, m.pc)
	if err != nil {
		t.Fatalf("decodeInstruction(0x%04x) error = %v, want nil", m.pc, err)
	}
	m.pc = inst.next
	return m.execute(&inst)
}

// mustStep executes one instruction and fails the test if it faults.
func mustStep(t *testing.T, m *Machine) control {
	t.Helper()
	ctl, err := step(t, m)
	if err != nil {
		t.Fatalf("execute() error = %v, want nil", err)
	}
	return ctl
}

// stepErr executes one instruction and returns the error it must have raised.
func stepErr(t *testing.T, m *Machine) error {
	t.Helper()
	_, err := step(t, m)
	if err == nil {
		t.Fatalf("execute() error = nil, want an error")
	}
	return err
}

// assertExecutionError checks that err is an ExecutionError naming the given
// program counter and classified as target.
func assertExecutionError(t *testing.T, err error, pc uint32, target error) {
	t.Helper()
	var execErr *ExecutionError
	if !errors.As(err, &execErr) {
		t.Fatalf("error %v is not an *ExecutionError", err)
	}
	if execErr.PC != pc {
		t.Errorf("error PC = 0x%04x, want 0x%04x", execErr.PC, pc)
	}
	if !errors.Is(err, target) {
		t.Errorf("error %v does not wrap %v", err, target)
	}
}

func TestNewRejectsBadArguments(t *testing.T) {
	story := newStory(t).load()

	if _, err := New(nil); !errors.Is(err, ErrInvalidStory) {
		t.Errorf("New(nil) error = %v, want one wrapping ErrInvalidStory", err)
	}
	if _, err := New(story, nil); err == nil {
		t.Errorf("New(story, nil) error = nil, want an error")
	}
	if _, err := New(story, WithLogger(nil)); err == nil {
		t.Errorf("WithLogger(nil) error = nil, want an error")
	}
	if _, err := New(story, WithTracer(nil)); err == nil {
		t.Errorf("WithTracer(nil) error = nil, want an error")
	}
	if _, err := New(story, WithInstructionLimit(0)); err == nil {
		t.Errorf("WithInstructionLimit(0) error = nil, want an error")
	}
}

// TestNewSetsInterpreterHeaderFields covers the header fields an interpreter
// must set after loading a story (the "Rst" entries of S 11.1): bit 4 of
// Flags 1 says the status line is unavailable, bit 5 that screen splitting is,
// and bit 0 of Flags 2 that transcription is on.
func TestNewSetsInterpreterHeaderFields(t *testing.T) {
	// Start from a story that claims the opposite of everything the engine
	// will claim, so that each bit must actually have been written.
	m := newStory(t).flags1(flags1NoStatusLine | flags1VariablePitch).machine()

	flags1, err := m.mem.readByte(hdrFlags1)
	if err != nil {
		t.Fatalf("readByte(Flags 1) error = %v", err)
	}
	if flags1&flags1NoStatusLine != 0 {
		t.Errorf("Flags 1 = 0x%02x: bit 4 set, but the engine produces a status line (S 8.2)", flags1)
	}
	if flags1&flags1SplitAvailable == 0 {
		t.Errorf("Flags 1 = 0x%02x: bit 5 clear, but the engine models the upper window (S 8.6.1.2)", flags1)
	}
	if flags1&flags1VariablePitch != 0 {
		t.Errorf("Flags 1 = 0x%02x: bit 6 set, but the engine has no fonts", flags1)
	}

	flags2, err := m.mem.readWord(hdrFlags2)
	if err != nil {
		t.Fatalf("readWord(Flags 2) error = %v", err)
	}
	if flags2&flags2Transcript != 0 {
		t.Errorf("Flags 2 = 0x%04x: bit 0 set, but no transcript stream exists (S 11.1.2)", flags2)
	}
}

// TestMachinesAreIsolated checks the central concurrency promise of spec S 28:
// two machines built from one story share no mutable state. Dynamic memory is
// private, so a global written in one is unchanged in the other, and the
// stacks and program counters are separate too.
func TestMachinesAreIsolated(t *testing.T) {
	story := newStory(t).load()

	first, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	second, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := first.writeGlobal(globalFirst, 0x1234); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if err := first.push(0x5678); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	first.pc = 0x0500

	got, err := second.readGlobal(globalFirst)
	if err != nil {
		t.Fatalf("readGlobal() error = %v", err)
	}
	if got != 0 {
		t.Errorf("second machine global $10 = 0x%04x, want 0x0000: dynamic memory is shared", got)
	}
	if second.stackDepth() != 0 {
		t.Errorf("second machine stack depth = %d, want 0", second.stackDepth())
	}
	if second.pc != story.initialPC {
		t.Errorf("second machine pc = 0x%04x, want 0x%04x", second.pc, story.initialPC)
	}
	// The story image itself must never be written through.
	if story.image[story.globals] != 0 || story.image[story.globals+1] != 0 {
		t.Errorf("story image global $10 = 0x%02x%02x, want 0x0000: the Story was mutated",
			story.image[story.globals], story.image[story.globals+1])
	}
}

// TestRandomSeedIsDeterministic checks that seeding twice with the same value
// produces the same sequence, which S 2.4.2 requires of the predictable state.
func TestRandomSeedIsDeterministic(t *testing.T) {
	sequence := func(seed uint64) []uint16 {
		m := newStory(t).machine(WithRandomSeed(seed))
		out := make([]uint16, 10)
		for i := range out {
			out[i] = m.randomInRange(1000)
		}
		return out
	}

	first, second := sequence(42), sequence(42)
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("draw %d = %d and %d, want equal: the same seed must give the same sequence", i, first[i], second[i])
		}
	}
	if other := sequence(43); other[0] == first[0] && other[1] == first[1] && other[2] == first[2] {
		t.Errorf("seeds 42 and 43 produced the same first three draws")
	}
}

// TestRandomInRange checks the uniformity requirement of S 2.4.1 at its edges:
// every draw must lie between 1 and n inclusive, and a range of 1 always
// yields 1.
func TestRandomInRange(t *testing.T) {
	m := newStory(t).machine(WithRandomSeed(7))

	for _, n := range []int16{1, 2, 6, 100, 32767} {
		seen := make(map[uint16]bool)
		for i := 0; i < 2000; i++ {
			got := m.randomInRange(n)
			if got < 1 || int16(got) > n {
				t.Fatalf("randomInRange(%d) = %d, want 1 to %d", n, got, n)
			}
			seen[got] = true
		}
		if n == 1 && len(seen) != 1 {
			t.Errorf("randomInRange(1) produced %d distinct values, want 1", len(seen))
		}
		if n == 6 && len(seen) != 6 {
			t.Errorf("randomInRange(6) produced %d of the 6 possible values in 2000 draws", len(seen))
		}
	}
}

// TestRandomStateRoundTrip checks that the generator's state can be taken out
// of the machine and put back, which is what carrying it across a request
// boundary needs.
func TestRandomStateRoundTrip(t *testing.T) {
	m := newStory(t).machine(WithRandomSeed(99))
	for i := 0; i < 5; i++ {
		m.randomInRange(1000)
	}

	state, predictable, err := m.randomState()
	if err != nil {
		t.Fatalf("randomState() error = %v", err)
	}
	if !predictable {
		t.Errorf("predictable = false, want true: the machine was seeded")
	}

	want := make([]uint16, 5)
	for i := range want {
		want[i] = m.randomInRange(1000)
	}

	if err := m.setRandomState(randomKindPCG, state, predictable); err != nil {
		t.Fatalf("setRandomState() error = %v", err)
	}
	for i := range want {
		if got := m.randomInRange(1000); got != want[i] {
			t.Fatalf("draw %d after restore = %d, want %d", i, got, want[i])
		}
	}

	if err := m.setRandomState(randomKindPCG, []byte("not a pcg state"), true); !errors.Is(err, ErrInvalidState) {
		t.Errorf("setRandomState(garbage) error = %v, want one wrapping ErrInvalidState", err)
	}
}

// TestRestartResetsState covers S 6.1.3 and S 15 restart: dynamic memory comes
// back from the story file, the stack is emptied and the program counter
// returns to the initial one, while the transcription and fixed-pitch bits of
// Flags 2 survive.
func TestRestartResetsState(t *testing.T) {
	m := newTestMachine(t)

	if err := m.writeGlobal(globalFirst, 0xbeef); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if err := m.push(1); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	m.pc = 0x0500
	flags2, _ := m.mem.readWord(hdrFlags2)
	if err := m.mem.writeWord(hdrFlags2, flags2|flags2FixedPitch); err != nil {
		t.Fatalf("writeWord(Flags 2) error = %v", err)
	}

	if err := m.restart(); err != nil {
		t.Fatalf("restart() error = %v", err)
	}

	if got, _ := m.readGlobal(globalFirst); got != 0 {
		t.Errorf("global $10 = 0x%04x, want 0x0000: dynamic memory was not reloaded", got)
	}
	if m.stackDepth() != 0 {
		t.Errorf("stack depth = %d, want 0", m.stackDepth())
	}
	if len(m.frames) != 1 {
		t.Errorf("call depth = %d frames, want 1", len(m.frames))
	}
	if m.pc != machineCodeBase {
		t.Errorf("pc = 0x%04x, want 0x%04x", m.pc, machineCodeBase)
	}
	// S 15, restart: the fixed-pitch bit is one of the two pieces of
	// information surviving from the previous state.
	got, _ := m.mem.readWord(hdrFlags2)
	if got&flags2FixedPitch == 0 {
		t.Errorf("Flags 2 = 0x%04x: the fixed-pitch bit did not survive the restart", got)
	}
}

// TestRestartKeepsSeededMachineReproducible checks the documented policy for
// the random generator across a restart. S 2.4 puts the generator into its
// "random" state when the game restarts; a machine the host seeded returns to
// that seed instead, so that seeding makes a whole run reproducible.
func TestRestartKeepsSeededMachineReproducible(t *testing.T) {
	m := newTestMachine(t)
	before := m.randomInRange(10000)

	if err := m.restart(); err != nil {
		t.Fatalf("restart() error = %v", err)
	}
	if after := m.randomInRange(10000); after != before {
		t.Errorf("first draw after restart = %d, want %d", after, before)
	}
	if !m.predictable {
		t.Errorf("predictable = false after restarting a seeded machine")
	}
}

// TestUpdateStatusLine covers S 8.2.2 and S 8.2.3: the location object comes
// from the first global, and the right-hand side is either score and turns or
// hours and minutes depending on bit 1 of Flags 1.
func TestUpdateStatusLine(t *testing.T) {
	t.Run("score game", func(t *testing.T) {
		m := newTestMachine(t)
		mustWriteGlobals(t, m, 24, unsigned(-5), 130)

		if err := m.updateStatusLine(); err != nil {
			t.Fatalf("updateStatusLine() error = %v", err)
		}
		want := StatusLine{Available: true, Object: 24, Score: -5, Turns: 130}
		if m.status != want {
			t.Errorf("status = %+v, want %+v", m.status, want)
		}
	})

	t.Run("time game", func(t *testing.T) {
		// S 8.2.1: bit 1 of Flags 1 set means the story is a time game.
		m := newStory(t).flags1(flags1TimeGame).machine()
		mustWriteGlobals(t, m, 7, 13, 45)

		if err := m.updateStatusLine(); err != nil {
			t.Fatalf("updateStatusLine() error = %v", err)
		}
		want := StatusLine{Available: true, Object: 7, TimeGame: true, Hours: 13, Minutes: 45}
		if m.status != want {
			t.Errorf("status = %+v, want %+v", m.status, want)
		}
	})
}

// mustWriteGlobals sets the first three global variables, which are the ones
// the status line reads (S 8.2.2, S 8.2.3).
func mustWriteGlobals(t *testing.T, m *Machine, values ...uint16) {
	t.Helper()
	for i, v := range values {
		if err := m.writeGlobal(uint8(globalFirst+i), v); err != nil {
			t.Fatalf("writeGlobal($%02x) error = %v", globalFirst+i, err)
		}
	}
}

// captureHandler collects log records so that a test can inspect diagnostics
// without touching the process-default logger.
type captureHandler struct {
	records []string
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" ")
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(a.Value.String())
		return true
	})
	h.records = append(h.records, b.String())
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

// TestLoggerReceivesDiagnosticsNotStoryOutput checks the logging rules of spec
// S 30 and of the project's guidance: a machine logs through the injected
// logger only, story output never reaches the log, and turning logging on does
// not change what the story does.
func TestLoggerReceivesDiagnosticsNotStoryOutput(t *testing.T) {
	const secret = "the mailbox is closed"
	// print, then quit.
	code := join(
		encodeShort(0x02), encodeText(t, secret),
		encodeShort(0x0a),
	)

	handler := &captureHandler{}
	logged := newStory(t).code(code...).machine(WithLogger(slog.New(handler)))
	silent := newTestMachine(t, code...)

	loggedResult, err := logged.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	silentResult, err := silent.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	if loggedResult.Output != silentResult.Output || loggedResult.Status != silentResult.Status ||
		loggedResult.StatusLine != silentResult.StatusLine {
		t.Errorf("logging changed the result: %+v vs %+v", loggedResult, silentResult)
	}
	if !strings.Contains(loggedResult.Output, secret) {
		t.Fatalf("Output = %q, want it to contain %q", loggedResult.Output, secret)
	}
	if len(handler.records) == 0 {
		t.Fatalf("no diagnostics were logged")
	}
	for _, record := range handler.records {
		if strings.Contains(record, secret) {
			t.Errorf("log record %q contains story output", record)
		}
	}
}

// encodeText encodes an ASCII string as a Z-string (S 3.2), for the inline
// text of print and print_ret (S 4.8).
//
// Only lower-case letters and spaces are encoded directly; every other
// character goes through the ten-bit ZSCII escape of S 3.4, which keeps the
// encoder short without limiting what a test can print.
func encodeText(t *testing.T, text string) []byte {
	t.Helper()

	var chars []uint8
	for _, r := range text {
		switch {
		case r == ' ':
			chars = append(chars, zcharSpace)
		case r >= 'a' && r <= 'z':
			chars = append(chars, uint8(r-'a')+zcharAlphabetLow)
		default:
			code, ok := zsciiFromRune(r)
			if !ok {
				t.Fatalf("encodeText: %q has no ZSCII code", r)
			}
			// S 3.4: shift to A2, then Z-character 6, then the code in two
			// halves of five bits.
			chars = append(chars, zcharShiftA2, zcharEscape, code>>zsciiEscapeShift&zcharMask, code&zcharMask)
		}
	}
	// S 3.2: the string is a whole number of words of three Z-characters,
	// padded with Z-character 5, which is a shift that prints nothing when it
	// falls at the end of a string (S 3.2.4).
	for len(chars)%zcharsPerWord != 0 {
		chars = append(chars, zcharShiftA2)
	}
	if len(chars) == 0 {
		chars = []uint8{zcharShiftA2, zcharShiftA2, zcharShiftA2}
	}

	var out []byte
	for i := 0; i < len(chars); i += zcharsPerWord {
		word := uint16(chars[i])<<10 | uint16(chars[i+1])<<5 | uint16(chars[i+2])
		if i+zcharsPerWord >= len(chars) {
			word |= zstringEndBit
		}
		out = append(out, uint8(word>>8), uint8(word))
	}
	return out
}

// TestEncodeTextRoundTrip checks the test encoder itself against the decoder
// from S 3, so that a failing opcode test cannot be blamed on the fixture.
func TestEncodeTextRoundTrip(t *testing.T) {
	for _, want := range []string{"", "a", "hello world", "west of house", "score: 10!"} {
		encoded := encodeText(t, want)
		m := newStory(t).at(machineCodeBase, encoded...).load()
		got, _, err := decodeStringAt(newMemory(m), machineCodeBase)
		if err != nil {
			t.Fatalf("decodeStringAt(%q) error = %v", want, err)
		}
		if got != want {
			t.Errorf("round trip of %q = %q", want, got)
		}
	}
}

// TestResultCarriesCapturedOutput checks spec S 12: everything the story
// prints is captured, with its whitespace preserved exactly, and nothing else
// joins it.
func TestResultCarriesCapturedOutput(t *testing.T) {
	const text = "west of house"
	code := join(
		encodeShort(0x02), encodeText(t, text), // print
		encodeShort(0x0b), // new_line
		encodeShort(0x0a), // quit
	)
	m := newTestMachine(t, code...)

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Output != text+"\n" {
		t.Errorf("Output = %q, want %q", result.Output, text+"\n")
	}
	if result.Status != Halted {
		t.Errorf("Status = %v, want %v", result.Status, Halted)
	}
	if !m.Halted() {
		t.Errorf("Halted() = false, want true")
	}
	if !bytes.Equal([]byte(result.UpperWindow), nil) {
		t.Errorf("UpperWindow = %q, want empty", result.UpperWindow)
	}
}

// TestConcurrentMachinesShareOneStory covers spec S 28: a Story is safe for
// concurrent use by many Machines, and players executing the same story
// concurrently have completely isolated game states. Run under -race, this
// also proves that nothing mutable is shared between them.
func TestConcurrentMachinesShareOneStory(t *testing.T) {
	// Each machine writes its own number into a global, reads it back and
	// prints it, so a shared byte anywhere would show up as a wrong answer.
	code := join(
		encodeVar(count2OP, 0x0d, smallOp(globalFirst), varOp(localFirst)), // store $10 local1
		encodeVar(countVAR, 0x06, varOp(globalFirst)),                      // print_num $10
		encodeShort(0x0a), // quit
	)
	const routine = 0x0600
	addr, header := routineAt(routine, 0)
	story := newStory(t).at(addr, header...).code(code...).load()

	const players = 16
	results := make([]string, players)
	errs := make([]error, players)

	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(players)
	for i := 0; i < players; i++ {
		go func(i int) {
			defer done.Done()
			m, err := New(story, WithRandomSeed(uint64(i)))
			if err != nil {
				errs[i] = err
				return
			}
			// Give each machine a routine frame holding its own number, which
			// the code above copies into a global and prints.
			m.frames = append(m.frames, frame{numLocals: 1, locals: [maxLocalsV3]uint16{uint16(1000 + i)}})
			start.Wait()
			result, err := m.Start(context.Background())
			results[i], errs[i] = result.Output, err
		}(i)
	}
	start.Done()
	done.Wait()

	for i := 0; i < players; i++ {
		if errs[i] != nil {
			t.Fatalf("player %d: Start() error = %v", i, errs[i])
		}
		if want := strconv.Itoa(1000 + i); results[i] != want {
			t.Errorf("player %d: Output = %q, want %q", i, results[i], want)
		}
	}
}

// FuzzExecute asserts that executing arbitrary bytes as Version 3 code never
// panics, never allocates without bound and always terminates. A story is
// untrusted input (spec S 26), and the bytes it jumps to are the least trusted
// part of it.
func FuzzExecute(f *testing.F) {
	f.Add([]byte{0xba})                         // quit
	f.Add([]byte{0xe0, 0x3f, 0x00, 0x00, 0x00}) // call 0 -> sp
	f.Add([]byte{0x8c, 0xff, 0xff})             // jump -1, a loop on itself
	f.Add([]byte{0x54, 0x01, 0x02, 0x10})       // add 1 2 -> $10
	f.Add([]byte{0xb2, 0x00, 0x00})             // print with an unterminated string
	f.Add(bytes.Repeat([]byte{0xff}, 32))

	// The fixture is built without a testing.T so that the seed corpus and the
	// fuzz target share one image.
	header := validTestHeader()
	header.size = machineStaticEnd
	header.initialPC = machineCodeBase
	base := header.build()

	f.Fuzz(func(t *testing.T, code []byte) {
		if len(code) > machineStaticEnd-machineCodeBase {
			t.Skip()
		}
		image := make([]byte, len(base))
		copy(image, base)
		copy(image[machineCodeBase:], code)

		fuzzed, err := LoadStory(image)
		if err != nil {
			t.Skip()
		}
		m, err := New(fuzzed, WithRandomSeed(1), WithInstructionLimit(500))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		// Bound the stack and the call chain far below their defaults so that
		// a fuzz case cannot spend the whole budget growing them.
		m.maxStack, m.maxDepth = 256, 32

		// The result and the error are both acceptable outcomes: the only
		// invariant is that arbitrary code cannot crash the host.
		if _, err := m.Start(context.Background()); err != nil {
			var execErr *ExecutionError
			var decodeErr *DecodeError
			var memErr *MemoryError
			var textErr *TextError
			switch {
			case errors.As(err, &execErr), errors.As(err, &decodeErr),
				errors.As(err, &memErr), errors.As(err, &textErr),
				errors.Is(err, ErrExecutionLimit):
			default:
				t.Fatalf("unclassified error %v", err)
			}
		}
	})
}
