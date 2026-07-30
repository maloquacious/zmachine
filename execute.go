package zmachine

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// The execution loop.
//
// One call to Start or Run decodes and executes instructions from the program
// counter until the story asks for a line of input it has not been given,
// terminates itself, exceeds the host's instruction limit, has its context
// cancelled, or faults.
//
// The loop never blocks: an input boundary is a value returned to the host,
// not a wait (spec S 4).

// control says what the execution loop should do after an instruction.
type control uint8

const (
	// controlContinue means execute the instruction at the program counter.
	controlContinue control = iota
	// controlHalt means the story terminated itself (S 15, quit).
	controlHalt
	// controlSuspend means the story asked for a line of input that the host
	// has not supplied, which is the input boundary of spec S 4.
	controlSuspend
)

// Start begins execution at the story's initial program counter (S 5.5) and
// runs until the first input boundary or termination (spec S 10).
//
// It supplies no player input: a new story usually prints its banner and
// opening text before asking for the first command.
func (m *Machine) Start(ctx context.Context) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("zmachine: Start: context is nil")
	}
	if m.halted {
		return Result{}, fmt.Errorf("zmachine: Start: the story has already halted: %w", ErrExecutionFault)
	}
	if m.started {
		return Result{}, fmt.Errorf("zmachine: Start: execution has already begun; use Run to continue it: %w", ErrExecutionFault)
	}
	m.started = true
	m.input, m.hasInput = "", false
	return m.run(ctx)
}

// Run supplies one line of player input and executes until the next input
// boundary or termination (spec S 11).
//
// The input is given to the story exactly as an interactive interpreter would
// give it: the engine performs the transformations Version 3 requires and
// leaves the story's own parser to interpret the command. Once the line has
// been consumed, the next request for input returns WaitingForInput.
func (m *Machine) Run(ctx context.Context, input string) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("zmachine: Run: context is nil")
	}
	if m.halted {
		return Result{}, fmt.Errorf("zmachine: Run: the story has already halted: %w", ErrExecutionFault)
	}
	m.started = true
	m.input, m.hasInput = input, true
	return m.run(ctx)
}

// run is the execution loop shared by Start and Run.
func (m *Machine) run(ctx context.Context) (Result, error) {
	m.out.beginInvocation()
	m.executed = 0

	for {
		// spec S 25: the limit protects the host from a story that never
		// stops. It is counted per call, so a story that legitimately runs a
		// long turn is not penalised for an earlier one.
		if m.executed >= m.instructionLimit {
			return Result{}, fmt.Errorf("zmachine: pc 0x%04x: %d instructions executed: %w",
				m.pc, m.executed, ErrExecutionLimit)
		}
		// spec S 25: the context need not be checked after every instruction,
		// but the latency must stay small.
		if m.executed%contextCheckInterval == 0 {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
		}
		m.executed++

		inst, err := decodeInstruction(m.mem, m.pc)
		if err != nil {
			return Result{}, err
		}

		pc := m.pc
		// The program counter moves past the instruction before it runs, so
		// that an instruction which does not move it itself simply continues
		// with the next one, and one that does - a branch, jump, call or
		// return - overwrites it.
		m.pc = inst.next

		ctl, err := m.execute(&inst)
		if err != nil {
			m.logger.Warn("execution fault",
				slog.Uint64("pc", uint64(pc)),
				slog.String("opcode", inst.op.String()),
				slog.String("error", err.Error()))
			return Result{}, err
		}

		switch ctl {
		case controlHalt:
			m.halted = true
			m.logger.Debug("story halted", slog.Uint64("pc", uint64(pc)))
			return m.result(Halted)
		case controlSuspend:
			// The program counter is left at the input instruction so that the
			// next call re-executes it with the input the host supplies.
			m.pc = pc
			m.logger.Debug("input boundary reached", slog.Uint64("pc", uint64(pc)))
			return m.result(WaitingForInput)
		}
	}
}

// result builds the Result for an invocation that stopped with the given
// status.
//
// An invocation that stopped at an input boundary carries a snapshot of the
// state execution resumes from (spec S 23). A halted one does not: the story
// ended itself with quit and there is nothing to resume, so State is nil and a
// host holding the previous turn's state still has the last resumable point.
func (m *Machine) result(status Status) (Result, error) {
	result := Result{
		Output:      string(m.out.screen),
		UpperWindow: string(m.out.upper),
		StatusLine:  m.status,
		Status:      status,
	}
	if status == Halted {
		return result, nil
	}

	state, err := m.snapshot()
	if err != nil {
		return Result{}, err
	}
	result.State = state
	return result, nil
}

