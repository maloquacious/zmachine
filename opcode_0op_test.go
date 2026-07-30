package zmachine

import (
	"testing"
)

// inRoutine puts the machine inside a routine with the given number of locals,
// so that instructions which return have somewhere to return to. It answers
// the address the routine returns to.
func inRoutine(t *testing.T, m *Machine, numLocals uint8, store uint8) uint32 {
	t.Helper()
	const returnPC = 0x0700
	m.frames = append(m.frames, frame{
		returnPC:  returnPC,
		numLocals: numLocals,
		stackBase: len(m.stack),
		store:     store,
		hasStore:  true,
	})
	return returnPC
}

// TestReturnOpcodes covers rtrue, rfalse and ret_popped (S 15). Returning
// false means returning 0 and returning true means returning 1 (S 6.4.5).
func TestReturnOpcodes(t *testing.T) {
	tests := []struct {
		name    string
		number  uint8
		pushed  []uint16
		want    uint16
		wantErr bool
	}{
		{name: "rtrue returns 1", number: 0x00, want: 1},
		{name: "rfalse returns 0", number: 0x01, want: 0},
		{name: "ret_popped returns the top of the stack", number: 0x08, pushed: []uint16{0x0009, 0x1234}, want: 0x1234},
		{name: "ret_popped on an empty stack is a fault", number: 0x08, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMachine(t, encodeShort(tt.number)...)
			returnPC := inRoutine(t, m, 0, globalFirst)
			for _, v := range tt.pushed {
				if err := m.push(v); err != nil {
					t.Fatalf("push() error = %v", err)
				}
			}

			if tt.wantErr {
				assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
				return
			}
			mustStep(t, m)

			if m.pc != returnPC {
				t.Errorf("pc = 0x%04x, want 0x%04x", m.pc, returnPC)
			}
			if got, _ := m.readGlobal(globalFirst); got != tt.want {
				t.Errorf("stored return value = 0x%04x, want 0x%04x", got, tt.want)
			}
		})
	}
}

// TestPrintAndPrintRet covers print and print_ret (S 15). Both carry the
// string inline after the opcode (S 4.8); print_ret then prints a new-line and
// returns true.
func TestPrintAndPrintRet(t *testing.T) {
	const text = "opening the small mailbox reveals a leaflet"

	t.Run("print", func(t *testing.T) {
		m := newTestMachine(t, join(encodeShort(0x02), encodeText(t, text))...)
		mustStep(t, m)
		if got := string(m.out.screen); got != text {
			t.Errorf("output = %q, want %q", got, text)
		}
	})

	t.Run("print_ret", func(t *testing.T) {
		m := newTestMachine(t, join(encodeShort(0x03), encodeText(t, text))...)
		returnPC := inRoutine(t, m, 0, globalFirst)
		mustStep(t, m)

		if got, want := string(m.out.screen), text+"\n"; got != want {
			t.Errorf("output = %q, want %q", got, want)
		}
		if m.pc != returnPC {
			t.Errorf("pc = 0x%04x, want 0x%04x", m.pc, returnPC)
		}
		if got, _ := m.readGlobal(globalFirst); got != 1 {
			t.Errorf("stored return value = %d, want 1 (true)", got)
		}
	})
}

// TestNewLineAndNop covers new_line and nop (S 15).
func TestNewLineAndNop(t *testing.T) {
	m := newTestMachine(t, join(encodeShort(0x0b), encodeShort(0x04))...)
	mustStep(t, m)
	if got := string(m.out.screen); got != "\n" {
		t.Errorf("new_line output = %q, want %q", got, "\n")
	}
	mustStep(t, m)
	if got := string(m.out.screen); got != "\n" {
		t.Errorf("nop changed the output to %q", got)
	}
}

// TestPop covers pop (S 15): throw away the top item on the stack.
func TestPop(t *testing.T) {
	m := newTestMachine(t, encodeShort(0x09)...)
	if err := m.push(1); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	if err := m.push(2); err != nil {
		t.Fatalf("push() error = %v", err)
	}
	mustStep(t, m)
	if got := m.stackDepth(); got != 1 {
		t.Fatalf("stackDepth() = %d, want 1", got)
	}
	if got, _ := m.pop(); got != 1 {
		t.Errorf("top of stack = %d, want 1", got)
	}

	// An empty stack is a fault, not a silent no-op (S 6.3.1).
	m = newTestMachine(t, encodeShort(0x09)...)
	assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
}

