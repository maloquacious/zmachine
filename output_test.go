package zmachine

import (
	"errors"
	"testing"
)

// outputStream assembles an output_stream instruction (S 15).
func outputStream(number int16, table ...uint16) []byte {
	ops := []opnd{largeOp(unsigned(number))}
	for _, t := range table {
		ops = append(ops, largeOp(t))
	}
	return encodeVar(countVAR, 0x13, ops...)
}

// TestMemoryStreamCapturesText covers output stream 3 (S 7.1.2.1): while it is
// selected, text goes to a table in dynamic memory instead of the screen, and
// when it is deselected the first word of the table holds the number of
// characters printed with the characters themselves from table+2 onwards.
func TestMemoryStreamCapturesText(t *testing.T) {
	const (
		table = machineScratch
		text  = "grue"
	)
	code := join(
		outputStream(streamMemory, table),
		encodeShort(0x02), encodeText(t, text),
		outputStream(-streamMemory),
		encodeShort(0x02), encodeText(t, "seen"),
	)
	m := newTestMachine(t, code...)
	for i := 0; i < 4; i++ {
		mustStep(t, m)
	}

	// S 7.1.2.2: while stream 3 is selected, no text is sent to any other
	// stream, so only the text printed afterwards reaches the screen.
	if got := string(m.out.screen); got != "seen" {
		t.Errorf("screen = %q, want %q: text leaked past stream 3", got, "seen")
	}

	count, err := m.mem.readWord(table)
	if err != nil {
		t.Fatalf("readWord() error = %v", err)
	}
	if int(count) != len(text) {
		t.Fatalf("character count = %d, want %d", count, len(text))
	}
	for i := 0; i < len(text); i++ {
		got, err := m.mem.readByte(table + memoryStreamHeader + uint32(i))
		if err != nil {
			t.Fatalf("readByte() error = %v", err)
		}
		if got != text[i] {
			t.Errorf("byte %d = 0x%02x, want 0x%02x", i, got, text[i])
		}
	}
}

// TestMemoryStreamWritesNewlinesAsZSCII13 covers S 7.1.2.2.1: newlines are
// written to output stream 3 as ZSCII 13, never as the host's line ending.
func TestMemoryStreamWritesNewlinesAsZSCII13(t *testing.T) {
	const table = machineScratch
	code := join(
		outputStream(streamMemory, table),
		encodeShort(0x0b), // new_line
		outputStream(-streamMemory),
	)
	m := newTestMachine(t, code...)
	for i := 0; i < 3; i++ {
		mustStep(t, m)
	}

	if count, _ := m.mem.readWord(table); count != 1 {
		t.Fatalf("character count = %d, want 1", count)
	}
	got, _ := m.mem.readByte(table + memoryStreamHeader)
	if got != zsciiNewline {
		t.Errorf("byte = %d, want %d (ZSCII 13)", got, zsciiNewline)
	}
}

// TestMemoryStreamNesting covers S 7.1.2.1.1: stream 3 may be selected while
// it is already on, in which case the previous table is resumed when the new
// one is finished. The nesting may reach sixteen; a seventeenth selection is
// an error.
func TestMemoryStreamNesting(t *testing.T) {
	const (
		outer = machineScratch
		inner = machineScratch + 0x20
	)
	code := join(
		outputStream(streamMemory, outer),
		encodeShort(0x02), encodeText(t, "ab"),
		outputStream(streamMemory, inner),
		encodeShort(0x02), encodeText(t, "xyz"),
		outputStream(-streamMemory),
		encodeShort(0x02), encodeText(t, "cd"),
		outputStream(-streamMemory),
	)
	m := newTestMachine(t, code...)
	for i := 0; i < 7; i++ {
		mustStep(t, m)
	}

	if count, _ := m.mem.readWord(inner); count != 3 {
		t.Errorf("inner table count = %d, want 3", count)
	}
	if count, _ := m.mem.readWord(outer); count != 4 {
		t.Errorf("outer table count = %d, want 4: the outer table must be resumed", count)
	}
	if len(m.out.screen) != 0 {
		t.Errorf("screen = %q, want none", string(m.out.screen))
	}

	t.Run("a seventeenth selection is an error", func(t *testing.T) {
		m := newTestMachine(t, outputStream(streamMemory, machineScratch)...)
		for i := 0; i < memoryStreamDepth; i++ {
			m.pc = machineCodeBase
			mustStep(t, m)
		}
		m.pc = machineCodeBase
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
	})
}

