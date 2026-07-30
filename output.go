package zmachine

import (
	"fmt"
	"log/slog"
	"unicode/utf8"
)

// Output streams and the logical screen (S 7, S 8.6).
//
// Everything the story prints is captured, never written anywhere by the
// engine (spec S 12). What Version 3 offers and what this engine does with it:
//
//   - Stream 1, the screen (S 7.1.1). Captured. Text printed while the lower
//     window is selected becomes Result.Output and text printed while the
//     upper window is selected becomes Result.UpperWindow, because the upper
//     window is an overlay at fixed screen positions rather than part of the
//     narrative (S 8.6.1.1.1).
//
//   - Stream 2, the transcript (S 7.1.1). Not provided: it writes to a printer
//     or a file, and this engine has neither. S 7.6.5 permits an interpreter
//     to omit external file access. Selecting it does nothing except keep bit
//     0 of Flags 2 clear, which S 11.1.2 requires to reflect the true state of
//     transcription.
//
//   - Stream 3, a table in dynamic memory (S 7.1.2.1). Implemented in full,
//     including the nesting of S 7.1.2.1.1 and the rule that while it is
//     selected no text reaches any other stream (S 7.1.2.2).
//
//   - Stream 4, a script of the player's commands (S 7.1.2). Not provided, for
//     the same reason as stream 2.
//
// Word wrapping (S 7.2) is deliberately not implemented. Buffering exists so
// that a word does not straddle two lines of a physical screen; the host here
// has no screen width, and inserting newlines would corrupt the story's own
// whitespace, which spec S 12 requires be preserved exactly.

const (
	// windowLower and windowUpper are the two Version 3 windows. The lower
	// window is selected initially (S 8.6.1).
	windowLower = 0
	windowUpper = 1

	// The output stream numbers of S 7.1.
	streamScreen     = 1
	streamTranscript = 2
	streamMemory     = 3
	streamCommands   = 4

	// memoryStreamDepth is how deeply selections of stream 3 may nest. A
	// seventeenth selection is an error (S 7.1.2.1.1).
	memoryStreamDepth = 16

	// memoryStreamHeader is the size of the count word that begins a stream 3
	// table; the characters follow it (S 7.1.2.1).
	memoryStreamHeader = 2

	// zsciiSubstitute is printed to stream 3 in place of a character ZSCII
	// cannot represent (S 7.5.3).
	zsciiSubstitute = '?'
)

// output holds the output streams and the logical screen state.
type output struct {
	// screen collects text printed to the lower window and upper text printed
	// to the upper window, both through stream 1.
	screen []byte
	upper  []byte

	// window is the selected window, lower or upper (S 8.6.1).
	window uint8
	// upperHeight is the height of the upper window in lines (S 8.6.1.1).
	upperHeight uint16

	// stream1 reports whether the screen stream is selected. It is on at the
	// start of a game and a story may turn it off with output_stream -1.
	stream1 bool

	// tables is the stack of selected stream 3 tables, innermost last
	// (S 7.1.2.1.1).
	tables []memoryStream
}

// memoryStream is one selection of output stream 3.
type memoryStream struct {
	// table is the byte address of the table being written to.
	table uint32
	// count is the number of characters written so far, which is stored in the
	// table's first word when the stream is deselected (S 7.1.2.1).
	count uint16
}

// init puts the output state back to how it is at the start of a game: the
// screen selected, the lower window current, no upper window and no memory
// streams (S 7.3, S 8.6.1).
func (o *output) init() {
	o.screen = o.screen[:0]
	o.upper = o.upper[:0]
	o.window = windowLower
	o.upperHeight = 0
	o.stream1 = true
	o.tables = o.tables[:0]
}

// beginInvocation discards the text captured by an earlier call to Start or
// Run. Stream selections and the window state survive, because they are
// interpreter state the story set and not something a request boundary
// changes.
func (o *output) beginInvocation() {
	o.screen = o.screen[:0]
	o.upper = o.upper[:0]
}

// printText prints already decoded text (S 7.1).
func (m *Machine) printText(text string) error {
	if len(text) == 0 {
		return nil
	}

	// S 7.1.2.2: while stream 3 is selected, no text is sent to any other
	// stream, even though the others remain selected.
	if len(m.out.tables) != 0 {
		for _, r := range text {
			code, ok := zsciiFromRune(r)
			if !ok {
				// S 7.5.3: a character ZSCII cannot represent becomes a
				// question mark.
				code = zsciiSubstitute
			}
			if err := m.writeMemoryStream(code); err != nil {
				return err
			}
		}
		return nil
	}

	if !m.out.stream1 {
		return nil
	}
	if m.out.window == windowUpper {
		m.out.upper = append(m.out.upper, text...)
	} else {
		m.out.screen = append(m.out.screen, text...)
	}
	return nil
}

