package zmachine

import "fmt"

// Instruction identity (S 4.3, S 14).
//
// An instruction is named by an operand count together with an opcode number
// within that count: opcode number 13 is store as a 2OP and print_paddr as a
// 1OP. The tables below record, for every opcode number in every count, the
// name of the instruction, the first version that defines it, and what follows
// its operands - a store variable (S 4.6), branch data (S 4.7) or an inline
// string (S 4.8).

// operandCount is the number of operands an instruction form supplies (S 4.3).
type operandCount uint8

const (
	count0OP operandCount = iota
	count1OP
	count2OP
	countVAR
)

// String returns the name the opcode table of S 14 uses for the count.
func (c operandCount) String() string {
	switch c {
	case count0OP:
		return "0OP"
	case count1OP:
		return "1OP"
	case count2OP:
		return "2OP"
	case countVAR:
		return "VAR"
	default:
		return "?OP"
	}
}

// opcode names one instruction. It packs the operand count into the high byte
// and the opcode number into the low byte, so that an instruction's identity is
// a single comparable value.
type opcode uint16

// makeOpcode builds the opcode with the given count and number.
func makeOpcode(count operandCount, number uint8) opcode {
	return opcode(count)<<8 | opcode(number)
}

// count returns the operand count the opcode belongs to.
func (o opcode) count() operandCount { return operandCount(o >> 8) }

// number returns the opcode number within that count.
func (o opcode) number() uint8 { return uint8(o) }

// Two-operand opcodes (S 14, 2OP). Opcode number 0 is not an instruction in any
// version, and numbers 25 to 28 are Version 4 and later, so neither appears
// here.
const (
	opJE          = opcode(count2OP)<<8 | 0x01
	opJL          = opcode(count2OP)<<8 | 0x02
	opJG          = opcode(count2OP)<<8 | 0x03
	opDecChk      = opcode(count2OP)<<8 | 0x04
	opIncChk      = opcode(count2OP)<<8 | 0x05
	opJin         = opcode(count2OP)<<8 | 0x06
	opTest        = opcode(count2OP)<<8 | 0x07
	opOr          = opcode(count2OP)<<8 | 0x08
	opAnd         = opcode(count2OP)<<8 | 0x09
	opTestAttr    = opcode(count2OP)<<8 | 0x0a
	opSetAttr     = opcode(count2OP)<<8 | 0x0b
	opClearAttr   = opcode(count2OP)<<8 | 0x0c
	opStore       = opcode(count2OP)<<8 | 0x0d
	opInsertObj   = opcode(count2OP)<<8 | 0x0e
	opLoadW       = opcode(count2OP)<<8 | 0x0f
	opLoadB       = opcode(count2OP)<<8 | 0x10
	opGetProp     = opcode(count2OP)<<8 | 0x11
	opGetPropAddr = opcode(count2OP)<<8 | 0x12
	opGetNextProp = opcode(count2OP)<<8 | 0x13
	opAdd         = opcode(count2OP)<<8 | 0x14
	opSub         = opcode(count2OP)<<8 | 0x15
	opMul         = opcode(count2OP)<<8 | 0x16
	opDiv         = opcode(count2OP)<<8 | 0x17
	opMod         = opcode(count2OP)<<8 | 0x18
)

// One-operand opcodes (S 14, 1OP). Every number except 8, call_1s, is defined
// in Version 3.
const (
	opJZ         = opcode(count1OP)<<8 | 0x00
	opGetSibling = opcode(count1OP)<<8 | 0x01
	opGetChild   = opcode(count1OP)<<8 | 0x02
	opGetParent  = opcode(count1OP)<<8 | 0x03
	opGetPropLen = opcode(count1OP)<<8 | 0x04
	opInc        = opcode(count1OP)<<8 | 0x05
	opDec        = opcode(count1OP)<<8 | 0x06
	opPrintAddr  = opcode(count1OP)<<8 | 0x07
	opRemoveObj  = opcode(count1OP)<<8 | 0x09
	opPrintObj   = opcode(count1OP)<<8 | 0x0a
	opRet        = opcode(count1OP)<<8 | 0x0b
	opJump       = opcode(count1OP)<<8 | 0x0c
	opPrintPaddr = opcode(count1OP)<<8 | 0x0d
	opLoad       = opcode(count1OP)<<8 | 0x0e
	opNot        = opcode(count1OP)<<8 | 0x0f
)

