package zmachine

import "testing"

// TestOpcodeIdentity checks that an opcode keeps the operand count and number
// it was built from. The two together name an instruction (S 4.3), so opcode
// number 13 must not be confused between counts: it is store as a 2OP and
// print_paddr as a 1OP.
func TestOpcodeIdentity(t *testing.T) {
	counts := []operandCount{count0OP, count1OP, count2OP, countVAR}
	seen := make(map[opcode]bool)
	for _, count := range counts {
		for number := 0; number < 32; number++ {
			op := makeOpcode(count, uint8(number))
			if op.count() != count || op.number() != uint8(number) {
				t.Fatalf("makeOpcode(%v, %d) = %v:%d", count, number, op.count(), op.number())
			}
			if seen[op] {
				t.Fatalf("makeOpcode(%v, %d) collides with an earlier opcode", count, number)
			}
			seen[op] = true
		}
	}

	if got, want := opStore.String(), "2OP:13 store"; got != want {
		t.Errorf("opStore.String() = %q, want %q", got, want)
	}
	if got, want := opPrintPaddr.String(), "1OP:13 print_paddr"; got != want {
		t.Errorf("opPrintPaddr.String() = %q, want %q", got, want)
	}
	// An opcode number that names no instruction still has a printable
	// identity, because a diagnostic has to be able to report it.
	if got, want := makeOpcode(count2OP, 0).String(), "2OP:0"; got != want {
		t.Errorf("2OP:0 String() = %q, want %q", got, want)
	}
}

// TestOpcodeTableConsistency checks the shape of the tables rather than their
// contents: an entry either describes an instruction or is empty, and only the
// two text opcodes of S 4.8 carry inline text.
func TestOpcodeTableConsistency(t *testing.T) {
	tables := []struct {
		count operandCount
		table []opcodeInfo
	}{
		{count: count0OP, table: opcodes0OP[:]},
		{count: count1OP, table: opcodes1OP[:]},
		{count: count2OP, table: opcodes2OP[:]},
		{count: countVAR, table: opcodesVAR[:]},
	}
	for _, tab := range tables {
		for number, info := range tab.table {
			op := makeOpcode(tab.count, uint8(number))
			switch {
			case info.name == "":
				if info.since != 0 || info.store || info.branch || info.text {
					t.Errorf("%v: unnamed opcode has table data %+v", op, info)
				}
				if info.definedInV3() {
					t.Errorf("%v: unnamed opcode reported as defined in Version 3", op)
				}
			case info.since == 0:
				t.Errorf("%v: named opcode has no version", op)
			}
			if info.text && op != opPrint && op != opPrintRet {
				t.Errorf("%v: carries inline text; S 4.8 says only print and print_ret do", op)
			}
		}
	}

	if !opPrint.hasText() || !opPrintRet.hasText() {
		t.Errorf("print and print_ret must carry inline text (S 4.8)")
	}
}

// TestOpcodeStoreAndBranchFlags checks the "St" and "Br" columns of S 14 for
// the opcodes where getting them wrong would silently misalign every following
// instruction. Each case names the rule it is protecting.
func TestOpcodeStoreAndBranchFlags(t *testing.T) {
	tests := []struct {
		op     opcode
		store  bool
		branch bool
		why    string
	}{
		{op: opGetSibling, store: true, branch: true, why: "get_sibling both stores and branches"},
		{op: opGetChild, store: true, branch: true, why: "get_child both stores and branches"},
		{op: opGetParent, store: true, why: "get_parent stores but does not branch, unlike its siblings"},
		{op: opJZ, branch: true, why: "jz branches"},
		{op: opJump, why: "jump is not a branch instruction: its operand is a signed offset (S 15, jump)"},
		{op: opStore, why: "2OP:13 store writes through its first operand, so it has no store byte"},
		{op: opLoad, store: true, why: "load names a variable by number and stores its contents"},
		{op: opInc, why: "inc has neither store nor branch"},
		{op: opIncChk, branch: true, why: "inc_chk branches but does not store"},
		{op: opNot, store: true, why: "1OP:15 in Version 3 is not, which stores"},
		{op: opSave, branch: true, why: "save branches in Versions 1 to 3 rather than storing"},
		{op: opRestore, branch: true, why: "restore branches in Versions 1 to 3 rather than storing"},
		{op: opVerify, branch: true, why: "verify branches"},
		{op: opCall, store: true, why: "call always stores, even when the story ignores the result"},
		{op: opSRead, why: "sread in Version 3 neither stores nor branches; aread stores from Version 5"},
		{op: opPull, why: "pull stores only in Version 6"},
		{op: opRandom, store: true, why: "random stores"},
		{op: opPutProp, why: "put_prop neither stores nor branches"},
		{op: opTestAttr, branch: true, why: "test_attr branches"},
		{op: opSetAttr, why: "set_attr does not branch, unlike test_attr"},
		{op: opNewLine, why: "new_line is a bare opcode byte"},
		{op: opPrint, why: "print is followed by text, not by a store or branch"},
	}
	for _, tt := range tests {
		if got := tt.op.storesResult(); got != tt.store {
			t.Errorf("%v storesResult() = %t, want %t: %s", tt.op, got, tt.store, tt.why)
		}
		if got := tt.op.branches(); got != tt.branch {
			t.Errorf("%v branches() = %t, want %t: %s", tt.op, got, tt.branch, tt.why)
		}
	}
}

