package zmachine

import "fmt"

// The dictionary and the encoding of typed words (S 13, S 3.7).
//
// The dictionary lives in static memory and begins with a short header
// (S 13.2):
//
//	n   list of keyboard input codes   entry-length  number-of-entries
//	byte  ------n bytes-------------   ----byte----  ---2-byte word---
//
// The keyboard input codes are the word separators of S 13.6.1. In Versions 1
// to 3 each entry then holds four bytes of encoded text - six Z-characters -
// followed by data bytes the interpreter never looks at (S 13.3).
//
// Encoding is the exact inverse of the decoding in text.go, restricted by
// S 3.7: the text is lower-cased, abbreviations may not be used, the pad
// character is Z-character 5 and the result is always exactly six
// Z-characters, with any multi-Z-character construction left incomplete rather
// than dropped if it does not fit.

const (
	// dictionaryZCharsV3 is the number of Z-characters in one entry's encoded
	// text (S 13.3).
	dictionaryZCharsV3 = 6
	// dictionaryTextBytesV3 is the size of that encoded text (S 13.3).
	dictionaryTextBytesV3 = 4
)

// dictionaryTable describes one dictionary as its header lays it out (S 13.2).
type dictionaryTable struct {
	// addr is the byte address of the dictionary.
	addr uint32
	// separators are the word separators: ZSCII codes which divide words and
	// are each a word in their own right (S 13.6.1).
	separators []uint8
	// entryLength is the size in bytes of one entry, at least 4 in Version 3.
	entryLength uint32
	// count is the number of entries.
	count uint32
	// entries is the byte address of the first entry.
	entries uint32
}

// storyDictionary reads the header of the dictionary named in the story header
// (S 13.1).
//
// LoadStory has already validated this table and the dictionary lies in static
// memory, which a story cannot write, so the reads below cannot fail for a
// loaded story. They still go through the checked accessors so that this
// function is safe to call on any address.
func (m *memory) storyDictionary() (dictionaryTable, error) {
	return m.readDictionary(m.story.dictionary)
}

// readDictionary reads a dictionary header from addr (S 13.2).
func (m *memory) readDictionary(addr uint32) (dictionaryTable, error) {
	count, err := m.readByte(addr)
	if err != nil {
		return dictionaryTable{}, err
	}
	table := dictionaryTable{addr: addr}
	if count != 0 {
		table.separators = make([]uint8, count)
		for i := range table.separators {
			code, err := m.readByte(addr + 1 + uint32(i))
			if err != nil {
				return dictionaryTable{}, err
			}
			table.separators[i] = code
		}
	}

	lengthAt := addr + 1 + uint32(count)
	entryLength, err := m.readByte(lengthAt)
	if err != nil {
		return dictionaryTable{}, err
	}
	if entryLength < dictionaryEntryLengthMinV3 {
		return dictionaryTable{}, fmt.Errorf("zmachine: dictionary at 0x%04x: entry length %d is less than the Version 3 minimum of %d (S 13.2): %w",
			addr, entryLength, dictionaryEntryLengthMinV3, ErrExecutionFault)
	}
	table.entryLength = uint32(entryLength)

	entries, err := m.readWord(lengthAt + 1)
	if err != nil {
		return dictionaryTable{}, err
	}
	// S 15, tokenise: a negative count marks an unsorted user dictionary. The
	// dictionary named in the story header is the sorted main one, and Version
	// 3 has no tokenise instruction to supply another.
	if signed(entries) < 0 {
		return dictionaryTable{}, fmt.Errorf("zmachine: dictionary at 0x%04x: entry count %d is negative, which marks an unsorted user dictionary (S 15, tokenise): %w",
			addr, signed(entries), ErrExecutionFault)
	}
	table.count = uint32(entries)
	table.entries = lengthAt + 3

	// entryLength is at most 255 and count at most 32767, so the extent cannot
	// overflow a uint32.
	if !m.readable(table.entries, table.count*table.entryLength) {
		return dictionaryTable{}, fmt.Errorf("zmachine: dictionary at 0x%04x: %d entries of %d bytes run past the end of the story (0x%04x): %w",
			addr, table.count, table.entryLength, m.size(), ErrMemoryAccess)
	}
	return table, nil
}

// isSeparator reports whether a ZSCII code is one of the dictionary's word
// separators (S 13.6.1).
func (d dictionaryTable) isSeparator(code uint8) bool {
	for _, sep := range d.separators {
		if sep == code {
			return true
		}
	}
	return false
}

