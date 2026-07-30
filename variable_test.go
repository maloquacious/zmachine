package zmachine

import (
	"errors"
	"testing"
)

// TestVariableZeroIsTheStack covers S 6.3: variable number $00 is the stack
// pointer, so writing to it pushes and reading from it pulls.
func TestVariableZeroIsTheStack(t *testing.T) {
	m := newTestMachine(t)

	if err := m.writeVariable(variableStack, 0x1234); err != nil {
		t.Fatalf("writeVariable($00) error = %v", err)
	}
	if err := m.writeVariable(variableStack, 0x5678); err != nil {
		t.Fatalf("writeVariable($00) error = %v", err)
	}
	if got := m.stackDepth(); got != 2 {
		t.Fatalf("stackDepth() = %d, want 2: writing variable $00 must push", got)
	}
	if got, _ := m.readVariable(variableStack); got != 0x5678 {
		t.Errorf("readVariable($00) = 0x%04x, want 0x5678", got)
	}
	if got := m.stackDepth(); got != 1 {
		t.Errorf("stackDepth() = %d, want 1: reading variable $00 must pop", got)
	}
}

// TestIndirectVariableReferencesUseTheStackInPlace covers S 6.3.4: in the
// seven opcodes that take indirect variable references, naming the stack
// pointer reads or writes the top item in place instead of pushing or popping
// it. Getting this wrong silently corrupts the stack of any story using
// "store sp" or "inc sp".
func TestIndirectVariableReferencesUseTheStackInPlace(t *testing.T) {
	m := newTestMachine(t)

	if err := m.push(0x0011); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	if err := m.push(0x0022); err != nil {
		t.Fatalf("push() error = %v", err)
	}

	got, err := m.readVariableIndirect(variableStack)
	if err != nil {
		t.Fatalf("readVariableIndirect($00) error = %v", err)
	}
	if got != 0x0022 {
		t.Errorf("readVariableIndirect($00) = 0x%04x, want 0x0022", got)
	}
	if depth := m.stackDepth(); depth != 2 {
		t.Errorf("stackDepth() = %d, want 2: an indirect read must not pop", depth)
	}

	if err := m.writeVariableIndirect(variableStack, 0x0033); err != nil {
		t.Fatalf("writeVariableIndirect($00) error = %v", err)
	}
	if depth := m.stackDepth(); depth != 2 {
		t.Errorf("stackDepth() = %d, want 2: an indirect write must not push", depth)
	}
	if got, _ := m.pop(); got != 0x0033 {
		t.Errorf("top of stack = 0x%04x, want 0x0033", got)
	}
	if got, _ := m.pop(); got != 0x0011 {
		t.Errorf("next on stack = 0x%04x, want 0x0011: the entry below was disturbed", got)
	}
}

// TestLocalVariablesAreBoundedByTheRoutine covers S 5.2 and S 6.2: a routine
// has between 0 and 15 locals, and only the ones it declared are variables at
// all. Reading beyond them is a fault in the story, not a zero.
func TestLocalVariablesAreBoundedByTheRoutine(t *testing.T) {
	m := newTestMachine(t)
	m.frames = append(m.frames, frame{numLocals: 3, locals: [maxLocalsV3]uint16{0x0a, 0x0b, 0x0c}})

	for number := uint8(localFirst); number <= 3; number++ {
		got, err := m.readVariable(number)
		if err != nil {
			t.Fatalf("readVariable($%02x) error = %v", number, err)
		}
		if want := uint16(0x09 + number); got != want {
			t.Errorf("readVariable($%02x) = 0x%04x, want 0x%04x", number, got, want)
		}
	}

	for _, number := range []uint8{4, 0x0f} {
		if _, err := m.readVariable(number); !errors.Is(err, ErrExecutionFault) {
			t.Errorf("readVariable($%02x) error = %v, want one wrapping ErrExecutionFault", number, err)
		}
		if err := m.writeVariable(number, 1); !errors.Is(err, ErrExecutionFault) {
			t.Errorf("writeVariable($%02x) error = %v, want one wrapping ErrExecutionFault", number, err)
		}
	}

	// S 5.5: the initial execution environment has no local variables at all.
	m.frames = m.frames[:1]
	if _, err := m.readVariable(localFirst); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("readVariable($01) in the initial environment error = %v, want a fault", err)
	}
}

// TestGlobalVariables covers S 6.2: variables $10 to $ff live in a table of
// 240 words in dynamic memory, at the address in the header.
func TestGlobalVariables(t *testing.T) {
	m := newTestMachine(t)

	tests := []struct {
		number uint8
		value  uint16
	}{
		{number: 0x10, value: 0x0001}, // the first global
		{number: 0x11, value: 0xffff},
		{number: 0x80, value: 0x8000},
		{number: 0xff, value: 0xabcd}, // the last global
	}
	for _, tt := range tests {
		if err := m.writeVariable(tt.number, tt.value); err != nil {
			t.Fatalf("writeVariable($%02x) error = %v", tt.number, err)
		}
	}
	for _, tt := range tests {
		got, err := m.readVariable(tt.number)
		if err != nil {
			t.Fatalf("readVariable($%02x) error = %v", tt.number, err)
		}
		if got != tt.value {
			t.Errorf("readVariable($%02x) = 0x%04x, want 0x%04x", tt.number, got, tt.value)
		}
	}

	// The table is at the header address and holds words, so the last global
	// must be the 240th word of it.
	last := m.story.globals + 239*wordAddressScale
	if got, _ := m.mem.readWord(last); got != 0xabcd {
		t.Errorf("word at 0x%04x = 0x%04x, want 0xabcd: global $ff is not the 240th word", last, got)
	}
}
