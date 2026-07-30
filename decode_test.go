package zmachine

import (
	"errors"
	"os"
	"testing"
)

// Layout of the story built by newDecodeFixture. It is the story from
// validTestHeader, whose high memory, from 0x0360 to the end of the file, is
// unused; hand-built instructions are written there.
const (
	decodeCodeBase = 0x0360
	decodeStoryEnd = 0x0400
)

// newDecodeFixture builds a story whose free high memory holds code, starting
// at decodeCodeBase.
func newDecodeFixture(t *testing.T, code ...byte) *memory {
	t.Helper()
	return newDecodeFixtureAt(t, decodeCodeBase, code...)
}

// newDecodeFixtureAt writes code at addr, which lets a test place an
// instruction so that it runs off the end of the story.
func newDecodeFixtureAt(t *testing.T, addr uint32, code ...byte) *memory {
	t.Helper()
	data := validTestHeader().build()
	if int(addr)+len(code) > len(data) {
		t.Fatalf("%d byte(s) of code at 0x%04x do not fit in the fixture", len(code), addr)
	}
	copy(data[addr:], code)
	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	return newMemory(story)
}

// decodeCode decodes the instruction at the start of code.
func decodeCode(t *testing.T, code ...byte) instruction {
	t.Helper()
	inst, err := decodeInstruction(newDecodeFixture(t, code...), decodeCodeBase)
	if err != nil {
		t.Fatalf("decodeInstruction() error = %v, want nil", err)
	}
	return inst
}

// assertOperands compares an instruction's operands with what was expected.
func assertOperands(t *testing.T, inst instruction, want []operand) {
	t.Helper()
	got := inst.operands()
	if len(got) != len(want) {
		t.Fatalf("%v decoded %d operand(s), want %d", inst.op, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%v operand %d = %s 0x%04x, want %s 0x%04x",
				inst.op, i+1, got[i].kind, got[i].value, want[i].kind, want[i].value)
		}
	}
}

// assertDecodeError checks that err is a DecodeError naming the given
// instruction address and opcode byte, and classified as target.
func assertDecodeError(t *testing.T, err error, addr uint32, opByte uint8, target error) {
	t.Helper()
	if err == nil {
		t.Fatalf("decodeInstruction() error = nil, want an error")
	}
	var de *DecodeError
	if !errors.As(err, &de) {
		t.Fatalf("decodeInstruction() error = %v (%T), want a *DecodeError", err, err)
	}
	if de.Addr != addr {
		t.Errorf("DecodeError.Addr = 0x%04x, want 0x%04x", de.Addr, addr)
	}
	if de.Opcode != opByte {
		t.Errorf("DecodeError.Opcode = 0x%02x, want 0x%02x", de.Opcode, opByte)
	}
	if !errors.Is(err, target) {
		t.Errorf("decodeInstruction() error = %v, want one classified as %v", err, target)
	}
}