// printZSCII prints one ZSCII character (S 3.8). A code Version 3 does not
// define for output prints nothing, which keeps a story that prints a stretch
// of arbitrary memory from putting control codes into the captured output.
func (m *Machine) printZSCII(code uint16) error {
	// A code with no output meaning prints nothing on any stream, so it is
	// dropped before either stream is considered. That includes ZSCII 0, which
	// is defined for output but has no effect (S 3.8.2.1).
	r, ok := zsciiOutputRune(code)
	if !ok {
		return nil
	}

	if len(m.out.tables) != 0 {
		// Every ZSCII code with an output meaning is below 256, so it goes
		// into the table as itself rather than as its printed form.
		return m.writeMemoryStream(uint8(code))
	}
	if !m.out.stream1 {
		return nil
	}
	var buf [utf8.UTFMax]byte
	n := utf8.EncodeRune(buf[:], r)
	if m.out.window == windowUpper {
		m.out.upper = append(m.out.upper, buf[:n]...)
	} else {
		m.out.screen = append(m.out.screen, buf[:n]...)
	}
	return nil
}

// printNewLine prints a carriage return (S 15, new_line).
func (m *Machine) printNewLine() error {
	// ZSCII 13 is the Z-machine's only line terminator (S 3.8.2.5), and is
	// what a newline must be written as on stream 3 (S 7.1.2.2.1).
	return m.printZSCII(zsciiNewline)
}

// selectMemoryStream selects output stream 3, writing to the table at addr
// (S 7.1.2.1).
func (m *Machine) selectMemoryStream(addr uint32) error {
	// S 7.1.2.1.1: selections nest up to sixteen deep, and a seventeenth is an
	// error. The bound also stops a story from growing the stack of tables
	// without limit.
	if len(m.out.tables) >= memoryStreamDepth {
		return fmt.Errorf("zmachine: output stream 3 selected %d times without being deselected, which S 7.1.2.1.1 forbids: %w",
			memoryStreamDepth+1, ErrExecutionFault)
	}
	// The table is written to as the story prints, so it must lie in dynamic
	// memory. Checking the count word now turns a bad table address into an
	// error at the point the story named it rather than at some later print.
	if !m.mem.writable(addr, memoryStreamHeader) {
		return m.mem.accessError(MemoryWrite, memoryStreamHeader, addr,
			"output stream 3 table is not in dynamic memory (0x%04x)", m.mem.dynamicSize())
	}
	m.out.tables = append(m.out.tables, memoryStream{table: addr})
	m.logger.Debug("output stream 3 selected",
		slog.Uint64("table", uint64(addr)), slog.Int("depth", len(m.out.tables)))
	return nil
}

// deselectMemoryStream deselects output stream 3, writing the number of
// characters printed into the first word of the table (S 7.1.2.1). Deselecting
// a stream that is not selected does nothing.
func (m *Machine) deselectMemoryStream() error {
	if len(m.out.tables) == 0 {
		m.logger.Debug("output stream 3 deselected while it was not selected")
		return nil
	}
	stream := m.out.tables[len(m.out.tables)-1]
	m.out.tables = m.out.tables[:len(m.out.tables)-1]
	if err := m.mem.writeWord(stream.table, stream.count); err != nil {
		return err
	}
	m.logger.Debug("output stream 3 deselected",
		slog.Uint64("table", uint64(stream.table)), slog.Uint64("characters", uint64(stream.count)))
	return nil
}

// writeMemoryStream appends one ZSCII byte to the innermost selected table.
//
// S 7.1.2.1 says it is the programmer's responsibility to make the table large
// enough and that the interpreter performs no overflow checking. That cannot
// mean writing outside dynamic memory, so the write goes through the checked
// accessor and a table that runs off the end of dynamic memory is an error.
func (m *Machine) writeMemoryStream(b uint8) error {
	stream := &m.out.tables[len(m.out.tables)-1]
	if stream.count == 0xffff {
		return fmt.Errorf("zmachine: output stream 3 table at 0x%04x has taken 65535 characters: %w",
			stream.table, ErrExecutionFault)
	}
	addr := stream.table + memoryStreamHeader + uint32(stream.count)
	if err := m.mem.writeByte(addr, b); err != nil {
		return err
	}
	stream.count++
	return nil
}

// splitWindow gives the upper window the given height in lines, or unsplits
// the screen when it is zero (S 15, split_window).
func (m *Machine) splitWindow(lines uint16) {
	m.out.upperHeight = lines
	// S 8.6.1.1.2: in Version 3 the upper window is cleared when a screen
	// split takes place. Text already there is gone from the display, so it is
	// dropped from the capture too.
	m.out.upper = m.out.upper[:0]
	m.logger.Debug("screen split", slog.Uint64("upper_lines", uint64(lines)))
}

// setWindow selects a window for text output (S 15, set_window).
func (m *Machine) setWindow(window uint16) error {
	// S 8.6.1: Version 3 has exactly two windows. A story naming another one
	// would have its output silently misdirected, so it is refused instead.
	if window != windowLower && window != windowUpper {
		return fmt.Errorf("zmachine: window %d does not exist in Version 3, which has windows 0 and 1: %w",
			window, ErrExecutionFault)
	}
	m.out.window = uint8(window)
	// S 8.6.1: whenever the upper window is selected its cursor returns to the
	// top left. This engine has no cursor, so nothing is recorded; the text
	// captured for the upper window is a transcript of what was written to it,
	// not a picture of where it landed.
	m.logger.Debug("window selected", slog.Uint64("window", uint64(window)))
	return nil
}