// Zero-operand opcodes (S 14, 0OP). Numbers 14 and 15 are Version 5, so they do
// not appear here.
const (
	opRTrue      = opcode(count0OP)<<8 | 0x00
	opRFalse     = opcode(count0OP)<<8 | 0x01
	opPrint      = opcode(count0OP)<<8 | 0x02
	opPrintRet   = opcode(count0OP)<<8 | 0x03
	opNop        = opcode(count0OP)<<8 | 0x04
	opSave       = opcode(count0OP)<<8 | 0x05
	opRestore    = opcode(count0OP)<<8 | 0x06
	opRestart    = opcode(count0OP)<<8 | 0x07
	opRetPopped  = opcode(count0OP)<<8 | 0x08
	opPop        = opcode(count0OP)<<8 | 0x09
	opQuit       = opcode(count0OP)<<8 | 0x0a
	opNewLine    = opcode(count0OP)<<8 | 0x0b
	opShowStatus = opcode(count0OP)<<8 | 0x0c
	opVerify     = opcode(count0OP)<<8 | 0x0d
)

// Variable-operand opcodes (S 14, VAR). Numbers 12 to 18 and 22 to 31 are
// Version 4 and later, so they do not appear here.
const (
	opCall         = opcode(countVAR)<<8 | 0x00
	opStoreW       = opcode(countVAR)<<8 | 0x01
	opStoreB       = opcode(countVAR)<<8 | 0x02
	opPutProp      = opcode(countVAR)<<8 | 0x03
	opSRead        = opcode(countVAR)<<8 | 0x04
	opPrintChar    = opcode(countVAR)<<8 | 0x05
	opPrintNum     = opcode(countVAR)<<8 | 0x06
	opRandom       = opcode(countVAR)<<8 | 0x07
	opPush         = opcode(countVAR)<<8 | 0x08
	opPull         = opcode(countVAR)<<8 | 0x09
	opSplitWindow  = opcode(countVAR)<<8 | 0x0a
	opSetWindow    = opcode(countVAR)<<8 | 0x0b
	opOutputStream = opcode(countVAR)<<8 | 0x13
	opInputStream  = opcode(countVAR)<<8 | 0x14
	opSoundEffect  = opcode(countVAR)<<8 | 0x15
)

// opcodeInfo describes one opcode number.
type opcodeInfo struct {
	// name is the Inform assembly name of the instruction (S 14). It is empty
	// for opcode numbers that name no instruction in any version.
	name string
	// since is the first version in which the opcode number has the meaning
	// recorded here, or 0 if it names no instruction in any version. It follows
	// the "V" column of S 14: an opcode is illegal before this version.
	since uint8
	// store is set when the instruction is followed by a byte giving the
	// variable its result is written to (S 4.6).
	store bool
	// branch is set when the instruction is followed by branch data (S 4.7).
	branch bool
	// text is set when the instruction is followed by an inline encoded string
	// (S 4.8). Only print and print_ret are.
	text bool
}

// definedInV3 reports whether a Version 3 story may legally contain this
// opcode. It is illegal for a story to contain an opcode not specified for its
// version (S 14.2).
func (i opcodeInfo) definedInV3() bool { return i.since != 0 && i.since <= versionV3 }