// TestDecodeForms covers the three instruction forms a Version 3 story can use
// and the operand types each of them can express (S 4.3, S 4.4).
//
// The disassembly table at the end of S 4 fixes what the first byte means:
//
//	$00 -- $1f  long      2OP     small constant, small constant
//	$20 -- $3f  long      2OP     small constant, variable
//	$40 -- $5f  long      2OP     variable, small constant
//	$60 -- $7f  long      2OP     variable, variable
//	$80 -- $8f  short     1OP     large constant
//	$90 -- $9f  short     1OP     small constant
//	$a0 -- $af  short     1OP     variable
//	$b0 -- $bf  short     0OP
//	$c0 -- $df  variable  2OP
//	$e0 -- $ff  variable  VAR
func TestDecodeForms(t *testing.T) {
	tests := []struct {
		name string
		code []byte
		op   opcode
		form instructionForm
		ops  []operand
		next uint32
	}{
		{
			name: "long form, small constant and small constant",
			code: []byte{0x14, 0x11, 0x22, 0x05}, // add 0x11 0x22 -> V05
			op:   opAdd, form: formLong,
			ops:  []operand{{operandSmall, 0x11}, {operandSmall, 0x22}},
			next: decodeCodeBase + 4,
		},
		{
			name: "long form, small constant and variable",
			code: []byte{0x34, 0x11, 0x22, 0x05}, // bit 5 set: second operand is a variable
			op:   opAdd, form: formLong,
			ops:  []operand{{operandSmall, 0x11}, {operandVariable, 0x22}},
			next: decodeCodeBase + 4,
		},
		{
			name: "long form, variable and small constant",
			code: []byte{0x54, 0x11, 0x22, 0x05}, // bit 6 set: first operand is a variable
			op:   opAdd, form: formLong,
			ops:  []operand{{operandVariable, 0x11}, {operandSmall, 0x22}},
			next: decodeCodeBase + 4,
		},
		{
			name: "long form, variable and variable",
			code: []byte{0x74, 0x11, 0x22, 0x05},
			op:   opAdd, form: formLong,
			ops:  []operand{{operandVariable, 0x11}, {operandVariable, 0x22}},
			next: decodeCodeBase + 4,
		},
		{
			// S 4.3.2: the long-form opcode number is five bits, so it reaches
			// past the sixteen numbers the short form can express.
			name: "long form opcode number uses five bits",
			code: []byte{0x18, 0x01, 0x02, 0x00}, // mod
			op:   opMod, form: formLong,
			ops:  []operand{{operandSmall, 0x01}, {operandSmall, 0x02}},
			next: decodeCodeBase + 4,
		},
		{
			name: "short form 1OP, large constant",
			code: []byte{0x8d, 0x12, 0x34}, // print_paddr 0x1234
			op:   opPrintPaddr, form: formShort,
			ops:  []operand{{operandLarge, 0x1234}},
			next: decodeCodeBase + 3,
		},
		{
			name: "short form 1OP, small constant",
			code: []byte{0x9b, 0x07}, // ret 7
			op:   opRet, form: formShort,
			ops:  []operand{{operandSmall, 0x07}},
			next: decodeCodeBase + 2,
		},
		{
			name: "short form 1OP, variable",
			code: []byte{0xab, 0x07}, // ret V07
			op:   opRet, form: formShort,
			ops:  []operand{{operandVariable, 0x07}},
			next: decodeCodeBase + 2,
		},
		{
			// S 4.3.1: an operand type of $$11 in bits 4 and 5 makes the
			// instruction 0OP, so the whole instruction is one byte.
			name: "short form 0OP",
			code: []byte{0xbb}, // new_line
			op:   opNewLine, form: formShort,
			next: decodeCodeBase + 1,
		},
		{
			// S 4.3.3: bit 5 clear in variable form still means 2OP. This is
			// how a 2OP instruction takes a large constant (S 4.4.2).
			name: "variable form, 2OP with a large constant",
			code: []byte{0xd6, 0x2f, 0x03, 0xe8, 0x02, 0x00}, // mul 1000 V02 -> sp
			op:   opMul, form: formVariable,
			ops:  []operand{{operandLarge, 1000}, {operandVariable, 0x02}},
			next: decodeCodeBase + 6,
		},
		{
			name: "variable form, VAR",
			code: []byte{0xe1, 0x97, 0x00, 0x00, 0x01}, // storew sp 0 1
			op:   opStoreW, form: formVariable,
			ops:  []operand{{operandVariable, 0x00}, {operandSmall, 0x00}, {operandSmall, 0x01}},
			next: decodeCodeBase + 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := decodeCode(t, tt.code...)
			if inst.op != tt.op {
				t.Errorf("decoded %v, want %v", inst.op, tt.op)
			}
			if inst.form != tt.form {
				t.Errorf("form = %v, want %v", inst.form, tt.form)
			}
			assertOperands(t, inst, tt.ops)
			if inst.addr != decodeCodeBase {
				t.Errorf("addr = 0x%04x, want 0x%04x", inst.addr, uint32(decodeCodeBase))
			}
			if inst.next != tt.next {
				t.Errorf("next = 0x%04x, want 0x%04x", inst.next, tt.next)
			}
			if got, want := inst.length(), tt.next-decodeCodeBase; got != want {
				t.Errorf("length() = %d, want %d", got, want)
			}
		})
	}
}