// execute evaluates an instruction's operands and carries it out.
func (m *Machine) execute(inst *instruction) (control, error) {
	// spec S 4: an unsatisfied line-input instruction is a suspension boundary
	// returned to the host, never a wait. The check comes before the operands
	// are evaluated, because evaluating one can pop the evaluation stack
	// (S 4.2.2) and this instruction will be executed again, from the same
	// program counter, when the host supplies a line.
	if inst.op == opSRead && !m.hasInput {
		// S 15, read: in Versions 1 to 3 the status line is redisplayed before
		// the keyboard is read, and this boundary is where the keyboard would
		// be waited on. Updating it here as well as when the instruction is
		// re-executed hands the host the status line the player would be
		// looking at while typing; the globals cannot change in between, so the
		// two updates agree and S 8.2.4 still sees one update per read.
		if err := m.updateStatusLine(); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlSuspend, nil
	}

	if m.tracing() {
		m.beginTrace()
	}

	var values [maxOperandsV3]uint16
	ops := values[:inst.numOps]
	// S 4.5.2: operands are evaluated in the order they appear, which matters
	// because reading a variable operand can pop the evaluation stack.
	for i, op := range inst.operands() {
		if op.kind != operandVariable {
			ops[i] = op.value
			continue
		}
		value, err := m.readVariable(uint8(op.value))
		if err != nil {
			return controlContinue, m.fail(inst, fmt.Errorf("reading operand %d: %w", i+1, err))
		}
		ops[i] = value
	}

	pc, depth := inst.addr, len(m.frames)-1

	var ctl control
	var err error
	switch inst.op.count() {
	case count0OP:
		ctl, err = m.execute0OP(inst, ops)
	case count1OP:
		ctl, err = m.execute1OP(inst, ops)
	case count2OP:
		ctl, err = m.execute2OP(inst, ops)
	case countVAR:
		ctl, err = m.executeVAR(inst, ops)
	default:
		// The decoder only builds opcodes in these four counts, so reaching
		// here would mean the decoder and this switch disagree.
		err = m.fault(inst, ErrInvalidOpcode, "unknown operand count %d", inst.op.count())
	}
	if err != nil {
		return controlContinue, err
	}

	if m.tracing() {
		m.emitTrace(inst, pc, depth, ops)
	}
	return ctl, nil
}

// branch takes or does not take an instruction's branch (S 4.7).
//
// The branch is taken when the condition matches the polarity recorded in the
// branch data. A taken branch with an offset of 0 or 1 returns from the
// current routine with false or true instead of jumping (S 4.7.1).
func (m *Machine) branch(inst *instruction, condition bool) (control, error) {
	if !inst.hasBranch {
		// A non-branch instruction asking to branch is a defect in this
		// engine's dispatch, not in the story.
		return controlContinue, m.fault(inst, nil, "instruction has no branch data")
	}

	taken := condition == inst.branch.onTrue
	if m.tracing() {
		m.trace.branched = taken
	}
	if !taken {
		return controlContinue, nil
	}

	if returns, value := inst.branch.returns(); returns {
		result := uint16(0)
		if value {
			result = 1
		}
		if err := m.returnFromRoutine(result); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil
	}

	// S 4.7.2: the destination is the address after the branch data, plus the
	// offset, minus two.
	target, err := inst.branch.target(inst.next)
	if err != nil {
		return controlContinue, m.fail(inst, err)
	}
	m.pc = target
	return controlContinue, nil
}

// store writes an instruction's result to the variable named by its store byte
// (S 4.6).
func (m *Machine) store(inst *instruction, value uint16) error {
	if !inst.hasStore {
		return m.fault(inst, nil, "instruction has no store variable")
	}
	if err := m.storeVariable(inst.store, value); err != nil {
		return m.fail(inst, err)
	}
	return nil
}

// storeBool writes a Z-machine truth value: false is 0 and true is 1
// (S 6.4.5).
func (m *Machine) storeBool(inst *instruction, value bool) error {
	if value {
		return m.store(inst, 1)
	}
	return m.store(inst, 0)
}

// operands checks that an instruction has between min and max operands. The
// decoder does not know how many operands an opcode should have, because that
// varies - je legally takes two to four (S 15, je) - so each instruction
// checks its own.
func (m *Machine) operands(inst *instruction, min, max int) error {
	n := int(inst.numOps)
	if n >= min && n <= max {
		return nil
	}
	if min == max {
		return m.fault(inst, nil, "takes %d operand(s), not %d", min, n)
	}
	return m.fault(inst, nil, "takes %d to %d operands, not %d", min, max, n)
}

// fault builds an ExecutionError for a condition this instruction ran into.
// A nil cause is classified as ErrExecutionFault.
func (m *Machine) fault(inst *instruction, cause error, format string, args ...any) *ExecutionError {
	if cause == nil {
		cause = ErrExecutionFault
	}
	return &ExecutionError{
		PC:     inst.addr,
		Op:     inst.op,
		Detail: fmt.Sprintf(format, args...),
		Err:    cause,
	}
}

// fail wraps an error raised while an instruction ran, adding the program
// counter and the opcode. The original error stays in the chain, so a caller
// can still classify it with errors.Is or errors.As.
func (m *Machine) fail(inst *instruction, err error) *ExecutionError {
	return &ExecutionError{
		PC:     inst.addr,
		Op:     inst.op,
		Detail: strings.TrimPrefix(err.Error(), "zmachine: "),
		Err:    err,
	}
}