// TestMemoryStreamRejectsTablesOutsideDynamicMemory checks that a table the
// engine cannot write to is refused when the story names it. S 7.1.2.1 says
// the interpreter performs no overflow checking, but that cannot extend to
// writing outside dynamic memory (S 1.1.2).
func TestMemoryStreamRejectsTablesOutsideDynamicMemory(t *testing.T) {
	tests := []struct {
		name  string
		table uint16
	}{
		{name: "static memory", table: testFirstStaticAddr},
		{name: "past the end of the story", table: machineStaticEnd},
		{name: "count word straddling the top of dynamic memory", table: testFirstStaticAddr - 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestMachine(t, outputStream(streamMemory, tt.table)...)
			assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrMemoryAccess)
		})
	}

	t.Run("a table that overruns dynamic memory while printing", func(t *testing.T) {
		// The table starts two bytes below the top of dynamic memory, so the
		// count word fits but the first character does not.
		table := uint16(testFirstStaticAddr - 2)
		code := join(
			outputStream(streamMemory, table),
			encodeShort(0x02), encodeText(t, "a"),
		)
		m := newTestMachine(t, code...)
		mustStep(t, m)
		printPC := m.pc
		assertExecutionError(t, stepErr(t, m), printPC, ErrMemoryAccess)
	})
}

// TestOutputStreamSelection covers the stream numbers of S 15: zero does
// nothing, a positive number selects and a negative one deselects. Streams 2
// and 4 write to a printer or a file and are not provided here (S 7.6.5), so
// they are recorded and dropped rather than ending the turn.
func TestOutputStreamSelection(t *testing.T) {
	t.Run("stream 1 can be deselected and reselected", func(t *testing.T) {
		code := join(
			outputStream(-streamScreen),
			encodeShort(0x02), encodeText(t, "hidden"),
			outputStream(streamScreen),
			encodeShort(0x02), encodeText(t, "shown"),
		)
		m := newTestMachine(t, code...)
		for i := 0; i < 4; i++ {
			mustStep(t, m)
		}
		if got := string(m.out.screen); got != "shown" {
			t.Errorf("screen = %q, want %q", got, "shown")
		}
	})

	t.Run("stream 0 does nothing", func(t *testing.T) {
		m := newTestMachine(t, outputStream(0)...)
		mustStep(t, m)
		if !m.out.stream1 || len(m.out.tables) != 0 {
			t.Errorf("output_stream 0 changed the stream state")
		}
	})

	t.Run("unavailable streams do not end the turn", func(t *testing.T) {
		for _, number := range []int16{streamTranscript, -streamTranscript, streamCommands, -streamCommands, 9, -9} {
			m := newTestMachine(t, outputStream(number)...)
			mustStep(t, m)
		}
	})

	t.Run("selecting the transcript keeps the header bit clear", func(t *testing.T) {
		// S 11.1.2: bit 0 of Flags 2 must always hold the true state of
		// transcription, and transcription is never on here.
		m := newTestMachine(t, outputStream(streamTranscript)...)
		flags2, _ := m.mem.readWord(hdrFlags2)
		if err := m.mem.writeWord(hdrFlags2, flags2|flags2Transcript); err != nil {
			t.Fatalf("writeWord() error = %v", err)
		}
		mustStep(t, m)
		got, _ := m.mem.readWord(hdrFlags2)
		if got&flags2Transcript != 0 {
			t.Errorf("Flags 2 = 0x%04x, want bit 0 clear", got)
		}
	})

	t.Run("selecting stream 3 without a table is a fault", func(t *testing.T) {
		m := newTestMachine(t, outputStream(streamMemory)...)
		assertExecutionError(t, stepErr(t, m), machineCodeBase, ErrExecutionFault)
	})

	t.Run("deselecting stream 3 when it is not selected does nothing", func(t *testing.T) {
		m := newTestMachine(t, outputStream(-streamMemory)...)
		mustStep(t, m)
	})
}

