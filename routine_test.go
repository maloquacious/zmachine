package zmachine

import (
	"errors"
	"testing"
)

// routineAt lays out a routine header at addr: the count byte, then one word
// per local giving its initial value (S 5.2, S 5.2.1). Version 3 reads those
// words; Version 5 and later would not have them at all.
func routineAt(addr uint32, initial ...uint16) (uint32, []byte) {
	out := []byte{uint8(len(initial))}
	for _, v := range initial {
		out = append(out, uint8(v>>8), uint8(v))
	}
	return addr, out
}

// packed returns the packed address of a routine at addr. In Versions 1 to 3
// the byte address is twice the packed address (S 1.2.3).
func packed(addr uint32) uint16 { return uint16(addr / packedScaleV3) }

// TestCallTakesInitialLocalsFromTheRoutineHeader covers S 5.2.1 and S 6.4.4:
// in Versions 1 to 4 a routine's locals are created with the initial values
// stored in its header, and the arguments are then written over them. From
// Version 5 the header carries no such words and every local starts at zero,
// so reading them is specifically Version 3 behaviour.
func TestCallTakesInitialLocalsFromTheRoutineHeader(t *testing.T) {
	const routine = 0x0600
	addr, header := routineAt(routine, 0x1111, 0x2222, 0x3333)
	m := newStory(t).at(addr, header...).machine()

	if err := m.callRoutine(packed(routine), []uint16{0x00ff}, 0x0500, 0, false); err != nil {
		t.Fatalf("callRoutine() error = %v", err)
	}

	// S 6.4.4: argument 1 goes into local 1; locals with no argument keep the
	// value from the header.
	want := []uint16{0x00ff, 0x2222, 0x3333}
	for i, w := range want {
		got, err := m.readVariable(uint8(localFirst + i))
		if err != nil {
			t.Fatalf("readVariable($%02x) error = %v", localFirst+i, err)
		}
		if got != w {
			t.Errorf("local $%02x = 0x%04x, want 0x%04x", localFirst+i, got, w)
		}
	}
	// S 5.3: execution begins at the byte after the routine header.
	if wantPC := uint32(routine + 1 + 3*2); m.pc != wantPC {
		t.Errorf("pc = 0x%04x, want 0x%04x", m.pc, wantPC)
	}
}

// TestCallArgumentCounts covers S 6.4.4.1: it is legal for there to be more
// arguments than local variables, in which case the spare ones are thrown
// away, or for there to be fewer.
func TestCallArgumentCounts(t *testing.T) {
	tests := []struct {
		name    string
		initial []uint16
		args    []uint16
		want    []uint16
	}{
		{
			name:    "fewer arguments than locals leaves the rest as declared",
			initial: []uint16{0xaaaa, 0xbbbb, 0xcccc},
			args:    []uint16{1},
			want:    []uint16{1, 0xbbbb, 0xcccc},
		},
		{
			name:    "as many arguments as locals overwrites them all",
			initial: []uint16{0xaaaa, 0xbbbb},
			args:    []uint16{1, 2},
			want:    []uint16{1, 2},
		},
		{
			name:    "spare arguments are thrown away",
			initial: []uint16{0xaaaa},
			args:    []uint16{1, 2, 3},
			want:    []uint16{1},
		},
		{
			name:    "a routine with no locals accepts arguments and discards them",
			initial: nil,
			args:    []uint16{1, 2, 3},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const routine = 0x0600
			addr, header := routineAt(routine, tt.initial...)
			m := newStory(t).at(addr, header...).machine()

			if err := m.callRoutine(packed(routine), tt.args, 0x0500, 0, false); err != nil {
				t.Fatalf("callRoutine() error = %v", err)
			}

			f := m.frames[len(m.frames)-1]
			if int(f.numLocals) != len(tt.want) {
				t.Fatalf("numLocals = %d, want %d", f.numLocals, len(tt.want))
			}
			if int(f.argCount) != len(tt.args) {
				t.Errorf("argCount = %d, want %d", f.argCount, len(tt.args))
			}
			for i, w := range tt.want {
				if got := f.locals[i]; got != w {
					t.Errorf("local $%02x = 0x%04x, want 0x%04x", localFirst+i, got, w)
				}
			}
		})
	}
}

// TestCallToPackedAddressZero covers S 6.4.3: a call to packed address 0 is
// legal, does nothing, and returns false. No frame is created and the program
// counter does not move, so a story using it as a null callback keeps running.
func TestCallToPackedAddressZero(t *testing.T) {
	// call 0 -> local nothing; the store variable is a global so the result
	// can be read back.
	code := append(encodeVar(countVAR, 0x00, largeOp(0)), globalFirst)
	m := newTestMachine(t, code...)

	depth, pc := len(m.frames), m.pc
	mustStep(t, m)

	if len(m.frames) != depth {
		t.Errorf("call depth = %d, want %d: a call to address 0 must not create a frame", len(m.frames), depth)
	}
	if m.pc != pc+uint32(len(code)) {
		t.Errorf("pc = 0x%04x, want 0x%04x: a call to address 0 must not move execution", m.pc, pc+uint32(len(code)))
	}
	if got, _ := m.readGlobal(globalFirst); got != 0 {
		t.Errorf("stored result = 0x%04x, want 0x0000 (false)", got)
	}
}