// TestDecodeOperandTypesByte covers every combination of operand types a
// variable-form instruction can express (S 4.4.3). The byte holds four 2-bit
// fields, bits 6 and 7 being the first and bits 0 and 1 the fourth, and a field
// of $$11 means the operand is omitted.
//
// The instruction used is storew, which neither stores nor branches, so the
// bytes after the operands belong to no field. The decoder does not check how
// many operands an opcode should have: je legally takes two to four operands
// (S 15, je), so arity is left to execution.
func TestDecodeOperandTypesByte(t *testing.T) {
	tests := []struct {
		name  string
		types byte
		body  []byte
		ops   []operand
	}{
		{
			name: "no operands", types: 0xff,
		},
		{
			name: "one large constant", types: 0x3f,
			body: []byte{0x12, 0x34},
			ops:  []operand{{operandLarge, 0x1234}},
		},
		{
			name: "one small constant", types: 0x7f,
			body: []byte{0x12},
			ops:  []operand{{operandSmall, 0x12}},
		},
		{
			name: "one variable", types: 0xbf,
			body: []byte{0x12},
			ops:  []operand{{operandVariable, 0x12}},
		},
		{
			// S 4.4.3's own example: $$00101111 is a large constant followed by
			// a variable.
			name: "large constant then variable", types: 0x2f,
			body: []byte{0x12, 0x34, 0x56},
			ops:  []operand{{operandLarge, 0x1234}, {operandVariable, 0x56}},
		},
		{
			name: "three operands, one of each type", types: 0x1b,
			body: []byte{0x12, 0x34, 0x56, 0x78},
			ops:  []operand{{operandLarge, 0x1234}, {operandSmall, 0x56}, {operandVariable, 0x78}},
		},
		{
			name: "four large constants", types: 0x00,
			body: []byte{0x00, 0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04},
			ops: []operand{
				{operandLarge, 1}, {operandLarge, 2}, {operandLarge, 3}, {operandLarge, 4},
			},
		},
		{
			name: "four small constants", types: 0x55,
			body: []byte{0x01, 0x02, 0x03, 0x04},
			ops: []operand{
				{operandSmall, 1}, {operandSmall, 2}, {operandSmall, 3}, {operandSmall, 4},
			},
		},
		{
			name: "four variables", types: 0xaa,
			body: []byte{0x01, 0x02, 0x03, 0x04},
			ops: []operand{
				{operandVariable, 1}, {operandVariable, 2}, {operandVariable, 3}, {operandVariable, 4},
			},
		},
		{
			name: "mixed types in every field", types: 0x1a,
			body: []byte{0x12, 0x34, 0x56, 0x78, 0x9a},
			ops: []operand{
				{operandLarge, 0x1234}, {operandSmall, 0x56},
				{operandVariable, 0x78}, {operandVariable, 0x9a},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := append([]byte{0xe1, tt.types}, tt.body...)
			inst := decodeCode(t, code...)
			if inst.op != opStoreW {
				t.Fatalf("decoded %v, want %v", inst.op, opStoreW)
			}
			assertOperands(t, inst, tt.ops)
			if want := uint32(2 + len(tt.body)); inst.length() != want {
				t.Errorf("length() = %d, want %d", inst.length(), want)
			}
		})
	}
}

// TestDecodeStoreBranchAndText covers the optional fields of S 4.1, which
// follow the operands in the order store, branch, text.
func TestDecodeStoreBranchAndText(t *testing.T) {
	t.Run("store only", func(t *testing.T) {
		// S 4.6: loadw is followed by one byte naming the variable its result
		// goes to.
		inst := decodeCode(t, 0x2f, 0x10, 0x02, 0x8f) // loadw 0x10 V02 -> V8f
		if inst.op != opLoadW {
			t.Fatalf("decoded %v, want %v", inst.op, opLoadW)
		}
		if !inst.hasStore || inst.store != 0x8f {
			t.Errorf("store = 0x%02x (present %t), want 0x8f present", inst.store, inst.hasStore)
		}
		if inst.hasBranch || inst.hasText {
			t.Errorf("loadw has branch %t and text %t, want neither", inst.hasBranch, inst.hasText)
		}
	})

	t.Run("branch only", func(t *testing.T) {
		// The example from S 4: @inc_chk c 0 label assembles as 05 02 00 d4.
		inst := decodeCode(t, 0x05, 0x02, 0x00, 0xd4)
		if inst.op != opIncChk {
			t.Fatalf("decoded %v, want %v", inst.op, opIncChk)
		}
		assertOperands(t, inst, []operand{{operandSmall, 0x02}, {operandSmall, 0x00}})
		if inst.hasStore {
			t.Errorf("inc_chk decoded a store variable")
		}
		if !inst.hasBranch || !inst.branch.onTrue || inst.branch.offset != 20 {
			t.Errorf("branch = %+v, want on true with offset 20", inst.branch)
		}
		if inst.length() != 4 {
			t.Errorf("length() = %d, want 4", inst.length())
		}
	})

	t.Run("store and branch", func(t *testing.T) {
		// S 14: get_sibling is one of the few opcodes taking both. The store
		// byte comes first (S 4.1).
		inst := decodeCode(t, 0xa1, 0x0c, 0x03, 0xc5) // get_sibling V0c -> V03 ?+5
		if inst.op != opGetSibling {
			t.Fatalf("decoded %v, want %v", inst.op, opGetSibling)
		}
		if !inst.hasStore || inst.store != 0x03 {
			t.Errorf("store = 0x%02x (present %t), want 0x03 present", inst.store, inst.hasStore)
		}
		if !inst.hasBranch || !inst.branch.onTrue || inst.branch.offset != 5 {
			t.Errorf("branch = %+v, want on true with offset 5", inst.branch)
		}
		if inst.length() != 4 {
			t.Errorf("length() = %d, want 4", inst.length())
		}
	})

	t.Run("print carries text", func(t *testing.T) {
		// The example from S 4: @print "Hello.^" assembles as
		// b2 11 aa 46 34 16 45 9c a5.
		inst := decodeCode(t, 0xb2, 0x11, 0xaa, 0x46, 0x34, 0x16, 0x45, 0x9c, 0xa5)
		if inst.op != opPrint {
			t.Fatalf("decoded %v, want %v", inst.op, opPrint)
		}
		if !inst.hasText || inst.text != "Hello.\n" {
			t.Errorf("text = %q (present %t), want %q", inst.text, inst.hasText, "Hello.\n")
		}
		// S 4.8: execution continues after the last 2-byte word of text, the
		// one with the top bit set. The words here are $11aa, $4634, $1645 and
		// $9ca5; only the last has bit 15 set, so the instruction is the
		// opcode byte plus four words.
		if inst.length() != 9 {
			t.Errorf("length() = %d, want 9", inst.length())
		}
		if inst.hasStore || inst.hasBranch {
			t.Errorf("print decoded a store or branch")
		}
	})

	t.Run("print_ret carries text", func(t *testing.T) {
		f := newTextFixture(t)
		// "ab" in alphabet A0: Z-characters 6 and 7 (S 3.5.3).
		end := f.putStringAt(decodeCodeBase+1, 6, 7)
		f.data[decodeCodeBase] = 0xb3
		inst, err := decodeInstruction(f.memory(), decodeCodeBase)
		if err != nil {
			t.Fatalf("decodeInstruction() error = %v, want nil", err)
		}
		if inst.op != opPrintRet {
			t.Fatalf("decoded %v, want %v", inst.op, opPrintRet)
		}
		if inst.text != "ab" {
			t.Errorf("text = %q, want %q", inst.text, "ab")
		}
		if inst.next != end {
			t.Errorf("next = 0x%04x, want 0x%04x", inst.next, end)
		}
	})
}

