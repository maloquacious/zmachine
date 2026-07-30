package zmachine

import "testing"

// exec2OPStore runs a storing two-operand instruction with the given operands
// and returns the value it wrote. The instruction is assembled in variable
// form so that either operand may be a large constant (S 4.4.2).
func exec2OPStore(t *testing.T, number uint8, a, b uint16) uint16 {
	t.Helper()
	code := append(encodeVar(count2OP, number, largeOp(a), largeOp(b)), globalFirst)
	m := newTestMachine(t, code...)
	mustStep(t, m)
	got, err := m.readGlobal(globalFirst)
	if err != nil {
		t.Fatalf("readGlobal() error = %v", err)
	}
	return got
}

// execBranch runs an instruction and reports whether it took its branch, by
// looking at where the program counter ended up.
func execBranch(t *testing.T, m *Machine, code []byte) bool {
	t.Helper()
	next := m.pc + uint32(len(code))
	mustStep(t, m)
	return m.pc != next
}

// branchTaken assembles a branching two-operand instruction whose branch, if
// taken, moves execution somewhere other than the following instruction, and
// reports whether it was taken.
func branchTaken(t *testing.T, number uint8, ops ...opnd) bool {
	t.Helper()
	code := join(encodeVar(count2OP, number, ops...), branch2(true, 20))
	m := newTestMachine(t, code...)
	return execBranch(t, m, code)
}

