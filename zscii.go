package zmachine

import "unicode/utf8"

// ZSCII (Zork Standard Code for Information Interchange) is the character set
// of the Z-machine (S 3.8). ZSCII codes are 10-bit unsigned values; only the
// codes listed in S 3.8 are legal, and some of those are defined for input
// only, some for output only.
//
// The codes named here are the ones a Version 3 interpreter must recognise.
const (
	// zsciiNull is defined for output but has no effect on any output stream
	// (S 3.8.2.1).
	zsciiNull = 0
	// zsciiDelete is defined for input only (S 3.8.2.2).
	zsciiDelete = 8
	// zsciiNewline is defined for both input and output (S 3.8.2.5). It is the
	// only control code a Version 3 story may print.
	zsciiNewline = 13
	// zsciiEscape is defined for input only (S 3.8.2.6).
	zsciiEscape = 27
	// zsciiSpace and zsciiTilde bound the block of codes that agree with
	// standard ASCII and are defined for input and output (S 3.8.3).
	zsciiSpace = 32
	zsciiTilde = 126

	// zsciiExtraFirst is the first of the "extra characters" (S 3.8.5). How
	// many of them are defined depends on the Unicode translation table.
	zsciiExtraFirst = 155

	// zsciiLimit is one past the largest value a ZSCII code can take: codes are
	// 10-bit unsigned values (S 3.8).
	zsciiLimit = 1024
)

// defaultUnicodeTable maps the extra characters, starting at zsciiExtraFirst,
// to Unicode code points (S 3.8.5.3, Table 1).
//
// Version 3 always uses this table: a story file may supply its own Unicode
// translation table only in Version 5 and later, and "under Versions 1 to 4,
// the default table is always used" (S 3.8.5.2).
//
// The array is immutable data. Nothing in this package writes to it.
var defaultUnicodeTable = [...]rune{
	0x0e4, 0x0f6, 0x0fc, 0x0c4, 0x0d6, 0x0dc, 0x0df, 0x0bb, // 155-162
	0x0ab, 0x0eb, 0x0ef, 0x0ff, 0x0cb, 0x0cf, 0x0e1, 0x0e9, // 163-170
	0x0ed, 0x0f3, 0x0fa, 0x0fd, 0x0c1, 0x0c9, 0x0cd, 0x0d3, // 171-178
	0x0da, 0x0dd, 0x0e0, 0x0e8, 0x0ec, 0x0f2, 0x0f9, 0x0c0, // 179-186
	0x0c8, 0x0cc, 0x0d2, 0x0d9, 0x0e2, 0x0ea, 0x0ee, 0x0f4, // 187-194
	0x0fb, 0x0c2, 0x0ca, 0x0ce, 0x0d4, 0x0db, 0x0e5, 0x0c5, // 195-202
	0x0f8, 0x0d8, 0x0e3, 0x0f1, 0x0f5, 0x0c3, 0x0d1, 0x0d5, // 203-210
	0x0e6, 0x0c6, 0x0e7, 0x0c7, 0x0fe, 0x0f0, 0x0de, 0x0d0, // 211-218
	0x0a3, 0x153, 0x152, 0x0a1, 0x0bf, // 219-223
}

// zsciiOutputRune returns the character that a ZSCII code prints as, and
// reports whether it prints anything at all.
//
// Codes that Version 3 does not define for output print nothing. This is
// deliberate: a story that prints a random stretch of memory as a string can
// produce arbitrary codes, and the specification recommends filtering illegal
// codes out rather than letting them reach the display (S 3.8 remarks;
// Appendix A). Undefined codes are a story bug, not an interpreter fault, so
// they are dropped rather than reported as an error.
//
// Codes defined for input only - delete, escape, the cursor and function keys,
// the mouse codes - also print nothing (S 3.8.2, S 3.8.4, S 3.8.6). So do the
// Version 6 output codes, tab and sentence space, which Version 3 does not
// define (S 3.8.2.3, S 3.8.2.4).
func zsciiOutputRune(code uint16) (rune, bool) {
	switch {
	case code == zsciiNewline:
		// S 3.8.2.5: ZSCII 13 is a carriage return. The engine models story
		// output as text, so it becomes a single newline character.
		return '\n', true
	case code >= zsciiSpace && code <= zsciiTilde:
		// S 3.8.3: this block agrees with ASCII, and therefore with Unicode.
		return rune(code), true
	case code >= zsciiExtraFirst && int(code) < zsciiExtraFirst+len(defaultUnicodeTable):
		return defaultUnicodeTable[code-zsciiExtraFirst], true
	default:
		return 0, false
	}
}

// appendZSCII appends the printed form of a ZSCII code to dst, appending
// nothing if the code prints nothing.
func appendZSCII(dst []byte, code uint16) []byte {
	r, ok := zsciiOutputRune(code)
	if !ok {
		return dst
	}
	return utf8.AppendRune(dst, r)
}

// zsciiFromRune maps a character supplied by the host as player input to its
// ZSCII code, reporting false if ZSCII cannot represent it.
//
// Both '\n' and '\r' map to zsciiNewline: ZSCII has one line terminator
// (S 3.8.2.5), so the host's line ending convention must not reach the VM.
func zsciiFromRune(r rune) (uint8, bool) {
	switch {
	case r == '\n' || r == '\r':
		return zsciiNewline, true
	case r >= zsciiSpace && r <= zsciiTilde:
		// S 3.8.3: input and output agree with ASCII in this range.
		return uint8(r), true
	case r == zsciiDelete || r == zsciiEscape:
		// S 3.8.2.2, S 3.8.2.6: defined for input only.
		return uint8(r), true
	}
	// S 3.8.5: the extra characters are defined for input as well as output.
	// The table has 69 entries, so a linear scan is cheaper than building - and
	// having to protect - a shared reverse map.
	for i, u := range defaultUnicodeTable {
		if u == r {
			return uint8(zsciiExtraFirst + i), true
		}
	}
	return 0, false
}
