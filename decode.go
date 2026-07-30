package zmachine

import "fmt"

// Instruction decoding (S 4).
//
// Decoding is separate from execution: it reads the bytes an instruction
// occupies and nothing else. In particular a Variable operand decodes to the
// number of a variable, never to its contents, because reading a variable can
// pop the stack (S 4.2.2, S 6.3) and must therefore happen exactly once, when
// the instruction runs and in the order S 4.5.2 requires.

// instructionForm is the encoding an instruction uses (S 4.3).
//
// There is no extended form here. An opcode byte of $be introduces an extended
// opcode only "if the opcode is 190 ($BE in hexadecimal) and the version is 5
// or later" (S 4.3); in Version 3 that byte is an ordinary short-form opcode,
// 0OP:14, which no version before 5 defines.
type instructionForm uint8

const (
	formLong instructionForm = iota
	formShort
	formVariable
)

// String returns the form name used in S 4.3.
func (f instructionForm) String() string {
	switch f {
	case formLong:
		return "long"
	case formShort:
		return "short"
	case formVariable:
		return "variable"
	default:
		return "unknown"
	}
}

// operandType is one of the four operand types of S 4.2. The values are the
// two-bit codes the encoding uses.
type operandType uint8

const (
	operandLarge    operandType = 0 // a 2-byte constant, 0 to 65535
	operandSmall    operandType = 1 // a 1-byte constant, 0 to 255
	operandVariable operandType = 2 // a 1-byte variable number
	operandOmitted  operandType = 3 // no operand at all
)

// String returns the operand type name used in S 4.2.
func (t operandType) String() string {
	switch t {
	case operandLarge:
		return "large constant"
	case operandSmall:
		return "small constant"
	case operandVariable:
		return "variable"
	case operandOmitted:
		return "omitted"
	default:
		return "unknown"
	}
}

// operand is one decoded operand.
type operand struct {
	// kind is the operand's type. It is never operandOmitted: omitted operands
	// are not present in a decoded instruction.
	kind operandType
	// value is a constant for the constant types and a variable number for the
	// variable type - never the contents of that variable (S 4.2.2).
	value uint16
}

const (
	// maxOperandsV3 is the greatest number of operands a Version 3 instruction
	// can have. No instruction has more than 8, and only call_vs2 and call_vn2
	// have more than 4 (S 4.5.1); neither exists before Version 4, so in
	// Version 3 the limit is 4.
	maxOperandsV3 = 4

	// operandTypeFields is the number of 2-bit operand type fields in one
	// operand types byte (S 4.4.3).
	operandTypeFields = 4

	// branchBias is subtracted from a branch offset: a taken branch moves
	// execution to "address after branch data + Offset - 2" (S 4.7.2).
	branchBias = 2

	// branchOffsetBits is the width of the offset in the two-byte branch form:
	// a signed 14-bit number (S 4.7).
	branchOffsetBits = 14
)

// Bit masks used to take an instruction apart (S 4.3, S 4.4, S 4.7).
const (
	formMask       = 0xc0 // the top two bits of the opcode byte select the form
	formBitsVar    = 0xc0 // $$11: variable form
	formBitsShort  = 0x80 // $$10: short form
	shortTypeShift = 4    // bits 4 and 5 of a short-form opcode byte give the operand type
	shortTypeMask  = 0x03
	shortNumMask   = 0x0f // a short-form opcode number is 4 bits (S 4.3.1)
	opcodeNumMask  = 0x1f // long and variable form opcode numbers are 5 bits
	varCountBit    = 0x20 // in variable form, bit 5 selects VAR over 2OP (S 4.3.3)
	longType1Bit   = 0x40 // in long form, bit 6 types the first operand (S 4.4.2)
	longType2Bit   = 0x20 // and bit 5 the second
	branchOnTrue   = 0x80 // bit 7 of the first branch byte gives the polarity
	branchShort    = 0x40 // bit 6 set means a one-byte branch
	branchOffsetHi = 0x3f // the offset bits of the first branch byte
)

