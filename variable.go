package zmachine

import "fmt"

// Variables (S 6.2, S 6.3).
//
// A variable number selects one of three things:
//
//	$00        the evaluation stack: reading pops, writing pushes (S 6.3)
//	$01 - $0f  the local variables of the current routine (S 5.2)
//	$10 - $ff  the 240 global variables, held in dynamic memory (S 6.2)
//
// The seven instructions that take an indirect variable reference - inc, dec,
// inc_chk, dec_chk, load, store and pull - treat variable $00 differently:
// they read or write the top of the stack in place rather than popping or
// pushing (S 6.3.4). Those instructions use the Indirect helpers below.

const (
	// variableStack is the stack pointer, variable number $00 (S 6.3).
	variableStack = 0x00
	// localFirst and localLast bound the local variable numbers (S 6.2).
	localFirst = 0x01
	localLast  = 0x0f
	// globalFirst is the first global variable number (S 6.2).
	globalFirst = 0x10
)

// readVariable returns the contents of a variable. Reading variable $00 pops
// the evaluation stack (S 6.3).
func (m *Machine) readVariable(number uint8) (uint16, error) {
	switch {
	case number == variableStack:
		return m.pop()
	case number <= localLast:
		return m.readLocal(number)
	default:
		return m.readGlobal(number)
	}
}

// writeVariable stores a value in a variable. Writing variable $00 pushes onto
// the evaluation stack (S 6.3).
func (m *Machine) writeVariable(number uint8, value uint16) error {
	switch {
	case number == variableStack:
		return m.push(value)
	case number <= localLast:
		return m.writeLocal(number, value)
	default:
		return m.writeGlobal(number, value)
	}
}

// readVariableIndirect returns the contents of a variable named by an operand
// of one of the seven indirect instructions. An indirect reference to the
// stack pointer reads the top item in place instead of popping it (S 6.3.4).
func (m *Machine) readVariableIndirect(number uint8) (uint16, error) {
	if number == variableStack {
		return m.peek()
	}
	return m.readVariable(number)
}

// writeVariableIndirect stores a value in a variable named by an operand of
// one of the seven indirect instructions. An indirect reference to the stack
// pointer overwrites the top item in place instead of pushing (S 6.3.4).
func (m *Machine) writeVariableIndirect(number uint8, value uint16) error {
	if number == variableStack {
		return m.replaceTop(value)
	}
	return m.writeVariable(number, value)
}

// readLocal returns local variable number, which must be between $01 and $0f
// and must be one the current routine declared (S 5.2).
func (m *Machine) readLocal(number uint8) (uint16, error) {
	f := &m.frames[len(m.frames)-1]
	if number < localFirst || number > f.numLocals {
		return 0, m.localRangeError(number, f)
	}
	return f.locals[number-localFirst], nil
}

// writeLocal stores a value in local variable number.
func (m *Machine) writeLocal(number uint8, value uint16) error {
	f := &m.frames[len(m.frames)-1]
	if number < localFirst || number > f.numLocals {
		return m.localRangeError(number, f)
	}
	f.locals[number-localFirst] = value
	return nil
}

// localRangeError reports a local variable the current routine does not have.
func (m *Machine) localRangeError(number uint8, f *frame) error {
	if f.numLocals == 0 {
		return fmt.Errorf("zmachine: local variable $%02x: the current routine has no local variables: %w",
			number, ErrExecutionFault)
	}
	return fmt.Errorf("zmachine: local variable $%02x: the current routine has only %d ($01 to $%02x): %w",
		number, f.numLocals, f.numLocals, ErrExecutionFault)
}

// globalAddress returns the byte address of global variable number. The table
// holds 240 words starting at the address in the header (S 6.2); LoadStory has
// already checked that all of it lies in dynamic memory, so any number from
// $10 to $ff addresses a word that exists.
func (m *Machine) globalAddress(number uint8) uint32 {
	return m.story.globals + uint32(number-globalFirst)*wordAddressScale
}

// readGlobal returns the contents of global variable number.
func (m *Machine) readGlobal(number uint8) (uint16, error) {
	if number < globalFirst {
		return 0, fmt.Errorf("zmachine: variable $%02x is not a global variable: %w", number, ErrExecutionFault)
	}
	return m.mem.readWord(m.globalAddress(number))
}

// writeGlobal stores a value in global variable number.
func (m *Machine) writeGlobal(number uint8, value uint16) error {
	if number < globalFirst {
		return fmt.Errorf("zmachine: variable $%02x is not a global variable: %w", number, ErrExecutionFault)
	}
	return m.mem.writeWord(m.globalAddress(number), value)
}
