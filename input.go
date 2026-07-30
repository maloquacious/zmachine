package zmachine

import (
	"fmt"
	"log/slog"
	"unicode"
)

// Line input and lexical analysis (S 15, read; S 13.6).
//
// sread is the only instruction that suspends. The execution loop returns to
// the host before this code runs when no line has been supplied (spec S 4), so
// by the time an instruction reaches here it always has exactly one line to
// give the story.
//
// What the interpreter owes the story is only the Z-machine-level mechanics:
// redisplay the status line, lower-case the line, put it in the text buffer,
// break it into words, look those words up in the dictionary and fill in the
// parse buffer (spec S 20). Interpreting the command is the story's own
// parser's job and nothing here knows anything about game grammar.

const (
	// textBufferOffsetV3 is where the typed text begins in the text buffer.
	// "In Versions 1 to 4 ... The text typed ... is stored in bytes 1 onward,
	// with a zero terminator" (S 15, read). It is also the position recorded
	// for the first character of the buffer, because the positions written into
	// the parse buffer are offsets into the text buffer itself. Version 5 moves
	// both to 2, which is why this is a Version 3 constant.
	textBufferOffsetV3 = 1

	// textBufferMinBytes and parseBufferMinBytes are the smallest buffers S 15
	// accepts: "Interpreters are asked to halt with a suitable error message if
	// the text or parse buffers have length of less than 3 or 6 bytes,
	// respectively: this sometimes occurs due to a previous array being
	// overrun, causing bugs which are very difficult to find."
	textBufferMinBytes  = 3
	parseBufferMinBytes = 6

	// parseHeaderSize is the size of the parse buffer header: the maximum
	// number of words and the number actually parsed (S 15, read).
	parseHeaderSize = 2
	// parseEntrySize is the size of one parsed word: the dictionary address as
	// a word, the number of letters, and the position in the text buffer
	// (S 15, read).
	parseEntrySize = 4
)

// inputWord is one word found in the player's line (S 13.6.1).
type inputWord struct {
	// start is the offset of the first character within the typed text, before
	// the text buffer's own offset is added.
	start uint32
	// text is the word's ZSCII characters.
	text []uint8
}

// executeSRead carries out sread (S 15, read).
func (m *Machine) executeSRead(inst *instruction, ops []uint16) (control, error) {
	// S 15, read: Version 3 takes the text and parse buffers and nothing else.
	// The time and routine operands arrive in Version 4.
	if err := m.operands(inst, 2, 2); err != nil {
		return controlContinue, err
	}
	// S 15, read: "In Versions 1 to 3, the status line is automatically
	// redisplayed first." S 8.2.4 makes this one of only two moments at which
	// the status line is updated.
	if err := m.updateStatusLine(); err != nil {
		return controlContinue, m.fail(inst, err)
	}

	// The line is consumed here, so that a story asking for a second line in
	// the same call reaches the input boundary instead (spec S 11).
	line := m.input
	m.input, m.hasInput = "", false

	text, err := m.storeInputLine(uint32(ops[0]), line)
	if err != nil {
		return controlContinue, m.fail(inst, err)
	}
	words, err := m.tokeniseInput(uint32(ops[1]), text)
	if err != nil {
		return controlContinue, m.fail(inst, err)
	}

	m.logger.Debug("line input read",
		slog.Uint64("pc", uint64(inst.addr)),
		slog.Int("characters", len(text)),
		slog.Int("words", words))
	return controlContinue, nil
}

// storeInputLine writes the player's line into the text buffer and returns the
// ZSCII characters it stored (S 15, read).
func (m *Machine) storeInputLine(addr uint32, line string) ([]uint8, error) {
	size, err := m.mem.readByte(addr)
	if err != nil {
		return nil, err
	}
	// S 15, read: "if byte 0 contains n then the buffer must contain n+1
	// bytes". The text occupies bytes 1 to n and ends with a zero terminator,
	// so at most n-1 characters fit, which is the "maximum number of letters
	// which can be typed" the same paragraph describes.
	length := uint32(size) + 1
	if length < textBufferMinBytes {
		return nil, fmt.Errorf("zmachine: text buffer at 0x%04x declares %d byte(s); S 15 asks the interpreter to halt when it is shorter than %d: %w",
			addr, length, textBufferMinBytes, ErrExecutionFault)
	}
	if !m.mem.writable(addr, length) {
		return nil, m.mem.accessError(MemoryWrite, length, addr,
			"text buffer is not in dynamic memory (0x%04x)", m.mem.dynamicSize())
	}
	maxLetters := int(size) - 1

	text := make([]uint8, 0, maxLetters)
	for _, r := range line {
		if len(text) >= maxLetters {
			// The host supplies a whole line (spec S 11); an interactive
			// interpreter would have refused the keystrokes past the buffer's
			// maximum, so the tail is dropped rather than reported as an error.
			m.logger.Debug("input line truncated to the text buffer maximum",
				slog.Int("maximum", maxLetters))
			break
		}
		// S 15, read: "The text typed is reduced to lower case (so that it can
		// tidily be printed back by the program if need be)".
		code, ok := zsciiFromRune(unicode.ToLower(r))
		if !ok || code < zsciiSpace {
			// A command is text. Control codes - including the line terminator
			// a host may leave on the end, which S 15 says is not stored - and
			// characters ZSCII cannot represent are not part of it.
			continue
		}
		text = append(text, code)
	}

	for i, code := range text {
		if err := m.mem.writeByte(addr+textBufferOffsetV3+uint32(i), code); err != nil {
			return nil, err
		}
	}
	// S 15, read: the text is stored "with a zero terminator (but without any
	// other terminator, such as a carriage return code)".
	if err := m.mem.writeByte(addr+textBufferOffsetV3+uint32(len(text)), 0); err != nil {
		return nil, err
	}
	return text, nil
}