// branchInfo is the branch data that follows a branch instruction (S 4.7).
type branchInfo struct {
	// onTrue reports the polarity: when set the branch is taken if the
	// condition held, and when clear it is taken if the condition failed.
	onTrue bool
	// offset is the branch offset, sign-extended from 14 bits when the two-byte
	// form was used. The values 0 and 1 are not offsets at all: they mean
	// return false and return true from the current routine (S 4.7.1).
	offset int16
	// long reports whether the two-byte form was used. It is recorded so that
	// the length of an instruction can be explained in diagnostics.
	long bool
}

// returns reports whether taking this branch returns from the current routine
// rather than jumping, and the value it returns (S 4.7.1).
func (b branchInfo) returns() (bool, bool) {
	switch b.offset {
	case 0:
		return true, false
	case 1:
		return true, true
	default:
		return false, false
	}
}

// target returns the address a taken branch moves execution to. next is the
// address of the first byte after the branch data, and the destination is
// next + offset - 2 (S 4.7.2).
//
// It must not be called for a branch whose offset is 0 or 1, which return
// instead of jumping. An offset that leaves the address space is reported as an
// error rather than wrapping round.
func (b branchInfo) target(next uint32) (uint32, error) {
	return offsetAddress(next, int32(b.offset)-branchBias)
}

// instruction is one decoded Version 3 instruction.
//
// It holds everything execution needs: the opcode, the operands and their
// types, the store variable and branch data where the opcode has them, any
// inline text, the address it was decoded from and the address of the
// instruction that follows it.
type instruction struct {
	// addr is the byte address of the opcode byte: the program counter this
	// instruction was decoded from.
	addr uint32
	// next is the byte address of the first byte after the instruction, which
	// is where execution continues unless the instruction moves the program
	// counter itself. It is always greater than addr.
	next uint32

	// op identifies the instruction (S 4.3) and form is how it was encoded.
	op   opcode
	form instructionForm

	// ops holds the operands in the order they appear, which is the order they
	// must be evaluated in (S 4.5.2). Only the first numOps are meaningful.
	ops    [maxOperandsV3]operand
	numOps uint8

	// store is the variable number the result is written to, meaningful only
	// when hasStore is set (S 4.6).
	store    uint8
	hasStore bool

	// branch is the branch data, meaningful only when hasBranch is set (S 4.7).
	branch    branchInfo
	hasBranch bool

	// text is the decoded inline string, meaningful only when hasText is set.
	// Only print and print_ret carry one (S 4.8).
	text    string
	hasText bool
}

// operands returns the instruction's operands, in encoding order.
func (i *instruction) operands() []operand { return i.ops[:i.numOps] }

// length returns the size of the instruction in bytes. It is never zero: an
// instruction always occupies at least its opcode byte.
func (i *instruction) length() uint32 { return i.next - i.addr }

// String describes the instruction for diagnostics. It is not Inform assembly
// and no part of the engine parses it.
func (i *instruction) String() string {
	s := fmt.Sprintf("%04x: %s", i.addr, i.op)
	for _, op := range i.operands() {
		switch op.kind {
		case operandVariable:
			s += fmt.Sprintf(" V%02x", op.value)
		default:
			s += fmt.Sprintf(" #%04x", op.value)
		}
	}
	if i.hasStore {
		s += fmt.Sprintf(" -> V%02x", i.store)
	}
	if i.hasBranch {
		s += fmt.Sprintf(" ?%t%+d", i.branch.onTrue, i.branch.offset)
	}
	if i.hasText {
		s += fmt.Sprintf(" %q", i.text)
	}
	return s
}