// TestDecodeBranchOffsets covers the branch encoding of S 4.7. Bit 7 of the
// first byte is the polarity, bit 6 selects the one-byte form, and in the
// two-byte form the offset is a *signed* 14-bit number: its sign bit is bit 13,
// not bit 15, so it must be sign-extended and not read as a positive value.
func TestDecodeBranchOffsets(t *testing.T) {
	tests := []struct {
		name     string
		branch   []byte
		onTrue   bool
		long     bool
		offset   int16
		returns  bool
		retValue bool
	}{
		{
			name: "one byte, branch on true", branch: []byte{0xd4},
			onTrue: true, offset: 20,
		},
		{
			name: "one byte, branch on false", branch: []byte{0x54},
			onTrue: false, offset: 20,
		},
		{
			name: "one byte, largest offset", branch: []byte{0xff},
			onTrue: true, offset: 63,
		},
		{
			// S 4.7.1: offset 0 means return false from the current routine,
			// whatever the polarity or the form.
			name: "one byte, offset 0 returns false", branch: []byte{0xc0},
			onTrue: true, offset: 0, returns: true, retValue: false,
		},
		{
			// S 4.7.1: offset 1 means return true.
			name: "one byte, offset 1 returns true", branch: []byte{0xc1},
			onTrue: true, offset: 1, returns: true, retValue: true,
		},
		{
			name: "one byte on false, offset 0 returns false", branch: []byte{0x40},
			onTrue: false, offset: 0, returns: true, retValue: false,
		},
		{
			name: "two bytes, offset 0 returns false", branch: []byte{0x80, 0x00},
			onTrue: true, long: true, offset: 0, returns: true, retValue: false,
		},
		{
			name: "two bytes, offset 1 returns true", branch: []byte{0x80, 0x01},
			onTrue: true, long: true, offset: 1, returns: true, retValue: true,
		},
		{
			name: "two bytes, small positive offset", branch: []byte{0x80, 0x20},
			onTrue: true, long: true, offset: 32,
		},
		{
			name: "two bytes, positive offset using the high bits", branch: []byte{0x81, 0x00},
			onTrue: true, long: true, offset: 256,
		},
		{
			name: "two bytes, largest positive offset", branch: []byte{0x9f, 0xff},
			onTrue: true, long: true, offset: 8191,
		},
		{
			// Bit 13 set: the offset is negative. Read as an unsigned 14-bit
			// number this would be 8192.
			name: "two bytes, most negative offset", branch: []byte{0xa0, 0x00},
			onTrue: true, long: true, offset: -8192,
		},
		{
			name: "two bytes, offset -1", branch: []byte{0xbf, 0xff},
			onTrue: true, long: true, offset: -1,
		},
		{
			name: "two bytes, offset -2", branch: []byte{0xbf, 0xfe},
			onTrue: true, long: true, offset: -2,
		},
		{
			name: "two bytes, moderate negative offset", branch: []byte{0xbf, 0x00},
			onTrue: true, long: true, offset: -256,
		},
		{
			name: "two bytes on false, negative offset", branch: []byte{0x3f, 0xff},
			onTrue: false, long: true, offset: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// jz V01 ?branch: a 1OP branch instruction with no store byte.
			code := append([]byte{0xa0, 0x01}, tt.branch...)
			inst := decodeCode(t, code...)
			if inst.op != opJZ {
				t.Fatalf("decoded %v, want %v", inst.op, opJZ)
			}
			if !inst.hasBranch {
				t.Fatalf("jz decoded without branch data")
			}
			if inst.branch.onTrue != tt.onTrue {
				t.Errorf("onTrue = %t, want %t", inst.branch.onTrue, tt.onTrue)
			}
			if inst.branch.long != tt.long {
				t.Errorf("long = %t, want %t", inst.branch.long, tt.long)
			}
			if inst.branch.offset != tt.offset {
				t.Errorf("offset = %d, want %d", inst.branch.offset, tt.offset)
			}
			if want := uint32(2 + len(tt.branch)); inst.length() != want {
				t.Errorf("length() = %d, want %d", inst.length(), want)
			}
			returns, value := inst.branch.returns()
			if returns != tt.returns || (returns && value != tt.retValue) {
				t.Errorf("returns() = %t, %t; want %t, %t", returns, value, tt.returns, tt.retValue)
			}
		})
	}
}

