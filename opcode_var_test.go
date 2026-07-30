package zmachine

import "testing"

// TestCallOpcode covers call (S 15), the only call instruction in Version 3.
// It calls the routine with 0, 1, 2 or 3 arguments and stores the return
// value; the routine returns to the instruction after the call.
func TestCallOpcode(t *testing.T) {
	const routine = 0x0600
	addr, header := routineAt(routine, 0x1111, 0x2222)

	code := append(encodeVar(countVAR, 0x00,
		largeOp(packed(routine)), largeOp(0x00aa), largeOp(0x00bb)), globalFirst)
	m := newStory(t).at(addr, header...).code(code...).machine()

	returnPC := m.pc + uint32(len(code))
	mustStep(t, m)

	if len(m.frames) != 2 {
		t.Fatalf("call depth = %d frames, want 2", len(m.frames))
	}
	f := m.frames[1]
	if f.returnPC != returnPC {
		t.Errorf("return address = 0x%04x, want 0x%04x", f.returnPC, returnPC)
	}
	if !f.hasStore || f.store != globalFirst {
		t.Errorf("store variable = $%02x (present %t), want $%02x", f.store, f.hasStore, globalFirst)
	}
	if f.argCount != 2 {
		t.Errorf("argCount = %d, want 2", f.argCount)
	}
	if f.locals[0] != 0x00aa || f.locals[1] != 0x00bb {
		t.Errorf("locals = 0x%04x, 0x%04x, want 0x00aa, 0x00bb", f.locals[0], f.locals[1])
	}
	if wantPC := uint32(routine + 1 + 2*2); m.pc != wantPC {
		t.Errorf("pc = 0x%04x, want 0x%04x (S 5.3)", m.pc, wantPC)
	}

	t.Run("call with no routine operand is a fault", func(t *testing.T) {
		code := append(encodeVar(countVAR, 0x00), globalFirst)
		m := newTestMachine(t, code...)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
	})
}

// TestStoreWAndStoreB covers storew and storeb (S 15): the word at
// array + 2*index and the byte at array + index, both of which must lie in
// dynamic memory. A write to static or high memory is refused (S 1.1.2), which
// is the guarantee spec S 14 rests on.
func TestStoreWAndStoreB(t *testing.T) {
	const (
		numberStoreW = 0x01
		numberStoreB = 0x02
	)

	t.Run("storew writes a word", func(t *testing.T) {
		code := encodeVar(countVAR, numberStoreW,
			largeOp(machineScratch), largeOp(3), largeOp(0xbeef))
		m := newTestMachine(t, code...)
		mustStep(t, m)
		got, err := m.mem.readWord(machineScratch + 6)
		if err != nil {
			t.Fatalf("readWord() error = %v", err)
		}
		if got != 0xbeef {
			t.Errorf("word at 0x%04x = 0x%04x, want 0xbeef", machineScratch+6, got)
		}
	})

	t.Run("storeb writes the low byte", func(t *testing.T) {
		code := encodeVar(countVAR, numberStoreB,
			largeOp(machineScratch), largeOp(5), largeOp(0xbeef))
		m := newTestMachine(t, code...)
		mustStep(t, m)
		got, err := m.mem.readByte(machineScratch + 5)
		if err != nil {
			t.Fatalf("readByte() error = %v", err)
		}
		if got != 0xef {
			t.Errorf("byte at 0x%04x = 0x%02x, want 0xef", machineScratch+5, got)
		}
	})

	tests := []struct {
		name   string
		number uint8
		base   uint16
		index  uint16
	}{
		{name: "storew into static memory", number: numberStoreW, base: testFirstStaticAddr, index: 0},
		{name: "storeb into static memory", number: numberStoreB, base: testFirstStaticAddr, index: 0},
		{name: "storew straddling the top of dynamic memory", number: numberStoreW, base: testFirstStaticAddr - 1, index: 0},
		{name: "storew past the end of the story", number: numberStoreW, base: machineStaticEnd, index: 0},
		{name: "storeb with an index that leaves dynamic memory", number: numberStoreB, base: machineScratch, index: 0x1000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := encodeVar(countVAR, tt.number, largeOp(tt.base), largeOp(tt.index), largeOp(1))
			m := newTestMachine(t, code...)
			assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrMemoryAccess)
		})
	}
}

