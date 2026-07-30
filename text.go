package zmachine

// Z-string decoding (S 3).
//
// Text is stored as a sequence of 2-byte words, each holding three 5-bit
// Z-characters (S 3.2). Decoding runs in two stages: unpacking the words into
// Z-characters, then translating the Z-characters into ZSCII codes through the
// alphabet table, the abbreviations table and the ten-bit escape sequence.

// The alphabet table for Versions 2 to 4 (S 3.5.3). Each row translates the
// Z-characters 6 to 31 of one alphabet into ZSCII codes; Z-characters 0 to 5
// have meanings that do not depend on the alphabet.
//
// Version 3 always uses this table. A story file may supply its own alphabet
// table, through the header word at $34, only "in Versions 5 and later"
// (S 3.5.5), so in Version 3 the word at $34 has no meaning and is ignored.
const (
	alphabetA0 = "abcdefghijklmnopqrstuvwxyz"
	alphabetA1 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	// Position 0 is Z-character 6, which in A2 introduces a ten-bit ZSCII
	// escape and is therefore never translated through the table (S 3.4); it
	// is held as a null so that a stray lookup prints nothing. Position 1 is
	// Z-character 7, which is a new-line (ZSCII 13).
	alphabetA2 = "\x00\r0123456789.,!?_#'\"/\\-:()"
)

// alphabets is indexed by the current alphabet (S 3.2.1). Its entries are
// strings, so the table is immutable.
var alphabets = [3]string{alphabetA0, alphabetA1, alphabetA2}

const (
	// alphabet indices into alphabets.
	alphabetIndexA0 = 0
	alphabetIndexA1 = 1
	alphabetIndexA2 = 2

	// zcharsPerWord is the number of 5-bit Z-characters packed into each word
	// of text (S 3.2).
	zcharsPerWord = 3
	// zcharMask selects one 5-bit Z-character.
	zcharMask = 0x1f
	// zstringEndBit is set on the last word of a string, and only there
	// (S 3.2).
	zstringEndBit = 0x8000
	// zstringWordSize is the size in bytes of one word of text.
	zstringWordSize = 2

	// Z-characters whose meaning does not come from the alphabet table.
	zcharSpace       = 0 // printed as a space, ZSCII 32 (S 3.5.1)
	zcharAbbrevLast  = 3 // 1, 2 and 3 introduce abbreviations (S 3.3)
	zcharShiftA1     = 4 // the next character is in A1 (S 3.2.3)
	zcharShiftA2     = 5 // the next character is in A2 (S 3.2.3)
	zcharEscape      = 6 // in A2 only: a ten-bit ZSCII escape (S 3.4)
	zcharAlphabetLow = 6 // the first Z-character the alphabet table covers

	// abbreviationsPerBlock is the number of abbreviations selected by each of
	// the Z-characters 1, 2 and 3 (S 3.3).
	abbreviationsPerBlock = 32
	// abbreviationCount is the number of entries in the Version 3
	// abbreviations table (S 3.3).
	abbreviationCount = 3 * abbreviationsPerBlock

	// zsciiEscapeShift is how far the first half of a ten-bit ZSCII escape is
	// shifted: the next Z-character gives the top 5 bits and the one after the
	// bottom 5 (S 3.4).
	zsciiEscapeShift = 5
)

// abbreviationExpander returns the text of an entry in the abbreviations
// table. A nil expander means abbreviations are not available: an abbreviation
// string may not itself use abbreviations (S 3.3.1).
type abbreviationExpander func(index uint16) (string, error)

// decodeStringAt decodes the Z-string stored at the byte address addr. It
// returns the text and the address of the first byte after the string, so that
// callers which print and advance - the print instruction embeds its text
// directly after the opcode - can continue decoding from there.
func decodeStringAt(m *memory, addr uint32) (string, uint32, error) {
	chars, next, err := readZChars(m, addr)
	if err != nil {
		return "", 0, err
	}
	text, err := decodeZText(chars, addr, func(index uint16) (string, error) {
		return abbreviationText(m, index)
	})
	if err != nil {
		return "", 0, err
	}
	return text, next, nil
}

// decodeStringAtPacked decodes the Z-string named by a packed address, as
// print_paddr does. In Versions 1 to 3 the byte address is twice the packed
// address (S 1.2.3).
func decodeStringAtPacked(m *memory, packed uint16) (string, uint32, error) {
	return decodeStringAt(m, unpackAddress(packed))
}

// readZChars reads the Z-characters of the string starting at addr, stopping
// after the word whose top bit is set (S 3.2). It returns the Z-characters and
// the address of the first byte after the string.
//
// The scan is bounded by the size of the story: every iteration reads a word
// through the memory view's checked accessor, so a string with no terminator
// stops at the end of memory with an error rather than looping.
func readZChars(m *memory, addr uint32) ([]uint8, uint32, error) {
	// Most strings are short; the slice grows if they are not.
	chars := make([]uint8, 0, 3*zcharsPerWord)
	for at := addr; ; at += zstringWordSize {
		word, err := m.readWord(at)
		if err != nil {
			if at == addr {
				// The string does not begin inside the story at all, which is
				// a memory fault rather than a malformed string.
				return nil, 0, err
			}
			return nil, 0, textErrorf(addr,
				"unterminated: no word with the end-of-string bit set before the end of the story (0x%04x)", m.size())
		}
		chars = appendZChars(chars, word)
		if word&zstringEndBit != 0 {
			return chars, at + zstringWordSize, nil
		}
	}
}