// TestBranchTarget checks the destination formula of S 4.7.2: a taken branch
// moves execution to "address after branch data + Offset - 2".
func TestBranchTarget(t *testing.T) {
	tests := []struct {
		name   string
		next   uint32
		offset int16
		want   uint32
	}{
		{name: "offset 2 continues with the next instruction", next: 0x1000, offset: 2, want: 0x1000},
		{name: "forwards", next: 0x1000, offset: 20, want: 0x1012},
		{name: "backwards", next: 0x1000, offset: -1, want: 0x0ffd},
		{name: "far backwards", next: 0x3000, offset: -8192, want: 0x0ffe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := branchInfo{offset: tt.offset}.target(tt.next)
			if err != nil {
				t.Fatalf("target() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("target(0x%04x) with offset %d = 0x%04x, want 0x%04x", tt.next, tt.offset, got, tt.want)
			}
		})
	}

	// A branch whose destination is outside the address space is an error, not
	// an address that has wrapped round.
	if _, err := (branchInfo{offset: -8192}).target(0x10); !errors.Is(err, ErrMemoryAccess) {
		t.Errorf("target() error = %v, want one classified as %v", err, ErrMemoryAccess)
	}
}

// TestDecodeMalformed covers instructions that cannot be decoded: opcodes that
// Version 3 does not define (S 14.2) and instructions truncated by the end of
// the story. Every case must produce an error naming the program counter and
// the opcode byte, and none may panic.
func TestDecodeMalformed(t *testing.T) {
	// Truncated instructions are written so that they end exactly at the end of
	// the story, so the next byte the decoder needs does not exist.
	truncated := func(code ...byte) (*memory, uint32) {
		addr := uint32(decodeStoryEnd - len(code))
		return newDecodeFixtureAt(t, addr, code...), addr
	}

	tests := []struct {
		name   string
		build  func() (*memory, uint32)
		opByte uint8
		target error
	}{
		{
			// S 14: 2OP opcode number 0 is marked "------"; it is not an
			// instruction in any version.
			name:   "opcode number defined in no version",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0x00, 0x01, 0x02), decodeCodeBase },
			opByte: 0x00, target: ErrInvalidOpcode,
		},
		{
			// S 4.3: $be is the first byte of an extended opcode only from
			// Version 5. In Version 3 it is the short-form 0OP:14, which no
			// version before 5 defines.
			name:   "the extended opcode byte $be",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0xbe, 0x00, 0x00), decodeCodeBase },
			opByte: 0xbe, target: ErrInvalidOpcode,
		},
		{
			name:   "a Version 4 opcode, call_1s",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0x98, 0x12), decodeCodeBase },
			opByte: 0x98, target: ErrInvalidOpcode,
		},
		{
			name:   "a Version 4 opcode, call_vs2",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0xec, 0x7f, 0x01, 0x00), decodeCodeBase },
			opByte: 0xec, target: ErrInvalidOpcode,
		},
		{
			name:   "a Version 5 opcode, throw",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0x1c, 0x01, 0x02), decodeCodeBase },
			opByte: 0x1c, target: ErrInvalidOpcode,
		},
		{
			// S 4.4.3: once one operand type is omitted, all the later ones
			// must be. A types byte that breaks the rule leaves the length of
			// the instruction ambiguous.
			name:   "operand types resume after an omitted one",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0xe1, 0xf0, 0x12, 0x34), decodeCodeBase },
			opByte: 0xe1, target: ErrInvalidOpcode,
		},
		{
			name: "the opcode byte is past the end of the story",
			build: func() (*memory, uint32) {
				return newDecodeFixture(t), decodeStoryEnd
			},
			// The failure happened before any opcode byte was read.
			opByte: 0x00, target: ErrMemoryAccess,
		},
		{
			name:   "a small constant operand is truncated",
			build:  func() (*memory, uint32) { return truncated(0x0f, 0x01) },
			opByte: 0x0f, target: ErrMemoryAccess,
		},
		{
			name:   "a large constant operand is truncated",
			build:  func() (*memory, uint32) { return truncated(0xe0, 0x3f, 0x12) },
			opByte: 0xe0, target: ErrMemoryAccess,
		},
		{
			name:   "the operand types byte is truncated",
			build:  func() (*memory, uint32) { return truncated(0xe1) },
			opByte: 0xe1, target: ErrMemoryAccess,
		},
		{
			name:   "the store variable is truncated",
			build:  func() (*memory, uint32) { return truncated(0x0f, 0x01, 0x02) },
			opByte: 0x0f, target: ErrMemoryAccess,
		},
		{
			name:   "the first branch byte is truncated",
			build:  func() (*memory, uint32) { return truncated(0x01, 0x01, 0x02) },
			opByte: 0x01, target: ErrMemoryAccess,
		},
		{
			// The first branch byte has bit 6 clear, so a second one is
			// required (S 4.7).
			name:   "the second branch byte is truncated",
			build:  func() (*memory, uint32) { return truncated(0x01, 0x01, 0x02, 0x00) },
			opByte: 0x01, target: ErrMemoryAccess,
		},
		{
			// The bytes after the opcode are zero, so no word has the
			// end-of-string bit set before the end of the story (S 3.2).
			name:   "the inline text is unterminated",
			build:  func() (*memory, uint32) { return newDecodeFixture(t, 0xb2), decodeCodeBase },
			opByte: 0xb2, target: ErrInvalidText,
		},
		{
			name:   "the inline text starts past the end of the story",
			build:  func() (*memory, uint32) { return truncated(0xb2) },
			opByte: 0xb2, target: ErrMemoryAccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, addr := tt.build()
			inst, err := decodeInstruction(m, addr)
			if err == nil {
				t.Fatalf("decodeInstruction(0x%04x) = %v, want an error", addr, inst.op)
			}
			assertDecodeError(t, err, addr, tt.opByte, tt.target)
		})
	}
}