// TestQuit covers quit (S 15): the story exits immediately. The host is told
// the story halted; the process is never touched (spec S 27).
func TestQuit(t *testing.T) {
	m := newTestMachine(t, encodeShort(0x0a)...)
	if got := mustStep(t, m); got != controlHalt {
		t.Errorf("quit returned control %d, want controlHalt", got)
	}
}

// TestSaveAndRestoreBranchFalse covers the Version 3 forms of save and restore
// (S 15), which branch on success. This engine snapshots on the host's behalf
// at request boundaries and has no filesystem, so an in-story save cannot
// succeed and reports failure by not branching - which is exactly what a
// Version 3 story is required to cope with. For restore, S 15 notes that in
// Version 3 the branch is never actually made in any case.
func TestSaveAndRestoreBranchFalse(t *testing.T) {
	for _, number := range []uint8{0x05, 0x06} {
		t.Run(makeOpcode(count0OP, number).name(), func(t *testing.T) {
			// Polarity true: the branch is taken only on success, so it must
			// not be taken.
			code := join(encodeShort(number), branch2(true, 20))
			m := newTestMachine(t, code...)
			if execBranch(t, m, code) {
				t.Errorf("branched on success, but the operation failed")
			}

			// Polarity false: the branch is taken on failure, so it must be.
			code = join(encodeShort(number), branch2(false, 20))
			m = newTestMachine(t, code...)
			if !execBranch(t, m, code) {
				t.Errorf("did not branch on failure")
			}
		})
	}
}

// TestVerify covers verify (S 15): branch if the sum of the bytes of the file
// from $0040 onwards, modulo $10000, agrees with the checksum in the header.
func TestVerify(t *testing.T) {
	code := join(encodeShort(0x0d), branch2(true, 20))

	t.Run("matching checksum branches", func(t *testing.T) {
		m := newStory(t).code(code...).checksum().machine()
		if !execBranch(t, m, code) {
			t.Errorf("verify did not branch on a story whose checksum matches")
		}
	})

	t.Run("mismatched checksum does not branch", func(t *testing.T) {
		// The code is written after the checksum is fixed, so the recorded
		// value no longer describes the image.
		b := newStory(t).checksum()
		m := b.code(code...).machine()
		if execBranch(t, m, code) {
			t.Errorf("verify branched on a story whose checksum does not match")
		}
	})
}

// TestRestartOpcode covers restart (S 15): the whole state comes back from the
// original story file. In particular, changing the program start address in
// the header before a restart does not restart from the new address.
func TestRestartOpcode(t *testing.T) {
	m := newTestMachine(t, encodeShort(0x07)...)
	if err := m.writeGlobal(globalFirst, 0x1234); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if err := m.mem.writeWord(hdrInitialPC, 0x0800); err != nil {
		t.Fatalf("writeWord(initial pc) error = %v", err)
	}

	mustStep(t, m)

	if m.pc != machineCodeBase {
		t.Errorf("pc = 0x%04x, want 0x%04x: restart used the altered header value", m.pc, machineCodeBase)
	}
	if got, _ := m.readGlobal(globalFirst); got != 0 {
		t.Errorf("global $10 = 0x%04x, want 0x0000", got)
	}
}

// TestShowStatus covers show_status (S 15), which in Version 3 displays and
// updates the status line at once rather than waiting for the next keyboard
// read (S 8.2.4). The status line is reported to the host on the Result rather
// than drawn (spec S 13).
func TestShowStatus(t *testing.T) {
	m := newTestMachine(t, encodeShort(0x0c)...)
	mustWriteGlobals(t, m, 12, 250, 88)

	if m.status.Available {
		t.Errorf("the status line is available before it has ever been updated (S 8.2.4)")
	}
	mustStep(t, m)

	want := StatusLine{Available: true, Object: 12, Score: 250, Turns: 88}
	if m.status != want {
		t.Errorf("status = %+v, want %+v", m.status, want)
	}
}
