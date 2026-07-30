package zmachine

import "fmt"

// The evaluation stack (S 6.3).
//
// The stack holds 2-byte words. Each routine owns the entries pushed since it
// was called: S 6.3.1 says the stack is empty at the start of a routine, and
// S 6.3.2 that whatever it pushed is thrown away when it returns. Both rules
// are enforced by the watermark each frame records, so a routine can neither
// read nor discard a value belonging to its caller.
//
// Underflow and overflow are faults in the story, not in the engine, so they
// are reported as errors classified as ErrExecutionFault.

// stackBase returns the index in the stack at which the current routine's
// entries begin. Nothing below it belongs to the current routine.
func (m *Machine) stackBase() int { return m.frames[len(m.frames)-1].stackBase }

// stackDepth returns the number of entries the current routine has pushed.
func (m *Machine) stackDepth() int { return len(m.stack) - m.stackBase() }

// push puts a value on the evaluation stack.
func (m *Machine) push(value uint16) error {
	if len(m.stack) >= m.maxStack {
		return fmt.Errorf("zmachine: evaluation stack overflow at %d entries: %w",
			m.maxStack, ErrExecutionFault)
	}
	m.stack = append(m.stack, value)
	return nil
}

// pop takes the top value off the evaluation stack.
func (m *Machine) pop() (uint16, error) {
	if m.stackDepth() <= 0 {
		// S 6.3.1: it is illegal to pull values from the stack unless values
		// have first been pushed on during this routine.
		return 0, fmt.Errorf("zmachine: evaluation stack underflow: the current routine has pushed nothing: %w",
			ErrExecutionFault)
	}
	value := m.stack[len(m.stack)-1]
	m.stack = m.stack[:len(m.stack)-1]
	return value, nil
}

// peek reads the top value without removing it. It serves the indirect
// variable references of S 6.3.4, which read the stack in place.
func (m *Machine) peek() (uint16, error) {
	if m.stackDepth() <= 0 {
		return 0, fmt.Errorf("zmachine: evaluation stack underflow: the current routine has pushed nothing: %w",
			ErrExecutionFault)
	}
	return m.stack[len(m.stack)-1], nil
}

// replaceTop overwrites the top value in place, for the indirect variable
// references of S 6.3.4.
func (m *Machine) replaceTop(value uint16) error {
	if m.stackDepth() <= 0 {
		return fmt.Errorf("zmachine: evaluation stack underflow: the current routine has pushed nothing: %w",
			ErrExecutionFault)
	}
	m.stack[len(m.stack)-1] = value
	return nil
}
