package zmachine

import "log/slog"

// Zero-operand instructions (S 14, 0OP).

// execute0OP carries out an instruction with no operands.
func (m *Machine) execute0OP(inst *instruction, _ []uint16) (control, error) {
	switch inst.op {
	case opRTrue:
		// S 15, rtrue: return true, that is 1, from the current routine.
		if err := m.returnFromRoutine(1); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opRFalse:
		// S 15, rfalse: return false, that is 0.
		if err := m.returnFromRoutine(0); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opPrint:
		// S 15, print: print the literal string that follows the opcode. The
		// decoder has already decoded it (S 4.8).
		if err := m.printText(inst.text); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opPrintRet:
		// S 15, print_ret: print the literal string, then a new-line, then
		// return true.
		if err := m.printText(inst.text); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if err := m.printNewLine(); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if err := m.returnFromRoutine(1); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opNop:
		// S 15, nop: no operation.
		return controlContinue, nil

	case opSave:
		// S 15, save: attempt to save and branch if it succeeded. This engine
		// snapshots at request boundaries on the host's behalf and has no
		// filesystem, so an in-story save cannot succeed and reports failure
		// by not branching. That is valid Version 3 behaviour: a story is
		// required to cope with a save that fails (spec S 24).
		m.logger.Debug("in-story save refused", slog.Uint64("pc", uint64(inst.addr)))
		return m.branch(inst, false)

	case opRestore:
		// S 15, restore: in Version 3 the branch is never actually made, since
		// either the story has picked up from where it was saved or the load
		// failed. With no save to load, the load fails and the branch is not
		// taken.
		m.logger.Debug("in-story restore refused", slog.Uint64("pc", uint64(inst.addr)))
		return m.branch(inst, false)

	case opRestart:
		// S 15, restart: restore the whole state from the original story file.
		if err := m.restart(); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opRetPopped:
		// S 15, ret_popped: pop the top of the stack and return it.
		value, err := m.pop()
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if err := m.returnFromRoutine(value); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opPop:
		// S 15, pop: throw away the top item on the stack.
		if _, err := m.pop(); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opQuit:
		// S 15, quit: exit the story immediately. The host is told the story
		// halted; the process is never touched (spec S 27).
		return controlHalt, nil

	case opNewLine:
		// S 15, new_line: print a carriage return.
		if err := m.printNewLine(); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opShowStatus:
		// S 15, show_status: display and update the status line now. The
		// status line is reported to the host on the Result rather than drawn
		// (spec S 13), so updating it is all there is to do.
		if err := m.updateStatusLine(); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opVerify:
		// S 15, verify: branch if the sum of the bytes of the file from $0040
		// onwards, modulo $10000, matches the checksum in the header. Both
		// values were computed when the story was loaded, over the declared
		// file length only, so the padding some story files carry beyond that
		// length is excluded. An early Version 3 story with no checksum in its
		// header simply fails verification, which is the condition S 15
		// describes rather than an error.
		return m.branch(inst, m.story.computed == m.story.checksum)

	default:
		return controlContinue, m.fault(inst, ErrInvalidOpcode, "not dispatched")
	}
}
