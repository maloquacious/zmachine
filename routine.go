package zmachine

import (
	"fmt"
	"log/slog"
)

// Routine calls and returns (S 5, S 6.4, S 6.5).
//
// A routine begins with a byte giving the number of local variables it has,
// between 0 and 15 (S 5.2). In Versions 1 to 4 that byte is followed by one
// word per local giving its initial value (S 5.2.1); from Version 5 the
// initial values are all zero and the words are absent. This engine is Version
// 3, so the words are read.
//
// Execution then begins at the byte after the header (S 5.3).

// frame is one entry in the routine call chain.
type frame struct {
	// returnPC is the address execution resumes at when the routine returns.
	returnPC uint32

	// locals holds the routine's local variables. Only the first numLocals are
	// in use; the rest are not variables of this routine at all and reading
	// one is an error rather than a zero (S 5.2).
	locals    [maxLocalsV3]uint16
	numLocals uint8

	// argCount is the number of arguments the call supplied, which may be more
	// or fewer than the routine declared locals (S 6.4.4.1).
	argCount uint8

	// stackBase is the height of the evaluation stack when the routine was
	// entered. Everything above it belongs to this routine (S 6.3.1) and is
	// discarded when it returns (S 6.3.2).
	stackBase int

	// store is the variable the return value is written to, meaningful only
	// when hasStore is set. Every Version 3 call stores its result, but the
	// base frame of S 5.5 does not, and nor would the call_vn family of later
	// versions.
	store    uint8
	hasStore bool
}

// callRoutine enters the routine at the given packed address (S 6.4).
//
// packed is the packed address from the story, args are the arguments in
// order, returnPC is where execution resumes, and store names the variable the
// return value goes to.
//
// A call to packed address 0 is legal: nothing happens and the call returns
// false (S 6.4.3). It is the only packed address that may name no routine.
func (m *Machine) callRoutine(packed uint16, args []uint16, returnPC uint32, store uint8, hasStore bool) error {
	if packed == 0 {
		// S 6.4.3: no routine is entered, so the program counter does not
		// move and no frame is pushed; the result is simply false.
		if hasStore {
			return m.storeVariable(store, 0)
		}
		return nil
	}

	if len(m.frames) >= m.maxDepth {
		return fmt.Errorf("zmachine: routine call chain reached %d frames: %w", m.maxDepth, ErrExecutionFault)
	}

	addr := unpackAddress(packed)
	count, err := m.mem.readByte(addr)
	if err != nil {
		return fmt.Errorf("zmachine: routine at 0x%04x (packed 0x%04x): reading the local variable count: %w",
			addr, packed, err)
	}
	// S 5.2: a routine has between 0 and 15 local variables. A larger count
	// means the packed address does not name a routine at all, which S 6.4.3
	// makes illegal, so it is refused before any words are read.
	if count > maxLocalsV3 {
		return fmt.Errorf("zmachine: routine at 0x%04x (packed 0x%04x): declares %d local variables, more than the %d S 5.2 permits: %w",
			addr, packed, count, maxLocalsV3, ErrExecutionFault)
	}

	f := frame{
		returnPC:  returnPC,
		numLocals: count,
		argCount:  uint8(len(args)),
		stackBase: len(m.stack),
		store:     store,
		hasStore:  hasStore,
	}

	// S 5.2.1, S 6.4.4: in Versions 1 to 4 the locals are created with the
	// initial values stored in the routine header. This is the Version 3
	// behaviour and differs from Version 5 and later, where the header carries
	// no words and every local starts at zero.
	at := addr + 1
	for i := uint8(0); i < count; i++ {
		value, err := m.mem.readWord(at)
		if err != nil {
			return fmt.Errorf("zmachine: routine at 0x%04x: reading the initial value of local $%02x: %w",
				addr, i+1, err)
		}
		f.locals[i] = value
		at += wordAddressScale
	}

	// S 6.4.4: the arguments are then written over the initial values,
	// argument 1 into local 1 and so on. S 6.4.4.1: spare arguments are thrown
	// away, and locals with no argument keep their initial value.
	for i, arg := range args {
		if i >= int(count) {
			break
		}
		f.locals[i] = arg
	}

	m.frames = append(m.frames, f)
	// S 5.3: execution begins at the byte after the routine header.
	m.pc = at

	if m.tracing() {
		m.trace.called = true
	}
	m.logger.Debug("routine call",
		slog.Uint64("routine", uint64(addr)),
		slog.Int("args", len(args)),
		slog.Uint64("locals", uint64(count)),
		slog.Int("depth", len(m.frames)-1))
	return nil
}

// returnFromRoutine leaves the current routine with the given value (S 6.4.5).
//
// The routine's evaluation stack entries are discarded (S 6.3.2), the caller's
// frame becomes current again, and the value is written to the variable the
// call named. Because the frame is dropped first, a return value stored to
// variable $00 is pushed onto the caller's stack, which is what S 6.4.2
// describes.
func (m *Machine) returnFromRoutine(value uint16) error {
	if len(m.frames) <= 1 {
		// S 5.5: the Z-machine starts in an environment from which a return is
		// illegal; a story that wants to stop must use quit (S 15, quit).
		return fmt.Errorf("zmachine: return from the initial execution environment, which S 5.5 forbids: %w",
			ErrExecutionFault)
	}

	f := m.frames[len(m.frames)-1]
	m.frames = m.frames[:len(m.frames)-1]
	m.stack = m.stack[:f.stackBase]
	m.pc = f.returnPC

	if m.tracing() {
		m.trace.returned = true
		m.trace.returnValue = value
	}
	m.logger.Debug("routine return",
		slog.Uint64("value", uint64(value)),
		slog.Uint64("pc", uint64(m.pc)),
		slog.Int("depth", len(m.frames)-1))

	if f.hasStore {
		return m.storeVariable(f.store, value)
	}
	return nil
}

// storeVariable writes a value to a variable and records the write for
// tracing. Every result an instruction produces goes through it, so a trace
// sees each one exactly once.
func (m *Machine) storeVariable(number uint8, value uint16) error {
	if err := m.writeVariable(number, value); err != nil {
		return err
	}
	if m.tracing() {
		m.trace.stored = true
		m.trace.storeVariable = number
		m.trace.storeValue = value
	}
	return nil
}
