package zmachine

import (
	"errors"
	"testing"
)

// TestJZ covers jz (S 15): jump if the operand is zero.
func TestJZ(t *testing.T) {
	tests := []struct {
		value uint16
		want  bool
	}{
		{value: 0, want: true},
		{value: 1},
		{value: 0xffff},
		{value: 0x8000},
	}
	for _, tt := range tests {
		code := join(encodeShort(0x00, largeOp(tt.value)), branch2(true, 20))
		m := newTestMachine(t, code...)
		if got := execBranch(t, m, code); got != tt.want {
			t.Errorf("jz(0x%04x) branched = %t, want %t", tt.value, got, tt.want)
		}
	}
}

// TestJumpTakesASignedOffset covers jump (S 15). It is not a branch
// instruction: its operand is an ordinary 2-byte signed offset, and the
// destination is the address after the instruction plus the offset minus two -
// the same formula branches use (S 4.7.2). Reading the operand as unsigned
// would make every backward jump run off the end of the story.
func TestJumpTakesASignedOffset(t *testing.T) {
	tests := []struct {
		name   string
		offset int16
	}{
		{name: "forwards", offset: 100},
		{name: "backwards", offset: -100},
		{name: "offset of two lands on the next instruction", offset: 2},
		{name: "offset of zero lands two bytes earlier", offset: 0},
		{name: "the smallest step backwards", offset: -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := encodeShort(0x0c, largeOp(unsigned(tt.offset)))
			m := newTestMachine(t, code...)
			next := m.pc + uint32(len(code))
			mustStep(t, m)

			want := uint32(int64(next) + int64(tt.offset) - branchBias)
			if m.pc != want {
				t.Errorf("jump %+d from 0x%04x: pc = 0x%04x, want 0x%04x", tt.offset, next, m.pc, want)
			}
		})
	}

	t.Run("a jump leaving the address space is refused", func(t *testing.T) {
		code := encodeShort(0x0c, largeOp(unsigned(-32768)))
		m := newTestMachine(t, code...)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrMemoryAccess)
	})
}

// TestNot covers not (S 15): bitwise NOT, all 16 bits reversed. In Versions 3
// and 4 it is a 1OP instruction; from Version 5 it moves into the variable set
// to make room for call_1n.
func TestNot(t *testing.T) {
	tests := []struct {
		value, want uint16
	}{
		{value: 0x0000, want: 0xffff},
		{value: 0xffff, want: 0x0000},
		{value: 0xf0f0, want: 0x0f0f},
		{value: 0x0001, want: 0xfffe},
	}
	for _, tt := range tests {
		code := append(encodeShort(0x0f, largeOp(tt.value)), globalFirst)
		m := newTestMachine(t, code...)
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != tt.want {
			t.Errorf("not(0x%04x) = 0x%04x, want 0x%04x", tt.value, got, tt.want)
		}
	}
}

// TestIncAndDec covers inc and dec (S 15). Both are signed, so -1 increments
// to 0 and 0 decrements to -1, and both take an indirect variable reference
// (S 6.3.4).
func TestIncAndDec(t *testing.T) {
	tests := []struct {
		name   string
		number uint8
		start  int16
		want   int16
	}{
		{name: "inc", number: 0x05, start: 41, want: 42},
		{name: "inc of -1 gives 0", number: 0x05, start: -1, want: 0},
		{name: "inc wraps at 32767", number: 0x05, start: 32767, want: -32768},
		{name: "dec", number: 0x06, start: 43, want: 42},
		{name: "dec of 0 gives -1", number: 0x06, start: 0, want: -1},
		{name: "dec wraps at -32768", number: 0x06, start: -32768, want: 32767},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMachine(t, encodeShort(tt.number, smallOp(globalFirst))...)
			if err := m.writeGlobal(globalFirst, unsigned(tt.start)); err != nil {
				t.Fatalf("writeGlobal() error = %v", err)
			}
			mustStep(t, m)
			got, _ := m.readGlobal(globalFirst)
			if signed(got) != tt.want {
				t.Errorf("%s(%d) = %d, want %d", tt.name, tt.start, signed(got), tt.want)
			}
		})
	}

	t.Run("inc of the stack pointer works in place", func(t *testing.T) {
		m := newTestMachine(t, encodeShort(0x05, smallOp(variableStack))...)
		if err := m.push(7); err != nil {
			t.Fatalf("push() error = %v", err)
		}
		mustStep(t, m)
		if got := m.stackDepth(); got != 1 {
			t.Fatalf("stackDepth() = %d, want 1 (S 6.3.4)", got)
		}
		if got, _ := m.pop(); got != 8 {
			t.Errorf("top of stack = %d, want 8", got)
		}
	})
}

