package zmachine

// Two-operand instructions (S 14, 2OP).
//
// Arithmetic here is 16-bit. Which reading a word takes is a property of the
// instruction and not of the word (S 2.2.1): comparison, addition,
// subtraction, multiplication, division and remainder are signed, while
// bitwise operations are unsigned. Every change of reading goes through the
// helpers in value.go so that no conversion silently picks up the host's
// integer width.

// execute2OP carries out an instruction with two operands, or in the case of
// je between two and four.
func (m *Machine) execute2OP(inst *instruction, ops []uint16) (control, error) {
	// S 15, je: je takes two to four operands, and is the only 2OP opcode
	// whose operand count varies. It is checked before the common case because
	// the check itself differs.
	if inst.op == opJE {
		// "je with just 1 operand is not permitted" (S 15, je).
		if err := m.operands(inst, 2, maxOperandsV3); err != nil {
			return controlContinue, err
		}
		// S 15, je: jump if a is equal to any of the subsequent operands. The
		// comparison is of whole words, so signedness does not enter into it.
		equal := false
		for _, other := range ops[1:] {
			if ops[0] == other {
				equal = true
				break
			}
		}
		return m.branch(inst, equal)
	}

	if err := m.operands(inst, 2, 2); err != nil {
		return controlContinue, err
	}
	a, b := ops[0], ops[1]

	switch inst.op {
	case opJL:
		// S 15, jl: jump if a < b, using a signed 16-bit comparison. S 2.2.1
		// warns that this makes jl and jg unsafe for comparing addresses.
		return m.branch(inst, signed(a) < signed(b))

	case opJG:
		// S 15, jg: jump if a > b, signed.
		return m.branch(inst, signed(a) > signed(b))

	case opDecChk:
		// S 15, dec_chk: decrement the variable a names, then branch if it is
		// now less than b. The decrement and the comparison are both signed.
		value, err := m.adjustVariable(inst, uint8(a), -1)
		if err != nil {
			return controlContinue, err
		}
		return m.branch(inst, value < signed(b))

	case opIncChk:
		// S 15, inc_chk: increment the variable a names, then branch if it is
		// now greater than b.
		value, err := m.adjustVariable(inst, uint8(a), 1)
		if err != nil {
			return controlContinue, err
		}
		return m.branch(inst, value > signed(b))

	case opTest:
		// S 15, test: jump if all of the flags in the bitmap are set, that is
		// if bitmap & flags == flags. This is a bitwise operation and so
		// unsigned (S 2.2.1).
		return m.branch(inst, a&b == b)

	case opOr:
		// S 15, or: bitwise OR.
		return controlContinue, m.store(inst, a|b)

	case opAnd:
		// S 15, and: bitwise AND.
		return controlContinue, m.store(inst, a&b)

	case opStore:
		// S 15, store: set the variable a names to b. The reference is
		// indirect, so storing to the stack pointer overwrites the top item
		// rather than pushing a new one (S 6.3.4).
		if err := m.writeVariableIndirect(uint8(a), b); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if m.tracing() {
			m.trace.stored = true
			m.trace.storeVariable = uint8(a)
			m.trace.storeValue = b
		}
		return controlContinue, nil

	case opLoadW:
		// S 15, loadw: store the word at address a + 2*b, which must lie in
		// static or dynamic memory.
		addr, err := m.arrayAddress(inst, a, b, wordAddressScale)
		if err != nil {
			return controlContinue, err
		}
		value, err := m.mem.readWord(addr)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, m.store(inst, value)

	case opLoadB:
		// S 15, loadb: store the byte at address a + b.
		addr, err := m.arrayAddress(inst, a, b, 1)
		if err != nil {
			return controlContinue, err
		}
		value, err := m.mem.readByte(addr)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, m.store(inst, uint16(value))

	case opAdd:
		// S 15, add: signed 16-bit addition. Overflow reduces modulo $10000,
		// which is what S 2.3.2 suggests and what 16-bit two's complement
		// arithmetic does anyway.
		return controlContinue, m.store(inst, unsigned(signed(a)+signed(b)))

	case opSub:
		// S 15, sub: signed 16-bit subtraction.
		return controlContinue, m.store(inst, unsigned(signed(a)-signed(b)))

	case opMul:
		// S 15, mul: signed 16-bit multiplication.
		return controlContinue, m.store(inst, unsigned(signed(a)*signed(b)))

	case opDiv:
		// S 15, div: signed 16-bit division, truncating towards zero, so that
		// -11 / 2 = -5 and 11 / -2 = -5 (S 2.2.1 remarks).
		if signed(b) == 0 {
			// S 2.3.1: it is illegal to divide by zero and the interpreter
			// should halt with an error message.
			return controlContinue, m.fault(inst, nil, "division of %d by zero, which S 2.3.1 forbids", signed(a))
		}
		return controlContinue, m.store(inst, unsigned(signed(a)/signed(b)))

	case opMod:
		// S 15, mod: remainder after signed 16-bit division. The remainder
		// takes the sign of the dividend, so that -13 % 5 = -3 and
		// 13 % -5 = 3 (S 2.2.1 remarks).
		if signed(b) == 0 {
			return controlContinue, m.fault(inst, nil, "remainder of %d after division by zero, which S 2.3.1 forbids", signed(a))
		}
		return controlContinue, m.store(inst, unsigned(signed(a)%signed(b)))

	case opJin:
		// S 15, jin: jump if object a is a direct child of b, that is if the
		// parent of a is b.
		if m.nothingObject(inst, a) {
			// "Nothing" is contained in nothing, so the branch is taken only
			// when the story asked whether it is in nothing.
			return m.branch(inst, b == objectNothing)
		}
		parent, err := m.mem.objectParent(a)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return m.branch(inst, parent == b)

	case opTestAttr:
		// S 15, test_attr: jump if the object has the attribute.
		if m.nothingObject(inst, a) {
			// "Nothing" has no attributes, so the branch is not taken.
			return m.branch(inst, false)
		}
		set, err := m.mem.objectAttribute(a, b)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return m.branch(inst, set)

	case opSetAttr:
		// S 15, set_attr: make the object have the attribute.
		if m.nothingObject(inst, a) {
			return controlContinue, nil
		}
		if err := m.mem.setObjectAttribute(a, b, true); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opClearAttr:
		// S 15, clear_attr: make the object not have the attribute.
		if m.nothingObject(inst, a) {
			return controlContinue, nil
		}
		if err := m.mem.setObjectAttribute(a, b, false); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opInsertObj:
		// S 15, insert_obj: move object a to become the first child of object
		// b, taking its own children with it.
		if m.nothingObject(inst, a) || m.nothingObject(inst, b) {
			// Moving nothing, or moving something into nothing, changes no
			// object in the tree.
			return controlContinue, nil
		}
		if err := m.mem.insertObject(a, b); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opGetProp:
		// S 15, get_prop: read a property from an object, giving the default
		// value if the object does not provide it (S 12.2).
		if m.nothingObject(inst, a) {
			// "Nothing" has no property table, so not even the default of
			// S 12.2 applies and the result is 0.
			return controlContinue, m.store(inst, 0)
		}
		value, err := m.mem.propertyValue(a, b)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, m.store(inst, value)

	case opGetPropAddr:
		// S 15, get_prop_addr: the byte address of the property data, or 0 if
		// the object has not got the property.
		if m.nothingObject(inst, a) {
			return controlContinue, m.store(inst, 0)
		}
		addr, err := m.mem.propertyAddress(a, b)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, m.store(inst, addr)

	case opGetNextProp:
		// S 15, get_next_prop: the number of the next property the object
		// provides, or the first one when asked for property 0, or 0 at the end
		// of the list.
		if m.nothingObject(inst, a) {
			return controlContinue, m.store(inst, 0)
		}
		next, err := m.mem.nextPropertyNumber(a, b)
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, m.store(inst, next)

	default:
		return controlContinue, m.fault(inst, ErrInvalidOpcode, "not dispatched")
	}
}

// arrayAddress computes the address of element index of an array based at
// base, whose elements are scale bytes wide (S 15, loadw, loadb, storew,
// storeb).
//
// The index is taken as unsigned and the sum computed in full rather than
// being reduced to 16 bits. The Standard does not say which reading applies;
// computing in full means an index that would have wrapped round the 16-bit
// address space reaches the caller as an out-of-range address, rather than
// silently naming a different part of memory than the story asked for.
func (m *Machine) arrayAddress(inst *instruction, base, index uint16, scale uint32) (uint32, error) {
	addr := uint32(base) + uint32(index)*scale
	if addr >= addressSpaceLimit {
		return 0, m.fault(inst, ErrMemoryAccess,
			"array at 0x%04x element %d leaves the address space at 0x%x", base, index, addr)
	}
	return addr, nil
}
