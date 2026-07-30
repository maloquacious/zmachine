package zmachine

// One-operand instructions (S 14, 1OP).

// execute1OP carries out an instruction with one operand.
func (m *Machine) execute1OP(inst *instruction, ops []uint16) (control, error) {
	if err := m.operands(inst, 1, 1); err != nil {
		return controlContinue, err
	}
	a := ops[0]

	switch inst.op {
	case opJZ:
		// S 15, jz: branch if a is zero.
		return m.branch(inst, a == 0)

	case opInc:
		// S 15, inc: increment the variable the operand names by 1. It is
		// signed, so -1 increments to 0, and the reference is indirect
		// (S 6.3.4).
		_, err := m.adjustVariable(inst, uint8(a), 1)
		return controlContinue, err

	case opDec:
		// S 15, dec: decrement the variable by 1. It is signed, so 0
		// decrements to -1.
		_, err := m.adjustVariable(inst, uint8(a), -1)
		return controlContinue, err

	case opPrintAddr:
		// S 15, print_addr: print the Z-encoded string at the given byte
		// address, which lies in dynamic or static memory.
		text, _, err := decodeStringAt(m.mem, uint32(a))
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if err := m.printText(text); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opRet:
		// S 15, ret: return from the current routine with the given value.
		if err := m.returnFromRoutine(a); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opJump:
		// S 15, jump: this is not a branch instruction. Its operand is an
		// ordinary 2-byte signed offset, and the destination is the address
		// after the instruction plus the offset minus two - the same formula
		// branches use (S 4.7.2).
		target, err := offsetAddress(inst.next, int32(signed(a))-branchBias)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		m.pc = target
		return controlContinue, nil

	case opPrintPaddr:
		// S 15, print_paddr: print the Z-encoded string at the given packed
		// address. In Versions 1 to 3 the byte address is twice the packed
		// address (S 1.2.3).
		text, _, err := decodeStringAtPacked(m.mem, a)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if err := m.printText(text); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opLoad:
		// S 15, load: store the value of the variable the operand names. The
		// reference is indirect, so naming the stack pointer reads the top
		// item in place rather than popping it (S 6.3.4).
		value, err := m.readVariableIndirect(uint8(a))
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, m.store(inst, value)

	case opNot:
		// S 15, not: bitwise NOT, all 16 bits reversed. In Versions 3 and 4
		// this is a 1OP instruction; from Version 5 it moves to the variable
		// set to make room for call_1n.
		return controlContinue, m.store(inst, ^a)

	case opGetSibling, opGetChild, opGetParent, opGetPropLen, opRemoveObj, opPrintObj:
		// The object model is not part of this build.
		return m.notImplemented(inst)

	default:
		return controlContinue, m.fault(inst, ErrInvalidOpcode, "not dispatched")
	}
}

// adjustVariable adds delta to the variable that number names, using the
// indirect reference rules of S 6.3.4, and returns the new value.
//
// It is shared by inc, dec, inc_chk and dec_chk. The arithmetic is signed
// (S 15, inc), so the value wraps within 16 bits exactly as add and sub do.
func (m *Machine) adjustVariable(inst *instruction, number uint8, delta int16) (int16, error) {
	value, err := m.readVariableIndirect(number)
	if err != nil {
		return 0, m.fail(inst, err)
	}
	updated := signed(value) + delta
	if err := m.writeVariableIndirect(number, unsigned(updated)); err != nil {
		return 0, m.fail(inst, err)
	}
	if m.tracing() {
		m.trace.stored = true
		m.trace.storeVariable = number
		m.trace.storeValue = unsigned(updated)
	}
	return updated, nil
}