// unpackZChars splits a run of 2-byte words into Z-characters, stopping after
// the word whose top bit is set (S 3.2). It returns the Z-characters and the
// number of bytes consumed.
//
// It is the same unpacking readZChars performs, over a plain byte slice rather
// than a memory view.
func unpackZChars(data []byte) ([]uint8, int, error) {
	chars := make([]uint8, 0, len(data)/zstringWordSize*zcharsPerWord)
	for i := 0; i+zstringWordSize <= len(data); i += zstringWordSize {
		// S 2.1: words are stored most significant byte first.
		word := uint16(data[i])<<8 | uint16(data[i+1])
		chars = appendZChars(chars, word)
		if word&zstringEndBit != 0 {
			return chars, i + zstringWordSize, nil
		}
	}
	return nil, 0, textErrorf(0,
		"unterminated: no word with the end-of-string bit set in %d byte(s)", len(data))
}

// appendZChars appends the three Z-characters packed into word (S 3.2):
//
//	--first byte-------   --second byte---
//	7    6 5 4 3 2  1 0   7 6 5  4 3 2 1 0
//	bit  --first--  --second---  --third--
func appendZChars(dst []uint8, word uint16) []uint8 {
	return append(dst,
		uint8((word>>10)&zcharMask),
		uint8((word>>5)&zcharMask),
		uint8(word&zcharMask))
}

// decodeZText translates a sequence of Z-characters into text (S 3.2 to
// S 3.6). addr is the address the Z-characters were read from and is used only
// for error context; pass zero when they did not come from story memory.
//
// expand resolves abbreviations. A nil expander rejects any use of an
// abbreviation, which is how the rule that an abbreviation may not itself use
// abbreviations is enforced (S 3.3.1).
func decodeZText(chars []uint8, addr uint32, expand abbreviationExpander) (string, error) {
	// Every Z-character contributes at most one ZSCII character, so before
	// abbreviations are expanded the output is bounded by the input.
	out := make([]byte, 0, len(chars))

	// A multi-Z-character construction consumes the Z-characters that follow
	// it as data rather than as characters.
	const (
		expectNothing = iota
		expectAbbreviation
		expectEscapeTop
		expectEscapeBottom
	)
	state := expectNothing
	var abbrevBlock uint16 // z-1 for the Z-character that began an abbreviation
	var escapeTop uint16

	// S 3.2.1: A0 is current at the start of a string. S 3.2.3: in Version 3
	// the alphabet is always A0 unless changed for one character only, so this
	// records a pending single-character shift and nothing more. There are no
	// shift locks in Version 3, unlike Versions 1 and 2 (S 3.2.2).
	alphabet := alphabetIndexA0

	for _, z := range chars {
		zc := uint16(z)

		switch state {
		case expectAbbreviation:
			state = expectNothing
			// S 3.3: entry 32(z-1)+x, where z began the abbreviation and x is
			// this Z-character.
			text, err := expand(abbrevBlock*abbreviationsPerBlock + zc)
			if err != nil {
				return "", err
			}
			out = append(out, text...)
			continue
		case expectEscapeTop:
			state = expectEscapeBottom
			escapeTop = zc
			continue
		case expectEscapeBottom:
			state = expectNothing
			out = appendZSCII(out, escapeTop<<zsciiEscapeShift|zc)
			continue
		}

		current := alphabet
		// The shift applies to one character only, so it is spent here whether
		// or not this character consults the alphabet table.
		alphabet = alphabetIndexA0

		switch {
		case z == zcharSpace:
			// S 3.5.1: Z-character 0 is printed as a space.
			out = appendZSCII(out, zsciiSpace)
		case z <= zcharAbbrevLast:
			// S 3.3: Z-characters 1, 2 and 3 introduce an abbreviation.
			if expand == nil {
				return "", textErrorf(addr,
					"abbreviation Z-character %d used inside an abbreviation, which S 3.3.1 forbids", z)
			}
			abbrevBlock = zc - 1
			state = expectAbbreviation
		case z == zcharShiftA1:
			alphabet = alphabetIndexA1
		case z == zcharShiftA2:
			alphabet = alphabetIndexA2
		case current == alphabetIndexA2 && z == zcharEscape:
			// S 3.4: the next two Z-characters give a ten-bit ZSCII code.
			state = expectEscapeTop
		default:
			// S 3.5.3: the remaining Z-characters index the alphabet table.
			out = appendZSCII(out, uint16(alphabets[current][z-zcharAlphabetLow]))
		}
	}

	// S 3.6.1: it is legal for a string to end while a multi-Z-character
	// construction is incomplete, and the partial construction is simply
	// ignored. S 3.2.4: a trailing run of shift characters is legal too, and
	// prints nothing. Both are the reason the loop above ends without any
	// check on state or alphabet.
	return string(out), nil
}

// abbreviationText returns the text of entry index of the abbreviations table
// (S 3.3). The table holds word addresses (S 1.2.2).
func abbreviationText(m *memory, index uint16) (string, error) {
	table := m.story.abbreviations
	if table == 0 {
		return "", textErrorf(0, "abbreviation %d used but the story declares no abbreviations table", index)
	}
	if index >= abbreviationCount {
		return "", textErrorf(table, "abbreviation %d is outside the %d-entry abbreviations table", index, abbreviationCount)
	}
	entry := table + uint32(index)*wordAddressScale
	word, err := m.readWord(entry)
	if err != nil {
		return "", err
	}
	addr := expandWordAddress(word)

	chars, _, err := readZChars(m, addr)
	if err != nil {
		return "", err
	}
	// S 3.3.1: an abbreviation string must not itself use abbreviations. The
	// nil expander both enforces that and bounds the recursion at one level,
	// so a table entry pointing at itself cannot exhaust the stack.
	return decodeZText(chars, addr, nil)
}