// TestLoad covers load (S 15): the value of the variable the operand names is
// stored in the result. The reference is indirect, so loading the stack
// pointer reads the top item without popping it (S 6.3.4).
func TestLoad(t *testing.T) {
	t.Run("from a global", func(t *testing.T) {
		code := append(encodeShort(0x0e, smallOp(globalFirst+1)), globalFirst)
		m := newTestMachine(t, code...)
		if err := m.writeGlobal(globalFirst+1, 0x1234); err != nil {
			t.Fatalf("writeGlobal() error = %v", err)
		}
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != 0x1234 {
			t.Errorf("load = 0x%04x, want 0x1234", got)
		}
	})

	t.Run("from the stack pointer does not pop", func(t *testing.T) {
		code := append(encodeShort(0x0e, smallOp(variableStack)), globalFirst)
		m := newTestMachine(t, code...)
		if err := m.push(0x4321); err != nil {
			t.Fatalf("push() error = %v", err)
		}
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != 0x4321 {
			t.Errorf("load = 0x%04x, want 0x4321", got)
		}
		if got := m.stackDepth(); got != 1 {
			t.Errorf("stackDepth() = %d, want 1 (S 6.3.4)", got)
		}
	})
}

// TestRet covers ret (S 15): return from the current routine with the given
// value.
func TestRet(t *testing.T) {
	const routine = 0x0600
	addr, header := routineAt(routine)
	m := newStory(t).at(addr, header...).code(encodeShort(0x0b, largeOp(0x0777))...).machine()

	// Enter the routine, but leave the program counter on the ret instruction
	// so that step executes it.
	if err := m.callRoutine(packed(routine), nil, 0x0500, globalFirst, true); err != nil {
		t.Fatalf("callRoutine() error = %v", err)
	}
	m.pc = machineCodeBase
	mustStep(t, m)

	if m.pc != 0x0500 {
		t.Errorf("pc = 0x%04x, want 0x0500", m.pc)
	}
	if got, _ := m.readGlobal(globalFirst); got != 0x0777 {
		t.Errorf("stored return value = 0x%04x, want 0x0777", got)
	}
}

// TestPrintAddrAndPrintPaddr covers print_addr and print_paddr (S 15). The
// first names a byte address in dynamic or static memory and the second a
// packed address, which in Versions 1 to 3 is half the byte address (S 1.2.3).
func TestPrintAddrAndPrintPaddr(t *testing.T) {
	const (
		text   = "you are likely to be eaten"
		strAdr = 0x0600
	)
	encoded := encodeText(t, text)

	t.Run("print_addr", func(t *testing.T) {
		code := encodeShort(0x07, largeOp(strAdr))
		m := newStory(t).at(strAdr, encoded...).code(code...).machine()
		mustStep(t, m)
		if got := string(m.out.screen); got != text {
			t.Errorf("output = %q, want %q", got, text)
		}
	})

	t.Run("print_paddr", func(t *testing.T) {
		code := encodeShort(0x0d, largeOp(packed(strAdr)))
		m := newStory(t).at(strAdr, encoded...).code(code...).machine()
		mustStep(t, m)
		if got := string(m.out.screen); got != text {
			t.Errorf("output = %q, want %q", got, text)
		}
	})

	t.Run("an unterminated string is an error, not a hang", func(t *testing.T) {
		// A run of words with the end bit never set, at the very end of the
		// story: readZChars must stop at the end of memory.
		code := encodeShort(0x07, largeOp(machineStaticEnd-4))
		m := newStory(t).at(machineStaticEnd-4, 0, 0, 0, 0).code(code...).machine()
		err := stepErr(t, m)
		if !errors.Is(err, ErrInvalidText) {
			t.Errorf("error = %v, want one wrapping ErrInvalidText", err)
		}
	})
}