// TestReturnRestoresTheCallerFrame covers S 6.3.2, S 6.4.2 and S 6.4.5: a
// return throws away the values the routine pushed, restores the caller's
// locals and program counter, and writes the return value where the call asked
// for it. A result stored to variable $00 lands on the caller's stack, because
// the callee's entries are gone by then.
func TestReturnRestoresTheCallerFrame(t *testing.T) {
	const routine = 0x0600
	addr, header := routineAt(routine, 0x1111)
	m := newStory(t).at(addr, header...).machine()

	if err := m.push(0xcafe); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	if err := m.callRoutine(packed(routine), nil, 0x0500, variableStack, true); err != nil {
		t.Fatalf("callRoutine() error = %v", err)
	}
	// Leave rubbish on the callee's stack; it must not survive the return.
	for i := 0; i < 4; i++ {
		if err := m.push(0xdead); err != nil {
			t.Fatalf("push() error = %v", err)
		}
	}

	if err := m.returnFromRoutine(0x0042); err != nil {
		t.Fatalf("returnFromRoutine() error = %v", err)
	}

	if m.pc != 0x0500 {
		t.Errorf("pc = 0x%04x, want 0x0500", m.pc)
	}
	if len(m.frames) != 1 {
		t.Fatalf("call depth = %d frames, want 1", len(m.frames))
	}
	if got := m.stackDepth(); got != 2 {
		t.Fatalf("stackDepth() = %d, want 2: the caller's value plus the returned one", got)
	}
	if got, _ := m.pop(); got != 0x0042 {
		t.Errorf("top of stack = 0x%04x, want the return value 0x0042", got)
	}
	if got, _ := m.pop(); got != 0xcafe {
		t.Errorf("next on stack = 0x%04x, want the caller's 0xcafe", got)
	}
}

// TestReturnFromInitialEnvironment covers S 5.5: the Z-machine starts in an
// environment from which a return is illegal. A story that wants to stop must
// use quit.
func TestReturnFromInitialEnvironment(t *testing.T) {
	m := newTestMachine(t)
	if err := m.returnFromRoutine(1); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("returnFromRoutine() at the base error = %v, want one wrapping ErrExecutionFault", err)
	}
}

// TestCallRejectsMalformedRoutines checks that a packed address which does not
// name a routine is refused rather than trusted. S 5.2 allows between 0 and 15
// locals, so a larger count means the address names something else, and a
// routine header running off the end of the story is not a routine either.
func TestCallRejectsMalformedRoutines(t *testing.T) {
	t.Run("too many locals", func(t *testing.T) {
		const routine = 0x0600
		m := newStory(t).at(routine, 16).machine()
		err := m.callRoutine(packed(routine), nil, 0x0500, 0, false)
		if !errors.Is(err, ErrExecutionFault) {
			t.Errorf("callRoutine() error = %v, want one wrapping ErrExecutionFault", err)
		}
	})

	t.Run("header runs off the end of the story", func(t *testing.T) {
		// The last byte of the story claims fifteen locals, whose initial
		// values would lie beyond the image.
		last := uint32(machineStaticEnd - 1)
		m := newStory(t).at(last, 15).machine()
		err := m.callRoutine(packed(last-1)+1, nil, 0x0500, 0, false)
		if !errors.Is(err, ErrMemoryAccess) {
			t.Errorf("callRoutine() error = %v, want one wrapping ErrMemoryAccess", err)
		}
	})

	t.Run("call depth is bounded", func(t *testing.T) {
		// S 6.3.3 guarantees a depth of at least 90 calls; whatever the bound,
		// it must be finite so that a story recursing forever is stopped.
		const routine = 0x0600
		addr, header := routineAt(routine)
		m := newStory(t).at(addr, header...).machine()
		m.maxDepth = 4

		var err error
		for i := 0; i < 100 && err == nil; i++ {
			err = m.callRoutine(packed(routine), nil, 0x0500, 0, false)
		}
		if !errors.Is(err, ErrExecutionFault) {
			t.Errorf("recursing without limit error = %v, want one wrapping ErrExecutionFault", err)
		}
		if len(m.frames) > m.maxDepth {
			t.Errorf("call depth grew to %d frames past the limit of %d", len(m.frames), m.maxDepth)
		}
	})
}