// TestOpcodesDefinedInV3 lists every opcode number that a Version 3 story may
// contain, taken from the "V" column of S 14. Anything else is illegal for a
// Version 3 story (S 14.2).
func TestOpcodesDefinedInV3(t *testing.T) {
	defined := map[opcode]string{
		opJE: "je", opJL: "jl", opJG: "jg", opDecChk: "dec_chk", opIncChk: "inc_chk",
		opJin: "jin", opTest: "test", opOr: "or", opAnd: "and", opTestAttr: "test_attr",
		opSetAttr: "set_attr", opClearAttr: "clear_attr", opStore: "store",
		opInsertObj: "insert_obj", opLoadW: "loadw", opLoadB: "loadb",
		opGetProp: "get_prop", opGetPropAddr: "get_prop_addr", opGetNextProp: "get_next_prop",
		opAdd: "add", opSub: "sub", opMul: "mul", opDiv: "div", opMod: "mod",

		opJZ: "jz", opGetSibling: "get_sibling", opGetChild: "get_child",
		opGetParent: "get_parent", opGetPropLen: "get_prop_len", opInc: "inc", opDec: "dec",
		opPrintAddr: "print_addr", opRemoveObj: "remove_obj", opPrintObj: "print_obj",
		opRet: "ret", opJump: "jump", opPrintPaddr: "print_paddr", opLoad: "load", opNot: "not",

		opRTrue: "rtrue", opRFalse: "rfalse", opPrint: "print", opPrintRet: "print_ret",
		opNop: "nop", opSave: "save", opRestore: "restore", opRestart: "restart",
		opRetPopped: "ret_popped", opPop: "pop", opQuit: "quit", opNewLine: "new_line",
		opShowStatus: "show_status", opVerify: "verify",

		opCall: "call", opStoreW: "storew", opStoreB: "storeb", opPutProp: "put_prop",
		opSRead: "sread", opPrintChar: "print_char", opPrintNum: "print_num",
		opRandom: "random", opPush: "push", opPull: "pull",
		opSplitWindow: "split_window", opSetWindow: "set_window",
		opOutputStream: "output_stream", opInputStream: "input_stream",
		// S 14 gives sound_effect the version "5/3": a Version 5 opcode that a
		// Version 3 story, 'The Lurking Horror', nevertheless uses.
		opSoundEffect: "sound_effect",
	}

	for op, name := range defined {
		if !op.info().definedInV3() {
			t.Errorf("%v should be defined in Version 3", op)
		}
		if got := op.name(); got != name {
			t.Errorf("%v name() = %q, want %q", op, got, name)
		}
	}

	counts := []operandCount{count0OP, count1OP, count2OP, countVAR}
	for _, count := range counts {
		limit := 32
		if count == count0OP || count == count1OP {
			limit = 16
		}
		for number := 0; number < limit; number++ {
			op := makeOpcode(count, uint8(number))
			_, want := defined[op]
			if got := op.info().definedInV3(); got != want {
				t.Errorf("%v definedInV3() = %t, want %t", op, got, want)
			}
		}
	}
}

// TestOpcodesNotDefinedInV3 names the later-version opcodes most likely to be
// mistaken for Version 3 instructions, and checks that the table still knows
// what they are so that a diagnostic can say which version introduced them.
func TestOpcodesNotDefinedInV3(t *testing.T) {
	tests := []struct {
		op    opcode
		name  string
		since uint8
	}{
		{op: makeOpcode(count2OP, 0x19), name: "call_2s", since: 4},
		{op: makeOpcode(count2OP, 0x1a), name: "call_2n", since: 5},
		{op: makeOpcode(count1OP, 0x08), name: "call_1s", since: 4},
		// The byte $be is the first byte of an extended opcode only from
		// Version 5 (S 4.3); in Version 3 it decodes to this 0OP.
		{op: makeOpcode(count0OP, 0x0e), name: "extended", since: 5},
		{op: makeOpcode(count0OP, 0x0f), name: "piracy", since: 5},
		{op: makeOpcode(countVAR, 0x0c), name: "call_vs2", since: 4},
		{op: makeOpcode(countVAR, 0x16), name: "read_char", since: 4},
		{op: makeOpcode(countVAR, 0x17), name: "scan_table", since: 4},
		{op: makeOpcode(countVAR, 0x1a), name: "call_vn2", since: 5},
		{op: makeOpcode(countVAR, 0x1f), name: "check_arg_count", since: 5},
	}
	for _, tt := range tests {
		info := tt.op.info()
		if info.definedInV3() {
			t.Errorf("%v is not a Version 3 opcode", tt.op)
		}
		if info.name != tt.name || info.since != tt.since {
			t.Errorf("%v = %q since Version %d, want %q since Version %d", tt.op, info.name, info.since, tt.name, tt.since)
		}
	}

	// Opcode numbers that name no instruction in any version are marked
	// "------" in S 14.
	for _, op := range []opcode{
		makeOpcode(count2OP, 0x00),
		makeOpcode(count2OP, 0x1d),
		makeOpcode(count2OP, 0x1f),
	} {
		if info := op.info(); info.since != 0 || info.name != "" {
			t.Errorf("%v = %+v, want an empty entry", op, info)
		}
	}
}