// tokeniseInput performs the lexical analysis of S 13.6 on the stored text and
// writes the parse buffer (S 15, read). It returns the number of words
// recorded.
func (m *Machine) tokeniseInput(addr uint32, text []uint8) (int, error) {
	table, err := m.mem.storyDictionary()
	if err != nil {
		return 0, err
	}

	maxWords, err := m.mem.readByte(addr)
	if err != nil {
		return 0, err
	}
	// S 15, read: "Initially, byte 0 of the parse-buffer should hold the
	// maximum number of textual words which can be parsed. (If this is n, the
	// buffer must be at least 2 + 4*n bytes long)", and a buffer of fewer than
	// 6 bytes is the error the same section asks interpreters to halt on.
	//
	// Only the header is checked against dynamic memory here, not the whole
	// 2+4*n bytes: S 15 records that Versions 1 and 2 and early Version 3 games
	// wrongly declare 240 words in a 240-byte buffer, so a story may legally
	// promise more room than it has. Each word written below is checked
	// individually instead.
	if parseHeaderSize+parseEntrySize*uint32(maxWords) < parseBufferMinBytes {
		return 0, fmt.Errorf("zmachine: parse buffer at 0x%04x declares room for %d word(s), %d byte(s); S 15 asks the interpreter to halt when it is shorter than %d: %w",
			addr, maxWords, parseHeaderSize+parseEntrySize*uint32(maxWords), parseBufferMinBytes, ErrExecutionFault)
	}
	if !m.mem.writable(addr, parseHeaderSize) {
		return 0, m.mem.accessError(MemoryWrite, parseHeaderSize, addr,
			"parse buffer is not in dynamic memory (0x%04x)", m.mem.dynamicSize())
	}

	written := 0
	for _, word := range splitInputWords(text, table) {
		// S 15, read: one block is written for each word "except that it should
		// stop before going beyond the maximum number of words specified".
		if written >= int(maxWords) {
			break
		}
		entry, err := m.mem.lookupDictionaryWord(table, encodeDictionaryWord(word.text))
		if err != nil {
			return 0, err
		}
		block := addr + parseHeaderSize + uint32(written)*parseEntrySize
		// S 15, read: the byte address of the word in the dictionary, or 0 if
		// it is not in the dictionary; then the number of letters in the word;
		// then the position in the text buffer of its first letter.
		if err := m.mem.writeWord(block, entry); err != nil {
			return 0, err
		}
		if err := m.mem.writeByte(block+2, uint8(len(word.text))); err != nil {
			return 0, err
		}
		if err := m.mem.writeByte(block+3, uint8(word.start+textBufferOffsetV3)); err != nil {
			return 0, err
		}
		written++
	}

	// S 15, read: "The number of words is written in byte 1".
	if err := m.mem.writeByte(addr+1, uint8(written)); err != nil {
		return 0, err
	}
	return written, nil
}

// splitInputWords breaks typed text into words (S 13.6.1): "Spaces divide up
// words and are otherwise ignored. Word separators also divide words, but each
// one of them is considered a word in its own right."
//
// So "fred,go fishing" becomes the four words fred, ",", go and fishing.
func splitInputWords(text []uint8, table dictionaryTable) []inputWord {
	var words []inputWord
	start := -1

	// flush ends the word in progress, if there is one, at the given offset.
	flush := func(end int) {
		if start < 0 {
			return
		}
		words = append(words, inputWord{start: uint32(start), text: text[start:end]})
		start = -1
	}

	for i, code := range text {
		switch {
		case code == zsciiSpace:
			// S 13.2.1 notes a space is never a word separator, so it is tested
			// first and simply divides.
			flush(i)
		case table.isSeparator(code):
			flush(i)
			words = append(words, inputWord{start: uint32(i), text: text[i : i+1]})
		default:
			if start < 0 {
				start = i
			}
		}
	}
	flush(len(text))
	return words
}