// encodeDictionaryWord encodes a word of ZSCII codes into the four bytes a
// Version 3 dictionary entry holds (S 3.7, S 13.3).
//
// The word is truncated to six Z-characters and padded with Z-character 5. A
// word of more than six Z-characters therefore encodes to the same four bytes
// as its prefix, which is why "mailboxes" matches the dictionary entry for
// "mailbox".
func encodeDictionaryWord(word []uint8) [dictionaryTextBytesV3]byte {
	chars := make([]uint8, 0, dictionaryZCharsV3+3)
	for _, code := range word {
		if len(chars) >= dictionaryZCharsV3 {
			break
		}
		chars = appendWordZChars(chars, code)
	}
	// S 3.7: "The pad character, if needed, must be 5. The total string length
	// must be 6 Z-characters (in Versions 1 to 3) ... any multi-Z-character
	// constructions should be left incomplete (rather than omitted) if there's
	// no room to finish them." Appending the whole construction and then
	// truncating is what leaves it incomplete.
	for len(chars) < dictionaryZCharsV3 {
		chars = append(chars, zcharShiftA2)
	}
	chars = chars[:dictionaryZCharsV3]

	// S 3.2: three Z-characters to a word, and the top bit of the last word
	// marks the end of the string.
	first := uint16(chars[0])<<10 | uint16(chars[1])<<5 | uint16(chars[2])
	second := uint16(chars[3])<<10 | uint16(chars[4])<<5 | uint16(chars[5]) | zstringEndBit
	return [dictionaryTextBytesV3]byte{
		uint8(first >> 8), uint8(first),
		uint8(second >> 8), uint8(second),
	}
}

// appendWordZChars appends the Z-characters for one ZSCII code (S 3.5, S 3.7).
func appendWordZChars(dst []uint8, code uint8) []uint8 {
	// S 3.7: "Text should be converted to lower case (as a result A1 will not
	// be needed unless the game provides its own alphabet table)", and a
	// Version 3 story cannot provide one (S 3.5.5).
	if code >= 'A' && code <= 'Z' {
		code += 'a' - 'A'
	}
	if code == zsciiSpace {
		// S 3.5.1: Z-character 0 is a space.
		return append(dst, zcharSpace)
	}
	if code >= 'a' && code <= 'z' {
		// S 3.5.3: A0 holds the lower-case letters from Z-character 6 upward.
		return append(dst, code-'a'+zcharAlphabetLow)
	}
	// S 3.5.3: A2 holds the digits and punctuation, reached by a shift.
	// Position 0 of the table is the ten-bit escape rather than a character, so
	// the search starts past it.
	for i := 1; i < len(alphabetA2); i++ {
		if alphabetA2[i] == code {
			return append(dst, zcharShiftA2, uint8(i)+zcharAlphabetLow)
		}
	}
	// S 3.4: anything else is written as a ten-bit ZSCII escape, whose two
	// halves follow Z-character 6 in A2.
	return append(dst, zcharShiftA2, zcharEscape,
		code>>zsciiEscapeShift&zcharMask, code&zcharMask)
}

// lookupDictionaryWord returns the byte address of the entry whose encoded text
// is exactly encoded, or 0 when the dictionary holds no such word (S 13.6.2).
//
// S 13.5 requires entries to be "given in numerical order of the encoded text
// (when the encoded text is regarded as a 32 ... bit binary number with
// most-significant byte first)", with no two entries alike, which is what makes
// the binary chop below correct.
func (m *memory) lookupDictionaryWord(table dictionaryTable, encoded [dictionaryTextBytesV3]byte) (uint16, error) {
	target := uint32(encoded[0])<<24 | uint32(encoded[1])<<16 | uint32(encoded[2])<<8 | uint32(encoded[3])

	low, high := uint32(0), table.count
	for low < high {
		middle := low + (high-low)/2
		addr := table.entries + middle*table.entryLength
		text, err := m.readEntryText(addr)
		if err != nil {
			return 0, err
		}
		switch {
		case text < target:
			low = middle + 1
		case text > target:
			high = middle
		default:
			if addr > 0xffff {
				// The address goes into the parse buffer as a word, so one the
				// story could not read back would name a different entry.
				return 0, fmt.Errorf("zmachine: dictionary entry at 0x%04x does not fit in the word the parse buffer holds: %w",
					addr, ErrExecutionFault)
			}
			return uint16(addr), nil
		}
	}
	// S 15, read: a word that is not in the dictionary is recorded as 0.
	return 0, nil
}

// readEntryText returns the four bytes of encoded text at the start of a
// dictionary entry as one 32-bit number, most significant byte first (S 13.5).
func (m *memory) readEntryText(addr uint32) (uint32, error) {
	high, err := m.readWord(addr)
	if err != nil {
		return 0, err
	}
	low, err := m.readWord(addr + zstringWordSize)
	if err != nil {
		return 0, err
	}
	return uint32(high)<<16 | uint32(low), nil
}