// TestPrintPreservesWhitespace covers spec S 12: output preserves the
// whitespace the story produced, exactly, with nothing inserted. In
// particular, no word wrapping happens: S 7.2 buffering exists to fit text to
// a physical screen, and there is none here.
func TestPrintPreservesWhitespace(t *testing.T) {
	const text = "a  double space and a very long line that no interpreter here will ever fold at any column at all"
	m := newTestMachine(t, join(encodeShort(0x02), encodeText(t, text))...)
	mustStep(t, m)
	if got := string(m.out.screen); got != text {
		t.Errorf("output = %q, want %q", got, text)
	}
}

// TestMemoryStreamSubstitutesUnrepresentableCharacters covers S 7.5.3: a
// character that cannot be converted to ZSCII is written to stream 3 as a
// question mark.
func TestMemoryStreamSubstitutesUnrepresentableCharacters(t *testing.T) {
	const table = machineScratch
	m := newTestMachine(t)
	if err := m.selectMemoryStream(table); err != nil {
		t.Fatalf("selectMemoryStream() error = %v", err)
	}
	// U+4E2D is not in ZSCII, not even among the extra characters of S 3.8.5.
	if err := m.printText("a中b"); err != nil {
		t.Fatalf("printText() error = %v", err)
	}
	if err := m.deselectMemoryStream(); err != nil {
		t.Fatalf("deselectMemoryStream() error = %v", err)
	}

	count, _ := m.mem.readWord(table)
	if count != 3 {
		t.Fatalf("character count = %d, want 3", count)
	}
	want := []byte{'a', zsciiSubstitute, 'b'}
	for i, w := range want {
		got, _ := m.mem.readByte(table + memoryStreamHeader + uint32(i))
		if got != w {
			t.Errorf("byte %d = 0x%02x, want 0x%02x", i, got, w)
		}
	}
}

// TestOutputIsNeverWrittenAnywhere is a guard on the promise of spec S 12 that
// the engine writes story output nowhere: the only way to see it is through
// the Result. It checks the one path that could escape - an error while
// printing - leaves the captured text intact and reports the fault instead.
func TestOutputIsNeverWrittenAnywhere(t *testing.T) {
	m := newTestMachine(t)
	if err := m.selectMemoryStream(testFirstStaticAddr); err == nil {
		t.Fatalf("selectMemoryStream() into static memory error = nil, want an error")
	} else if !errors.Is(err, ErrMemoryAccess) {
		t.Errorf("error = %v, want one wrapping ErrMemoryAccess", err)
	}
	if len(m.out.tables) != 0 {
		t.Errorf("a refused selection left %d table(s) on the stream stack", len(m.out.tables))
	}
}

// TestMemoryStreamKeepsZSCIICodes covers the difference between the two ways
// text reaches output stream 3. A decoded string is converted back to ZSCII,
// substituting a question mark for anything ZSCII cannot hold (S 7.5.3), while
// print_char supplies a ZSCII code directly and it goes into the table as
// itself. A code with no output meaning prints nothing on any stream, so it
// must not become a question mark either.
func TestMemoryStreamKeepsZSCIICodes(t *testing.T) {
	const table = machineScratch
	m := newTestMachine(t)
	if err := m.selectMemoryStream(table); err != nil {
		t.Fatalf("selectMemoryStream() error = %v", err)
	}
	// ZSCII 0 has no effect on any output stream (S 3.8.2.1); 7 is not defined
	// for output at all; 155 is the first of the extra characters (S 3.8.5).
	for _, code := range []uint16{'a', zsciiNull, 7, 155, zsciiNewline} {
		if err := m.printZSCII(code); err != nil {
			t.Fatalf("printZSCII(%d) error = %v", code, err)
		}
	}
	if err := m.deselectMemoryStream(); err != nil {
		t.Fatalf("deselectMemoryStream() error = %v", err)
	}

	want := []byte{'a', 155, zsciiNewline}
	count, _ := m.mem.readWord(table)
	if int(count) != len(want) {
		t.Fatalf("character count = %d, want %d", count, len(want))
	}
	for i, w := range want {
		got, _ := m.mem.readByte(table + memoryStreamHeader + uint32(i))
		if got != w {
			t.Errorf("byte %d = %d, want %d", i, got, w)
		}
	}
}