// TestArithmetic covers add, sub and mul (S 15). They are signed 16-bit
// operations, and an out-of-range result is reduced modulo $10000 (S 2.3.2),
// which is what 16-bit two's complement arithmetic gives.
func TestArithmetic(t *testing.T) {
	tests := []struct {
		name   string
		number uint8
		a, b   int16
		want   int16
	}{
		{name: "add", number: 0x14, a: 3, b: 4, want: 7},
		{name: "add negative", number: 0x14, a: -3, b: 4, want: 1},
		{name: "add wraps past 32767", number: 0x14, a: 32767, b: 1, want: -32768},
		{name: "add wraps below -32768", number: 0x14, a: -32768, b: -1, want: 32767},
		{name: "sub", number: 0x15, a: 10, b: 4, want: 6},
		{name: "sub to negative", number: 0x15, a: 4, b: 10, want: -6},
		{name: "sub wraps", number: 0x15, a: -32768, b: 1, want: 32767},
		{name: "mul", number: 0x16, a: 6, b: 7, want: 42},
		{name: "mul negative", number: 0x16, a: -6, b: 7, want: -42},
		{name: "mul of two negatives", number: 0x16, a: -6, b: -7, want: 42},
		{name: "mul wraps modulo 0x10000", number: 0x16, a: 1000, b: 1000, want: 16960},
		{name: "mul by zero", number: 0x16, a: -1234, b: 0, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := signed(exec2OPStore(t, tt.number, unsigned(tt.a), unsigned(tt.b)))
			if got != tt.want {
				t.Errorf("%s(%d, %d) = %d, want %d", tt.name, tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestDivisionAndModulo covers div and mod (S 15). Both are signed: version
// 0.2 of the Standard wrongly called them unsigned, and the remarks to S 2.2.1
// give the correct results, which this table reproduces exactly.
//
//	-11 / 2 = -5       -11 / -2 = 5        11 / -2 = -5
//	-13 % 5 = -3        13 % -5 = 3       -13 % -5 = -3
//
// Division truncates towards zero and the remainder takes the sign of the
// dividend.
func TestDivisionAndModulo(t *testing.T) {
	const (
		numberDiv = 0x17
		numberMod = 0x18
	)

	tests := []struct {
		name   string
		number uint8
		a, b   int16
		want   int16
	}{
		{name: "div", number: numberDiv, a: 11, b: 2, want: 5},
		{name: "div of a negative", number: numberDiv, a: -11, b: 2, want: -5},
		{name: "div by a negative", number: numberDiv, a: 11, b: -2, want: -5},
		{name: "div of two negatives", number: numberDiv, a: -11, b: -2, want: 5},
		{name: "div truncates towards zero", number: numberDiv, a: -1, b: 2, want: 0},
		{name: "div of the most negative value", number: numberDiv, a: -32768, b: -1, want: -32768},
		{name: "mod", number: numberMod, a: 13, b: 5, want: 3},
		{name: "mod of a negative", number: numberMod, a: -13, b: 5, want: -3},
		{name: "mod by a negative", number: numberMod, a: 13, b: -5, want: 3},
		{name: "mod of two negatives", number: numberMod, a: -13, b: -5, want: -3},
		{name: "mod of the most negative value", number: numberMod, a: -32768, b: -1, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := signed(exec2OPStore(t, tt.number, unsigned(tt.a), unsigned(tt.b)))
			if got != tt.want {
				t.Errorf("%s(%d, %d) = %d, want %d", tt.name, tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestDivisionByZero covers S 2.3.1: it is illegal to divide by zero, or to
// ask for the remainder after division by zero, and the interpreter should
// halt with an error message. Here that is an error naming the instruction,
// never a panic and never a silent zero.
func TestDivisionByZero(t *testing.T) {
	for _, number := range []uint8{0x17, 0x18} {
		code := append(encodeVar(count2OP, number, largeOp(unsigned(-11)), largeOp(0)), globalFirst)
		m := newTestMachine(t, code...)
		err := stepErr(t, m)
		assertExecutionError(t, err, machineCodeBase, ErrExecutionFault)
	}
}

// TestBitwiseOperations covers or, and and test (S 15). S 2.2.1 makes bitwise
// operations unsigned, so no sign extension may creep in.
func TestBitwiseOperations(t *testing.T) {
	if got := exec2OPStore(t, 0x08, 0xf0f0, 0x0ff0); got != 0xfff0 {
		t.Errorf("or(0xf0f0, 0x0ff0) = 0x%04x, want 0xfff0", got)
	}
	if got := exec2OPStore(t, 0x09, 0xf0f0, 0x0ff0); got != 0x00f0 {
		t.Errorf("and(0xf0f0, 0x0ff0) = 0x%04x, want 0x00f0", got)
	}

	// S 15, test: jump if all of the flags in the bitmap are set, that is if
	// bitmap & flags == flags.
	tests := []struct {
		bitmap, flags uint16
		want          bool
	}{
		{bitmap: 0xffff, flags: 0x0101, want: true},
		{bitmap: 0x0101, flags: 0x0101, want: true},
		{bitmap: 0x0100, flags: 0x0101, want: false},
		{bitmap: 0x0000, flags: 0x0000, want: true},
		{bitmap: 0x8000, flags: 0x8000, want: true},
	}
	for _, tt := range tests {
		if got := branchTaken(t, 0x07, largeOp(tt.bitmap), largeOp(tt.flags)); got != tt.want {
			t.Errorf("test(0x%04x, 0x%04x) branched = %t, want %t", tt.bitmap, tt.flags, got, tt.want)
		}
	}
}

// TestSignedComparisons covers jl and jg (S 15), which use a signed 16-bit
// comparison. S 2.2.1 warns that this makes them unsafe for comparing
// addresses: 0x8000 is a large address but a negative number.
func TestSignedComparisons(t *testing.T) {
	tests := []struct {
		name           string
		a, b           int16
		wantJL, wantJG bool
	}{
		{name: "less", a: 1, b: 2, wantJL: true},
		{name: "greater", a: 2, b: 1, wantJG: true},
		{name: "equal", a: 2, b: 2},
		{name: "negative is less than positive", a: -1, b: 1, wantJL: true},
		{name: "the most negative value", a: -32768, b: 32767, wantJL: true},
		{name: "a high address compares as negative", a: -32768, b: 1, wantJL: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := largeOp(unsigned(tt.a)), largeOp(unsigned(tt.b))
			if got := branchTaken(t, 0x02, a, b); got != tt.wantJL {
				t.Errorf("jl(%d, %d) branched = %t, want %t", tt.a, tt.b, got, tt.wantJL)
			}
			if got := branchTaken(t, 0x03, a, b); got != tt.wantJG {
				t.Errorf("jg(%d, %d) branched = %t, want %t", tt.a, tt.b, got, tt.wantJG)
			}
		})
	}
}

// TestJE covers je (S 15), which is the only opcode whose operand count
// varies: it jumps if the first operand equals any of the subsequent ones, and
// takes between two and four operands. "je a" never jumps, and je with just
// one operand is not permitted.
func TestJE(t *testing.T) {
	tests := []struct {
		name string
		ops  []opnd
		want bool
	}{
		{name: "two operands, equal", ops: []opnd{largeOp(5), largeOp(5)}, want: true},
		{name: "two operands, unequal", ops: []opnd{largeOp(5), largeOp(6)}},
		{name: "three operands, matches the second", ops: []opnd{largeOp(5), largeOp(9), largeOp(5)}, want: true},
		{name: "three operands, no match", ops: []opnd{largeOp(5), largeOp(9), largeOp(8)}},
		{name: "four operands, matches the last", ops: []opnd{largeOp(5), largeOp(1), largeOp(2), largeOp(5)}, want: true},
		{name: "four operands, no match", ops: []opnd{largeOp(5), largeOp(1), largeOp(2), largeOp(3)}},
		{name: "compares whole words, not signed values", ops: []opnd{largeOp(0xffff), largeOp(0xffff)}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := branchTaken(t, 0x01, tt.ops...); got != tt.want {
				t.Errorf("je branched = %t, want %t", got, tt.want)
			}
		})
	}

	t.Run("one operand is not permitted", func(t *testing.T) {
		code := join(encodeVar(count2OP, 0x01, largeOp(5)), branch2(true, 20))
		m := newTestMachine(t, code...)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
	})
}

// TestIncChkAndDecChk covers inc_chk and dec_chk (S 15). Both adjust the
// variable their first operand names and then compare it with the second,
// signed in both cases: a variable holding -1 increments to 0, and a variable
// holding 0 decrements to -1.
func TestIncChkAndDecChk(t *testing.T) {
	const (
		numberDecChk = 0x04
		numberIncChk = 0x05
	)

	tests := []struct {
		name       string
		number     uint8
		start      int16
		compare    int16
		wantValue  int16
		wantBranch bool
	}{
		{name: "inc_chk branches when now greater", number: numberIncChk, start: 4, compare: 4, wantValue: 5, wantBranch: true},
		{name: "inc_chk does not branch when now equal", number: numberIncChk, start: 3, compare: 4, wantValue: 4},
		{name: "inc_chk on a negative value", number: numberIncChk, start: -1, compare: -1, wantValue: 0, wantBranch: true},
		{name: "inc_chk compares signed", number: numberIncChk, start: -5, compare: 1, wantValue: -4},
		{name: "inc_chk wraps at 32767", number: numberIncChk, start: 32767, compare: 0, wantValue: -32768},
		{name: "dec_chk branches when now less", number: numberDecChk, start: 4, compare: 4, wantValue: 3, wantBranch: true},
		{name: "dec_chk does not branch when now equal", number: numberDecChk, start: 5, compare: 4, wantValue: 4},
		{name: "dec_chk on zero gives -1", number: numberDecChk, start: 0, compare: 0, wantValue: -1, wantBranch: true},
		{name: "dec_chk compares signed", number: numberDecChk, start: 1, compare: -5, wantValue: 0},
		{name: "dec_chk wraps at -32768", number: numberDecChk, start: -32768, compare: 0, wantValue: 32767},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := join(
				encodeVar(count2OP, tt.number, smallOp(globalFirst), largeOp(unsigned(tt.compare))),
				branch2(true, 20),
			)
			m := newTestMachine(t, code...)
			if err := m.writeGlobal(globalFirst, unsigned(tt.start)); err != nil {
				t.Fatalf("writeGlobal() error = %v", err)
			}

			if got := execBranch(t, m, code); got != tt.wantBranch {
				t.Errorf("branched = %t, want %t", got, tt.wantBranch)
			}
			got, _ := m.readGlobal(globalFirst)
			if signed(got) != tt.wantValue {
				t.Errorf("global $10 = %d, want %d", signed(got), tt.wantValue)
			}
		})
	}
}

// TestStoreOpcode covers store (S 15), whose first operand names the variable
// to write. The reference is indirect, so storing to the stack pointer
// overwrites the top item rather than pushing a new one (S 6.3.4).
func TestStoreOpcode(t *testing.T) {
	t.Run("into a global", func(t *testing.T) {
		code := encodeVar(count2OP, 0x0d, smallOp(globalFirst), largeOp(0xbeef))
		m := newTestMachine(t, code...)
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != 0xbeef {
			t.Errorf("global $10 = 0x%04x, want 0xbeef", got)
		}
	})

	t.Run("into the stack pointer writes in place", func(t *testing.T) {
		code := encodeVar(count2OP, 0x0d, smallOp(variableStack), largeOp(0xbeef))
		m := newTestMachine(t, code...)
		if err := m.push(0x1111); err != nil {
			t.Fatalf("push() error = %v", err)
		}
		mustStep(t, m)
		if got := m.stackDepth(); got != 1 {
			t.Fatalf("stackDepth() = %d, want 1 (S 6.3.4)", got)
		}
		if got, _ := m.pop(); got != 0xbeef {
			t.Errorf("top of stack = 0x%04x, want 0xbeef", got)
		}
	})
}

// TestLoadWAndLoadB covers loadw and loadb (S 15): the word at array + 2*index
// and the byte at array + index, which must lie in static or dynamic memory.
func TestLoadWAndLoadB(t *testing.T) {
	const table = machineScratch

	build := func(number uint8, base, index uint16) *Machine {
		code := append(encodeVar(count2OP, number, largeOp(base), largeOp(index)), globalFirst)
		m := newTestMachine(t, code...)
		for i := uint32(0); i < 8; i += 2 {
			if err := m.mem.writeWord(table+i, uint16(0x1000+i)); err != nil {
				t.Fatalf("writeWord() error = %v", err)
			}
		}
		return m
	}

	t.Run("loadw indexes by words", func(t *testing.T) {
		for index := uint16(0); index < 4; index++ {
			m := build(0x0f, table, index)
			mustStep(t, m)
			want := uint16(0x1000 + 2*index)
			if got, _ := m.readGlobal(globalFirst); got != want {
				t.Errorf("loadw(0x%04x, %d) = 0x%04x, want 0x%04x", table, index, got, want)
			}
		}
	})

	t.Run("loadb indexes by bytes", func(t *testing.T) {
		m := build(0x10, table, 1)
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != 0x00 {
			t.Errorf("loadb(0x%04x, 1) = 0x%04x, want 0x0000, the low byte of 0x1000", table, got)
		}
		m = build(0x10, table, 2)
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != 0x10 {
			t.Errorf("loadb(0x%04x, 2) = 0x%04x, want 0x0010, the high byte of 0x1002", table, got)
		}
	})

	t.Run("reading past the end of the story is refused", func(t *testing.T) {
		m := build(0x0f, machineStaticEnd-2, 8)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrMemoryAccess)
	})

	t.Run("an index that leaves the address space is refused", func(t *testing.T) {
		m := build(0x0f, 0xfffe, 0xffff)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrMemoryAccess)
	})
}