// The opcode tables of S 14. Each is indexed by opcode number, so a gap in the
// table is an opcode number with no instruction.
//
// The arrays are immutable data. Nothing in this package writes to them.
var (
	opcodes2OP = [32]opcodeInfo{
		// Opcode number 0 is marked "------" in S 14: it is not an instruction
		// in any version.
		0x01: {name: "je", since: 1, branch: true},
		0x02: {name: "jl", since: 1, branch: true},
		0x03: {name: "jg", since: 1, branch: true},
		0x04: {name: "dec_chk", since: 1, branch: true},
		0x05: {name: "inc_chk", since: 1, branch: true},
		0x06: {name: "jin", since: 1, branch: true},
		0x07: {name: "test", since: 1, branch: true},
		0x08: {name: "or", since: 1, store: true},
		0x09: {name: "and", since: 1, store: true},
		0x0a: {name: "test_attr", since: 1, branch: true},
		0x0b: {name: "set_attr", since: 1},
		0x0c: {name: "clear_attr", since: 1},
		0x0d: {name: "store", since: 1},
		0x0e: {name: "insert_obj", since: 1},
		0x0f: {name: "loadw", since: 1, store: true},
		0x10: {name: "loadb", since: 1, store: true},
		0x11: {name: "get_prop", since: 1, store: true},
		0x12: {name: "get_prop_addr", since: 1, store: true},
		0x13: {name: "get_next_prop", since: 1, store: true},
		0x14: {name: "add", since: 1, store: true},
		0x15: {name: "sub", since: 1, store: true},
		0x16: {name: "mul", since: 1, store: true},
		0x17: {name: "div", since: 1, store: true},
		0x18: {name: "mod", since: 1, store: true},
		0x19: {name: "call_2s", since: 4, store: true},
		0x1a: {name: "call_2n", since: 5},
		0x1b: {name: "set_colour", since: 5},
		0x1c: {name: "throw", since: 5},
	}

	opcodes1OP = [16]opcodeInfo{
		0x00: {name: "jz", since: 1, branch: true},
		0x01: {name: "get_sibling", since: 1, store: true, branch: true},
		0x02: {name: "get_child", since: 1, store: true, branch: true},
		0x03: {name: "get_parent", since: 1, store: true},
		0x04: {name: "get_prop_len", since: 1, store: true},
		0x05: {name: "inc", since: 1},
		0x06: {name: "dec", since: 1},
		0x07: {name: "print_addr", since: 1},
		0x08: {name: "call_1s", since: 4, store: true},
		0x09: {name: "remove_obj", since: 1},
		0x0a: {name: "print_obj", since: 1},
		0x0b: {name: "ret", since: 1},
		// jump is not a branch instruction: its operand is an ordinary signed
		// 16-bit offset (S 15, jump).
		0x0c: {name: "jump", since: 1},
		0x0d: {name: "print_paddr", since: 1},
		0x0e: {name: "load", since: 1, store: true},
		// Opcode number 15 is not in Versions 1 to 4 and call_1n from Version
		// 5. Version 3 sees not.
		0x0f: {name: "not", since: 1, store: true},
	}

	opcodes0OP = [16]opcodeInfo{
		0x00: {name: "rtrue", since: 1},
		0x01: {name: "rfalse", since: 1},
		0x02: {name: "print", since: 1, text: true},
		0x03: {name: "print_ret", since: 1, text: true},
		0x04: {name: "nop", since: 1},
		// save and restore branch in Versions 1 to 3, store a result in
		// Version 4, and are illegal from Version 5, where the extended forms
		// replace them.
		0x05: {name: "save", since: 1, branch: true},
		0x06: {name: "restore", since: 1, branch: true},
		0x07: {name: "restart", since: 1},
		0x08: {name: "ret_popped", since: 1},
		0x09: {name: "pop", since: 1},
		0x0a: {name: "quit", since: 1},
		0x0b: {name: "new_line", since: 1},
		// show_status exists only in Version 3; from Version 4 it is illegal.
		0x0c: {name: "show_status", since: 3},
		0x0d: {name: "verify", since: 3, branch: true},
		// The opcode byte $be introduces an extended opcode only "if the
		// opcode is 190 ($BE in hexadecimal) and the version is 5 or later"
		// (S 4.3). In Version 3 it is simply the short-form 0OP:14, which no
		// version before 5 defines, so a Version 3 story containing it is
		// rejected like any other undefined opcode.
		0x0e: {name: "extended", since: 5},
		0x0f: {name: "piracy", since: 5, branch: true},
	}

	opcodesVAR = [32]opcodeInfo{
		// call is call_vs from Version 4, with the same encoding.
		0x00: {name: "call", since: 1, store: true},
		0x01: {name: "storew", since: 1},
		0x02: {name: "storeb", since: 1},
		0x03: {name: "put_prop", since: 1},
		// sread takes two operands in Version 3, four from Version 4, and is
		// replaced by the storing aread in Version 5. The Version 3 form
		// neither stores nor branches.
		0x04: {name: "sread", since: 1},
		0x05: {name: "print_char", since: 1},
		0x06: {name: "print_num", since: 1},
		0x07: {name: "random", since: 1, store: true},
		0x08: {name: "push", since: 1},
		// pull stores a result only in Version 6.
		0x09: {name: "pull", since: 1},
		0x0a: {name: "split_window", since: 3},
		0x0b: {name: "set_window", since: 3},
		0x0c: {name: "call_vs2", since: 4, store: true},
		0x0d: {name: "erase_window", since: 4},
		0x0e: {name: "erase_line", since: 4},
		0x0f: {name: "set_cursor", since: 4},
		0x10: {name: "get_cursor", since: 4},
		0x11: {name: "set_text_style", since: 4},
		0x12: {name: "buffer_mode", since: 4},
		0x13: {name: "output_stream", since: 3},
		0x14: {name: "input_stream", since: 3},
		// S 14 gives sound_effect the version "5/3": it belongs to the Version
		// 5 specification but is "used also in one solitary Version 3 game,
		// 'The Lurking Horror'" (S 14 remarks). Its encoding is unambiguous -
		// variable operands, no store, no branch - so it is decoded rather than
		// refused, because refusing it would make a known Version 3 story
		// undecodable.
		0x15: {name: "sound_effect", since: 3},
		0x16: {name: "read_char", since: 4, store: true},
		0x17: {name: "scan_table", since: 4, store: true, branch: true},
		0x18: {name: "not", since: 5, store: true},
		0x19: {name: "call_vn", since: 5},
		0x1a: {name: "call_vn2", since: 5},
		0x1b: {name: "tokenise", since: 5},
		0x1c: {name: "encode_text", since: 5},
		0x1d: {name: "copy_table", since: 5},
		0x1e: {name: "print_table", since: 5},
		0x1f: {name: "check_arg_count", since: 5, branch: true},
	}
)