// TestDecodeDoesNotReadVariables checks that decoding is free of execution: an
// operand of type Variable decodes to the variable's number, and the contents
// of that variable never enter the decoded instruction (S 4.2.2). Decoding the
// same bytes twice with different memory must therefore give the same result.
func TestDecodeDoesNotReadVariables(t *testing.T) {
	code := []byte{0x74, 0x10, 0x11, 0x00} // add V10 V11 -> sp
	m := newDecodeFixture(t, code...)

	first, err := decodeInstruction(m, decodeCodeBase)
	if err != nil {
		t.Fatalf("decodeInstruction() error = %v, want nil", err)
	}

	// Global variable 0 lives at the start of the global variables table
	// (S 6.2), which is variable number 0x10.
	if err := m.writeWord(m.story.globals, 0xbeef); err != nil {
		t.Fatalf("writeWord() error = %v, want nil", err)
	}
	second, err := decodeInstruction(m, decodeCodeBase)
	if err != nil {
		t.Fatalf("decodeInstruction() error = %v, want nil", err)
	}
	if first != second {
		t.Errorf("decoding changed after a variable was written:\n %v\n %v", &first, &second)
	}
	assertOperands(t, first, []operand{{operandVariable, 0x10}, {operandVariable, 0x11}})
}

// TestDecodeInstructionsZork1 decodes the first instructions of a real story.
//
// The expected values were derived by hand from the bytes at Zork I's initial
// program counter, $50d5, using the disassembly table at the end of S 4, and
// then checked three ways:
//
//   - Every instruction's next address is the address of the following
//     instruction, so a single wrong length would break the whole chain.
//
//   - The chain ends with jump $ff66, that is offset -154, whose destination by
//     S 4.7.2 is $5171 - 154 - 2 = $50d5: exactly the initial program counter
//     the chain started from. Any mistake in any earlier length would land
//     somewhere else.
//
//   - Every packed address called along the way expands (S 1.2.3) to a byte
//     address holding a plausible routine header, that is a local variable
//     count of 15 or less (S 5.2): $2afd, $732b, $7316, $4e7b, $3b03, $42e2 and
//     $2b54 give 3, 2, 2, 1, 1, 0 and 1 locals respectively.
func TestDecodeInstructionsZork1(t *testing.T) {
	const path = "testdata/stories/zork1-r119-880429.z3"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("story fixture unavailable: %v", err)
	}
	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory(%s) error = %v, want nil", path, err)
	}
	m := newMemory(story)

	if story.initialPC != 0x50d5 {
		t.Fatalf("initial program counter = 0x%04x, want 0x50d5", story.initialPC)
	}

	// Shorthands for the operand types, so that the table below stays readable.
	const (
		lc = operandLarge
		sc = operandSmall
		vr = operandVariable
	)
	want := []struct {
		addr  uint32
		op    opcode
		form  instructionForm
		types []operandType
		next  uint32
	}{
		{0x50d5, opCall, formVariable, []operandType{lc, lc, lc}, 0x50de},
		{0x50de, opStoreW, formVariable, []operandType{vr, sc, sc}, 0x50e3},
		{0x50e3, opCall, formVariable, []operandType{lc, lc, lc}, 0x50ec},
		{0x50ec, opCall, formVariable, []operandType{lc, lc, lc}, 0x50f5},
		{0x50f5, opStoreW, formVariable, []operandType{vr, sc, sc}, 0x50fa},
		{0x50fa, opCall, formVariable, []operandType{lc, lc, sc}, 0x5102},
		{0x5102, opCall, formVariable, []operandType{lc, lc, sc}, 0x510a},
		{0x510a, opPutProp, formVariable, []operandType{sc, sc, sc}, 0x510f},
		{0x510f, opAdd, formLong, []operandType{vr, sc}, 0x5113},
		{0x5113, opStoreW, formVariable, []operandType{vr, sc, vr}, 0x5118},
		{0x5118, opAdd, formLong, []operandType{vr, sc}, 0x511c},
		{0x511c, opStoreW, formVariable, []operandType{vr, sc, vr}, 0x5121},
		{0x5121, opAdd, formLong, []operandType{vr, sc}, 0x5125},
		{0x5125, opStoreW, formVariable, []operandType{vr, sc, vr}, 0x512a},
		{0x512a, opAdd, formLong, []operandType{vr, sc}, 0x512e},
		{0x512e, opStoreW, formVariable, []operandType{vr, sc, vr}, 0x5133},
		{0x5133, opAdd, formLong, []operandType{vr, sc}, 0x5137},
		{0x5137, opStoreW, formVariable, []operandType{vr, sc, vr}, 0x513c},
		{0x513c, opAdd, formLong, []operandType{vr, sc}, 0x5140},
		{0x5140, opStoreW, formVariable, []operandType{vr, sc, vr}, 0x5145},
		{0x5145, opStore, formLong, []operandType{sc, sc}, 0x5148},
		{0x5148, opCall, formVariable, []operandType{lc, sc}, 0x514e},
		{0x514e, opTestAttr, formLong, []operandType{vr, sc}, 0x5152},
		{0x5152, opCall, formVariable, []operandType{lc}, 0x5157},
		{0x5157, opNewLine, formShort, nil, 0x5158},
		{0x5158, opStore, formLong, []operandType{sc, sc}, 0x515b},
		{0x515b, opStore, formLong, []operandType{sc, sc}, 0x515e},
		{0x515e, opStore, formLong, []operandType{sc, vr}, 0x5161},
		{0x5161, opInsertObj, formLong, []operandType{vr, vr}, 0x5164},
		{0x5164, opCall, formVariable, []operandType{lc}, 0x5169},
		{0x5169, opCall, formVariable, []operandType{lc}, 0x516e},
		{0x516e, opJump, formShort, []operandType{lc}, 0x5171},
	}

	pc := story.initialPC
	var last instruction
	for i, tt := range want {
		if pc != tt.addr {
			t.Fatalf("instruction %d: program counter is 0x%04x, want 0x%04x", i, pc, tt.addr)
		}
		inst, err := decodeInstruction(m, pc)
		if err != nil {
			t.Fatalf("instruction %d at 0x%04x: decodeInstruction() error = %v, want nil", i, pc, err)
		}
		if inst.op != tt.op {
			t.Errorf("instruction %d at 0x%04x = %v, want %v", i, pc, inst.op, tt.op)
		}
		if inst.form != tt.form {
			t.Errorf("instruction %d at 0x%04x: form = %v, want %v", i, pc, inst.form, tt.form)
		}
		if got := len(inst.operands()); got != len(tt.types) {
			t.Errorf("instruction %d at 0x%04x: %d operand(s), want %d", i, pc, got, len(tt.types))
		} else {
			for j, kind := range tt.types {
				if inst.ops[j].kind != kind {
					t.Errorf("instruction %d at 0x%04x: operand %d is a %s, want a %s",
						i, pc, j+1, inst.ops[j].kind, kind)
				}
			}
		}
		if inst.next != tt.next {
			t.Fatalf("instruction %d at 0x%04x: next = 0x%04x, want 0x%04x", i, pc, inst.next, tt.next)
		}
		// Every call and add in this run ends with a store byte of $00, the
		// stack (S 4.2.2), which is also the last byte of the instruction, so
		// the store byte was read from where the chain says it is.
		if tt.op == opCall || tt.op == opAdd {
			if !inst.hasStore || inst.store != 0x00 {
				t.Errorf("instruction %d at 0x%04x: store = 0x%02x (present %t), want 0x00 present",
					i, pc, inst.store, inst.hasStore)
			}
		} else if inst.hasStore {
			t.Errorf("instruction %d at 0x%04x: %v decoded a store variable", i, pc, inst.op)
		}
		pc, last = inst.next, inst
	}

	// The branch in the middle of the chain: test_attr V10 3, taken on true
	// with a one-byte offset of 8, so by S 4.7.2 it skips the call and the
	// new_line that follow and lands on the store at $5158.
	branch, err := decodeInstruction(m, 0x514e)
	if err != nil {
		t.Fatalf("decodeInstruction(0x514e) error = %v, want nil", err)
	}
	if !branch.hasBranch || !branch.branch.onTrue || branch.branch.long || branch.branch.offset != 8 {
		t.Fatalf("branch at 0x514e = %+v, want a one-byte branch on true with offset 8", branch.branch)
	}
	target, err := branch.branch.target(branch.next)
	if err != nil {
		t.Fatalf("target() error = %v, want nil", err)
	}
	if target != 0x5158 {
		t.Errorf("branch at 0x514e targets 0x%04x, want 0x5158", target)
	}

	// The jump that closes the loop. Its operand is not branch data but an
	// ordinary signed 16-bit offset (S 15, jump), applied by the same formula.
	if last.op != opJump {
		t.Fatalf("the chain ends with %v, want %v", last.op, opJump)
	}
	if got := last.ops[0].value; got != 0xff66 {
		t.Fatalf("jump offset = 0x%04x, want 0xff66", got)
	}
	dest, err := offsetAddress(last.next, int32(signed(last.ops[0].value))-branchBias)
	if err != nil {
		t.Fatalf("offsetAddress() error = %v, want nil", err)
	}
	if dest != story.initialPC {
		t.Errorf("the jump at 0x%04x lands on 0x%04x, want the initial program counter 0x%04x",
			last.addr, dest, story.initialPC)
	}
}