// TestPushAndPull covers push and pull (S 15). In Version 3 pull is not a
// store instruction: the destination is an operand, and the reference is
// indirect (S 6.3.4).
func TestPushAndPull(t *testing.T) {
	code := join(
		encodeVar(countVAR, 0x08, largeOp(0x1234)),      // push 0x1234
		encodeVar(countVAR, 0x09, smallOp(globalFirst)), // pull -> $10
	)
	m := newTestMachine(t, code...)

	mustStep(t, m)
	if got := m.stackDepth(); got != 1 {
		t.Fatalf("stackDepth() after push = %d, want 1", got)
	}
	mustStep(t, m)
	if got := m.stackDepth(); got != 0 {
		t.Errorf("stackDepth() after pull = %d, want 0", got)
	}
	if got, _ := m.readGlobal(globalFirst); got != 0x1234 {
		t.Errorf("global $10 = 0x%04x, want 0x1234", got)
	}

	t.Run("pull on an empty stack is a fault", func(t *testing.T) {
		m := newTestMachine(t, encodeVar(countVAR, 0x09, smallOp(globalFirst))...)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
	})
}

// TestPrintCharAndPrintNum covers print_char and print_num (S 15).
// print_char takes a ZSCII output code (S 3.8) and print_num prints a signed
// number in decimal.
func TestPrintCharAndPrintNum(t *testing.T) {
	t.Run("print_char", func(t *testing.T) {
		tests := []struct {
			code uint16
			want string
		}{
			{code: 'A', want: "A"},
			{code: zsciiSpace, want: " "},
			{code: zsciiNewline, want: "\n"},
			{code: 155, want: "ä"},      // the first of the extra characters (S 3.8.5)
			{code: zsciiNull, want: ""}, // defined for output but with no effect (S 3.8.2.1)
			{code: 7, want: ""},         // not defined for output at all
			{code: 1023, want: ""},      // the largest ZSCII code, undefined
		}
		for _, tt := range tests {
			m := newTestMachine(t, encodeVar(countVAR, 0x05, largeOp(tt.code))...)
			mustStep(t, m)
			if got := string(m.out.screen); got != tt.want {
				t.Errorf("print_char(%d) = %q, want %q", tt.code, got, tt.want)
			}
		}
	})

	t.Run("print_num", func(t *testing.T) {
		tests := []struct {
			value int16
			want  string
		}{
			{value: 0, want: "0"},
			{value: 42, want: "42"},
			{value: -42, want: "-42"},
			{value: 32767, want: "32767"},
			{value: -32768, want: "-32768"},
		}
		for _, tt := range tests {
			m := newTestMachine(t, encodeVar(countVAR, 0x06, largeOp(unsigned(tt.value)))...)
			mustStep(t, m)
			if got := string(m.out.screen); got != tt.want {
				t.Errorf("print_num(%d) = %q, want %q", tt.value, got, tt.want)
			}
		}
	})
}