// info returns the table entry describing the opcode.
//
// The masks cannot discard anything for an opcode built by the decoder, whose
// numbers already come from the bottom 4 or 5 bits of an opcode byte (S 4.3.1
// to S 4.3.3). They are there so that no opcode value can index outside a
// table.
func (o opcode) info() opcodeInfo {
	switch o.count() {
	case count0OP:
		return opcodes0OP[o.number()&0x0f]
	case count1OP:
		return opcodes1OP[o.number()&0x0f]
	case count2OP:
		return opcodes2OP[o.number()&0x1f]
	case countVAR:
		return opcodesVAR[o.number()&0x1f]
	default:
		return opcodeInfo{}
	}
}

// name returns the Inform assembly name of the instruction, or the empty string
// for an opcode number that names no instruction in any version.
func (o opcode) name() string { return o.info().name }

// storesResult reports whether the instruction is followed by a store variable
// (S 4.6).
func (o opcode) storesResult() bool { return o.info().store }

// branches reports whether the instruction is followed by branch data (S 4.7).
func (o opcode) branches() bool { return o.info().branch }

// hasText reports whether the instruction is followed by an inline encoded
// string (S 4.8).
func (o opcode) hasText() bool { return o.info().text }

// String returns the opcode in the form used by the tables of S 14, for example
// "2OP:20 add", or just "2OP:29" for an opcode number naming no instruction.
func (o opcode) String() string {
	if name := o.name(); name != "" {
		return fmt.Sprintf("%s:%d %s", o.count(), o.number(), name)
	}
	return fmt.Sprintf("%s:%d", o.count(), o.number())
}