// decodeInstruction decodes the instruction stored at addr (S 4).
//
// It reads only the bytes the instruction occupies, through the checked memory
// accessors, so an instruction that runs off the end of the story is reported
// as an error. It never panics and it never changes any machine state.
func decodeInstruction(m *memory, addr uint32) (instruction, error) {
	d := decoder{m: m, addr: addr, at: addr}

	opByte, err := d.byte("the opcode byte")
	if err != nil {
		return instruction{}, err
	}
	d.opByte = opByte

	inst := instruction{addr: addr}
	var types [maxOperandsV3]operandType
	var numTypes int

	switch {
	case opByte&formMask == formBitsVar:
		// S 4.3: the top two bits $$11 mean variable form. S 4.3.3: bit 5
		// chooses between 2OP and VAR, and the opcode number is the bottom five
		// bits. A 2OP opcode assembled in variable form is still a 2OP opcode -
		// that is how a 2OP instruction takes a large constant (S 4.4.2).
		inst.form = formVariable
		count := count2OP
		if opByte&varCountBit != 0 {
			count = countVAR
		}
		inst.op = makeOpcode(count, opByte&opcodeNumMask)

	case opByte&formMask == formBitsShort:
		// S 4.3.1: in short form bits 4 and 5 give an operand type. If it is
		// omitted the instruction is 0OP, and otherwise 1OP with that one
		// operand. The opcode number is the bottom four bits.
		inst.form = formShort
		kind := operandType(opByte >> shortTypeShift & shortTypeMask)
		count := count1OP
		if kind == operandOmitted {
			count = count0OP
		} else {
			types[0] = kind
			numTypes = 1
		}
		inst.op = makeOpcode(count, opByte&shortNumMask)

	default:
		// S 4.3.2: everything else is long form, which is always 2OP with the
		// opcode number in the bottom five bits. S 4.4.2: bit 6 types the first
		// operand and bit 5 the second, 0 meaning a small constant and 1 a
		// variable.
		inst.form = formLong
		types[0], types[1] = operandSmall, operandSmall
		if opByte&longType1Bit != 0 {
			types[0] = operandVariable
		}
		if opByte&longType2Bit != 0 {
			types[1] = operandVariable
		}
		numTypes = 2
		inst.op = makeOpcode(count2OP, opByte&opcodeNumMask)
	}

	// S 14.2: it is illegal for a story to contain an opcode not specified for
	// its version. Rejecting the opcode before its operands are read also means
	// that no Version 3 instruction can reach the "double variable" case of
	// S 4.4.3.1, which applies only to call_vs2 and call_vn2 - both Version 4
	// or later - and is the only case needing a second operand types byte.
	info := inst.op.info()
	if !info.definedInV3() {
		return instruction{}, d.undefined(inst.op, info)
	}

	if inst.form == formVariable {
		if numTypes, err = d.operandTypes(&types); err != nil {
			return instruction{}, err
		}
	}

	for i := 0; i < numTypes; i++ {
		if inst.ops[i], err = d.operand(types[i], i); err != nil {
			return instruction{}, err
		}
	}
	inst.numOps = uint8(numTypes)

	// S 4.6: a store instruction is followed by one byte giving the variable
	// its result goes to.
	if info.store {
		if inst.store, err = d.byte("the store variable"); err != nil {
			return instruction{}, err
		}
		inst.hasStore = true
	}

	// S 4.7: a branch instruction is followed by one or two bytes of branch
	// data.
	if info.branch {
		if inst.branch, err = d.branch(); err != nil {
			return instruction{}, err
		}
		inst.hasBranch = true
	}

	// S 4.8: print and print_ret are followed by an encoded string, and
	// execution continues after its last word, the one with the top bit set.
	if info.text {
		text, next, err := decodeStringAt(m, d.at)
		if err != nil {
			return instruction{}, d.fail(err, "the inline text is malformed: %v", err)
		}
		inst.text, inst.hasText = text, true
		d.at = next
	}

	inst.next = d.at
	return inst, nil
}

// decoder reads the bytes of one instruction in order, keeping enough context
// to describe where a read failed.
type decoder struct {
	m *memory
	// addr is the address of the instruction, which is the program counter it
	// is being decoded from.
	addr uint32
	// at is the address of the next byte to read. It advances only past bytes
	// that were read successfully, so it never leaves the story.
	at uint32
	// opByte is the first byte of the instruction, for error context. It is
	// zero until that byte has been read.
	opByte uint8
}

// byte reads the next byte. what names the field being read, for diagnostics.
func (d *decoder) byte(what string) (uint8, error) {
	v, err := d.m.readByte(d.at)
	if err != nil {
		return 0, d.truncated(err, what)
	}
	d.at++
	return v, nil
}

// word reads the next word, most significant byte first (S 2.1).
func (d *decoder) word(what string) (uint16, error) {
	v, err := d.m.readWord(d.at)
	if err != nil {
		return 0, d.truncated(err, what)
	}
	d.at += 2
	return v, nil
}