// FuzzDecodeInstruction asserts that decoding arbitrary bytes never panics,
// always terminates, and always reports a length greater than zero, so that a
// caller walking instructions with the returned next address cannot loop
// forever on the same instruction.
func FuzzDecodeInstruction(f *testing.F) {
	f.Add([]byte{0xbb})                                                 // new_line
	f.Add([]byte{0x54, 0x98, 0x02, 0x00})                               // add V98 2 -> sp
	f.Add([]byte{0xe0, 0x03, 0x2a, 0xfd, 0x83, 0xa4, 0x00})             // call
	f.Add([]byte{0x05, 0x02, 0x00, 0xd4})                               // inc_chk with a branch
	f.Add([]byte{0xa1, 0x0c, 0x03, 0x80, 0x00})                         // get_sibling, store and branch
	f.Add([]byte{0xb2, 0x11, 0xaa, 0x46, 0x34, 0x16, 0x45, 0x9c, 0xa5}) // print "Hello.^"
	f.Add([]byte{0xbe, 0x00, 0x00})                                     // the extended opcode byte
	f.Add(make([]byte, decodeStoryEnd-decodeCodeBase))

	f.Fuzz(func(t *testing.T, data []byte) {
		// The fuzz data fills the story's free high memory. The header and the
		// tables validated at load time are left intact so that the story
		// always loads and the decoder is always given a usable memory view.
		image := validTestHeader().build()
		copy(image[decodeCodeBase:decodeStoryEnd], data)
		story, err := LoadStory(image)
		if err != nil {
			t.Fatalf("LoadStory() error = %v, want nil", err)
		}
		m := newMemory(story)

		for addr := uint32(decodeCodeBase); addr < decodeStoryEnd; addr++ {
			inst, err := decodeInstruction(m, addr)
			if err != nil {
				var de *DecodeError
				if !errors.As(err, &de) {
					t.Fatalf("decodeInstruction(0x%04x) error = %v (%T), want a *DecodeError", addr, err, err)
				}
				if de.Addr != addr {
					t.Fatalf("decodeInstruction(0x%04x) reported address 0x%04x", addr, de.Addr)
				}
				continue
			}
			if inst.addr != addr {
				t.Fatalf("decodeInstruction(0x%04x) reported address 0x%04x", addr, inst.addr)
			}
			if inst.length() == 0 {
				t.Fatalf("decodeInstruction(0x%04x) returned a zero-length instruction", addr)
			}
			if inst.next > m.size() {
				t.Fatalf("decodeInstruction(0x%04x) ends at 0x%04x, past the end of the story (0x%04x)",
					addr, inst.next, m.size())
			}
			if int(inst.numOps) > maxOperandsV3 {
				t.Fatalf("decodeInstruction(0x%04x) returned %d operands", addr, inst.numOps)
			}
			if inst.hasText != inst.op.hasText() || inst.hasStore != inst.op.storesResult() ||
				inst.hasBranch != inst.op.branches() {
				t.Fatalf("decodeInstruction(0x%04x) = %v disagrees with the opcode table", addr, &inst)
			}
		}

		// Walking from one instruction to the next must reach the end of the
		// story or an error in a bounded number of steps, because every
		// instruction has a length of at least one byte.
		steps := 0
		for pc := uint32(decodeCodeBase); ; steps++ {
			if steps > decodeStoryEnd-decodeCodeBase {
				t.Fatalf("walking instructions from 0x%04x did not terminate", uint32(decodeCodeBase))
			}
			inst, err := decodeInstruction(m, pc)
			if err != nil || inst.next >= decodeStoryEnd {
				break
			}
			pc = inst.next
		}
	})
}