// TestRandomOpcode covers random (S 15) and the generator states of S 2.4. A
// positive range gives a uniformly random number between 1 and the range; a
// negative range seeds the generator and returns 0; a range of zero reseeds it
// unpredictably and returns 0. Interpreters that treat a range of zero as
// illegal are wrong to.
func TestRandomOpcode(t *testing.T) {
	t.Run("a positive range stays within 1 to n", func(t *testing.T) {
		// The same instruction is executed repeatedly by putting the program
		// counter back, which is cheaper than rebuilding the story and proves
		// the draws come from one generator.
		code := append(encodeVar(countVAR, 0x07, largeOp(6)), globalFirst)
		m := newTestMachine(t, code...)
		seen := make(map[uint16]bool)
		for i := 0; i < 500; i++ {
			m.pc = machineCodeBase
			mustStep(t, m)
			got, _ := m.readGlobal(globalFirst)
			if got < 1 || got > 6 {
				t.Fatalf("random(6) = %d, want 1 to 6", got)
			}
			seen[got] = true
		}
		if len(seen) != 6 {
			t.Errorf("random(6) produced %d of the 6 possible values in 500 draws", len(seen))
		}
	})

	t.Run("a negative range seeds the generator and returns 0", func(t *testing.T) {
		draw := func() []uint16 {
			code := append(encodeVar(countVAR, 0x07, largeOp(unsigned(-1000))), globalFirst)
			m := newTestMachine(t, code...)
			mustStep(t, m)
			if got, _ := m.readGlobal(globalFirst); got != 0 {
				t.Fatalf("random(-1000) = %d, want 0", got)
			}
			if !m.predictable {
				t.Errorf("the generator is not in its predictable state after being seeded (S 2.4.2)")
			}
			out := make([]uint16, 5)
			for i := range out {
				out[i] = m.randomInRange(10000)
			}
			return out
		}
		first, second := draw(), draw()
		for i := range first {
			if first[i] != second[i] {
				t.Fatalf("draw %d after the same seed = %d and %d, want equal (S 2.4.2)", i, first[i], second[i])
			}
		}
	})

	t.Run("a range of zero reseeds unpredictably and returns 0", func(t *testing.T) {
		code := append(encodeVar(countVAR, 0x07, largeOp(0)), globalFirst)
		m := newTestMachine(t, code...)
		mustStep(t, m)
		if got, _ := m.readGlobal(globalFirst); got != 0 {
			t.Errorf("random(0) = %d, want 0", got)
		}
		if m.predictable {
			t.Errorf("the generator is still predictable after random(0) (S 2.4)")
		}
	})
}

// TestWindowOpcodes covers split_window and set_window (S 15, S 8.6.1). Both
// are legal in Version 3 only because the interpreter sets bit 5 of Flags 1 to
// say the upper window exists (S 8.6.1.2).
func TestWindowOpcodes(t *testing.T) {
	// split_window 3; set_window 1; print; set_window 0; print.
	upper, lower := "status", "narrative"
	code := join(
		encodeVar(countVAR, 0x0a, smallOp(3)),
		encodeVar(countVAR, 0x0b, smallOp(windowUpper)),
		encodeShort(0x02), encodeText(t, upper),
		encodeVar(countVAR, 0x0b, smallOp(windowLower)),
		encodeShort(0x02), encodeText(t, lower),
	)
	m := newTestMachine(t, code...)
	for i := 0; i < 5; i++ {
		mustStep(t, m)
	}

	if m.out.upperHeight != 3 {
		t.Errorf("upper window height = %d, want 3", m.out.upperHeight)
	}
	// The two windows are captured separately: the upper one overlays fixed
	// screen positions and would corrupt the narrative if merged into it.
	if got := string(m.out.screen); got != lower {
		t.Errorf("lower window = %q, want %q", got, lower)
	}
	if got := string(m.out.upper); got != upper {
		t.Errorf("upper window = %q, want %q", got, upper)
	}

	t.Run("a split clears the upper window", func(t *testing.T) {
		// S 8.6.1.1.2: when a screen split takes place in Version 3, the upper
		// window is cleared.
		m := newTestMachine(t, encodeVar(countVAR, 0x0a, smallOp(1))...)
		m.out.upper = append(m.out.upper, "stale"...)
		mustStep(t, m)
		if len(m.out.upper) != 0 {
			t.Errorf("upper window = %q, want it cleared", string(m.out.upper))
		}
	})

	t.Run("a window Version 3 does not have is refused", func(t *testing.T) {
		m := newTestMachine(t, encodeVar(countVAR, 0x0b, smallOp(2))...)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
	})
}

// TestInputStreamAndSoundEffectAreIgnored checks that the two Version 3
// instructions this engine cannot honour do not end a turn. Neither an input
// file (S 7.6.5) nor an audio device exists here, and S 15 asks interpreters
// not to halt over sound_effect in particular.
func TestInputStreamAndSoundEffectAreIgnored(t *testing.T) {
	code := join(
		encodeVar(countVAR, 0x14, smallOp(1)),             // input_stream 1
		encodeVar(countVAR, 0x15, smallOp(3), smallOp(2)), // sound_effect 3 2
		encodeVar(countVAR, 0x14, smallOp(0)),             // input_stream 0
	)
	m := newTestMachine(t, code...)
	for i := 0; i < 3; i++ {
		mustStep(t, m)
	}
	if len(m.out.screen) != 0 {
		t.Errorf("output = %q, want none", string(m.out.screen))
	}
}