// operandTypes reads the operand types byte of a variable form instruction
// (S 4.4.3) and returns the number of operands that follow.
func (d *decoder) operandTypes(types *[maxOperandsV3]operandType) (int, error) {
	b, err := d.byte("the operand types byte")
	if err != nil {
		return 0, err
	}
	// S 4.4.3: the byte holds four 2-bit fields, bits 6 and 7 being the first
	// and bits 0 and 1 the fourth.
	field := func(i int) operandType {
		return operandType(b >> (2 * (operandTypeFields - 1 - i)) & shortTypeMask)
	}
	for i := 0; i < operandTypeFields; i++ {
		kind := field(i)
		if kind != operandOmitted {
			types[i] = kind
			continue
		}
		// S 4.4.3: "once one type has been given as 'omitted', all subsequent
		// ones must be". A later type that is not omitted leaves the length of
		// the instruction ambiguous, so it is rejected rather than guessed at.
		for j := i + 1; j < operandTypeFields; j++ {
			if field(j) != operandOmitted {
				return 0, d.fail(ErrInvalidOpcode,
					"operand types byte 0x%02x gives operand %d as omitted but operand %d as %s, which S 4.4.3 forbids",
					b, i+1, j+1, field(j))
			}
		}
		return i, nil
	}
	return operandTypeFields, nil
}

// operand reads one operand of the given type. index is its position, counted
// from zero, and is used only for diagnostics.
func (d *decoder) operand(kind operandType, index int) (operand, error) {
	what := fmt.Sprintf("operand %d", index+1)
	switch kind {
	case operandLarge:
		// S 4.2.1: large constants are stored most significant byte first.
		v, err := d.word(what)
		return operand{kind: kind, value: v}, err
	case operandSmall, operandVariable:
		// S 4.2.2: a variable operand holds a variable number, which the
		// decoder passes on untouched.
		v, err := d.byte(what)
		return operand{kind: kind, value: uint16(v)}, err
	default:
		// Omitted operands never reach here: they end the operand list.
		return operand{}, d.fail(ErrInvalidOpcode, "%s has no type", what)
	}
}

// branch reads the branch data that follows a branch instruction (S 4.7).
func (d *decoder) branch() (branchInfo, error) {
	first, err := d.byte("the first branch byte")
	if err != nil {
		return branchInfo{}, err
	}
	// S 4.7: if bit 7 is 0 the branch happens when the condition was false, and
	// if 1 when it was true.
	b := branchInfo{onTrue: first&branchOnTrue != 0}

	if first&branchShort != 0 {
		// S 4.7: bit 6 set means the branch occupies one byte and the offset is
		// the unsigned value in the bottom six bits, so a one-byte branch can
		// only go forwards.
		b.offset = int16(first & branchOffsetHi)
		return b, nil
	}

	second, err := d.byte("the second branch byte")
	if err != nil {
		return branchInfo{}, err
	}
	// S 4.7: bit 6 clear means the offset is a signed 14-bit number held in
	// bits 0 to 5 of the first byte followed by all 8 of the second. Its sign
	// bit is bit 13, so the value must be sign-extended from 14 bits and not
	// simply read as a positive number.
	b.long = true
	b.offset = signExtend(uint16(first&branchOffsetHi)<<8|uint16(second), branchOffsetBits)
	return b, nil
}

// undefined reports an opcode that a Version 3 story may not contain (S 14.2).
func (d *decoder) undefined(op opcode, info opcodeInfo) *DecodeError {
	if info.since == 0 {
		return d.fail(ErrInvalidOpcode, "%s is not an instruction in any version of the Z-machine", op)
	}
	return d.fail(ErrInvalidOpcode, "%s is defined only from Version %d", op, info.since)
}

// truncated reports an instruction that runs off the end of the story. cause is
// the refused memory access, which classifies the error as ErrMemoryAccess.
func (d *decoder) truncated(cause error, what string) *DecodeError {
	return d.fail(cause, "%s at 0x%04x is beyond the end of the story (0x%04x)", what, d.at, d.m.size())
}

// fail builds a DecodeError for the instruction being decoded.
func (d *decoder) fail(cause error, format string, args ...any) *DecodeError {
	return &DecodeError{
		Addr:   d.addr,
		Opcode: d.opByte,
		Detail: fmt.Sprintf(format, args...),
		Err:    cause,
	}
}
