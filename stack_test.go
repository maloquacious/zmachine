package zmachine

import (
	"errors"
	"testing"
)

// TestStackPushPopOrder checks the basic contract of S 6.3: writing to
// variable $00 pushes and reading pulls, so values come back in the reverse of
// the order they went in.
func TestStackPushPopOrder(t *testing.T) {
	m := newTestMachine(t)

	values := []uint16{1, 0x8000, 0xffff, 0}
	for _, v := range values {
		if err := m.push(v); err != nil {
			t.Fatalf("push(0x%04x) error = %v", v, err)
		}
	}
	if got := m.stackDepth(); got != len(values) {
		t.Fatalf("stackDepth() = %d, want %d", got, len(values))
	}
	for i := len(values) - 1; i >= 0; i-- {
		got, err := m.pop()
		if err != nil {
			t.Fatalf("pop() error = %v", err)
		}
		if got != values[i] {
			t.Errorf("pop() = 0x%04x, want 0x%04x", got, values[i])
		}
	}
	if got := m.stackDepth(); got != 0 {
		t.Errorf("stackDepth() = %d, want 0", got)
	}
}

// TestStackUnderflow covers S 6.3.1: it is illegal to pull values from the
// stack unless values have first been pushed on. An empty stack is a fault in
// the story, so it must be an error and never a panic.
func TestStackUnderflow(t *testing.T) {
	m := newTestMachine(t)

	if _, err := m.pop(); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("pop() on an empty stack error = %v, want one wrapping ErrExecutionFault", err)
	}
	if _, err := m.peek(); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("peek() on an empty stack error = %v, want one wrapping ErrExecutionFault", err)
	}
	if err := m.replaceTop(1); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("replaceTop() on an empty stack error = %v, want one wrapping ErrExecutionFault", err)
	}
}

// TestStackOverflowIsBounded checks that a story pushing without limit is
// stopped rather than allowed to exhaust the host's memory. S 6.3.3 sets the
// minimum stack at 1024 words and advises a larger one; whatever the size, it
// must be finite.
func TestStackOverflowIsBounded(t *testing.T) {
	m := newTestMachine(t)
	m.maxStack = 8

	for i := 0; i < m.maxStack; i++ {
		if err := m.push(uint16(i)); err != nil {
			t.Fatalf("push %d error = %v, want nil", i, err)
		}
	}
	err := m.push(0)
	if !errors.Is(err, ErrExecutionFault) {
		t.Errorf("push past the limit error = %v, want one wrapping ErrExecutionFault", err)
	}
	if got := len(m.stack); got != m.maxStack {
		t.Errorf("stack grew to %d entries past the limit of %d", got, m.maxStack)
	}
}

// TestStackIsPrivateToEachRoutine covers S 6.3.1 and S 6.3.2: a routine starts
// with an empty stack and cannot reach the values its caller pushed, and
// whatever it pushes is thrown away when it returns.
func TestStackIsPrivateToEachRoutine(t *testing.T) {
	m := newTestMachine(t)

	if err := m.push(0xcafe); err != nil {
		t.Fatalf("push() error = %v", err)
	}

	// Enter a routine by hand: what matters here is the watermark the frame
	// records, not how the frame came to be pushed.
	m.frames = append(m.frames, frame{stackBase: len(m.stack)})

	if got := m.stackDepth(); got != 0 {
		t.Errorf("stackDepth() in the callee = %d, want 0 (S 6.3.1)", got)
	}
	if _, err := m.pop(); err == nil {
		t.Errorf("pop() in the callee returned the caller's value (S 6.3.1)")
	}
	for i := 0; i < 3; i++ {
		if err := m.push(uint16(i)); err != nil {
			t.Fatalf("push() error = %v", err)
		}
	}

	if err := m.returnFromRoutine(0); err != nil {
		t.Fatalf("returnFromRoutine() error = %v", err)
	}
	if got := m.stackDepth(); got != 1 {
		t.Fatalf("stackDepth() after return = %d, want 1 (S 6.3.2)", got)
	}
	if got, _ := m.pop(); got != 0xcafe {
		t.Errorf("pop() after return = 0x%04x, want 0xcafe", got)
	}
}
