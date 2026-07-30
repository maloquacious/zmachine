package zmachine

import "testing"

// TestZSCIIOutputRune covers S 3.8: only the codes the standard defines are
// legal, and Version 3 defines fewer of them than later versions. Everything
// else prints nothing, so that a story printing a random stretch of memory
// cannot emit control characters (S 3.8 remarks; Appendix A).
func TestZSCIIOutputRune(t *testing.T) {
	tests := []struct {
		name string
		code uint16
		want rune
		ok   bool
	}{
		{name: "null prints nothing (S 3.8.2.1)", code: 0},
		{name: "delete is input only (S 3.8.2.2)", code: 8},
		{name: "tab is Version 6 only (S 3.8.2.3)", code: 9},
		{name: "line feed is undefined (S 3.8.2)", code: 10},
		{name: "sentence space is Version 6 only (S 3.8.2.4)", code: 11},
		{name: "newline (S 3.8.2.5)", code: 13, want: '\n', ok: true},
		{name: "escape is input only (S 3.8.2.6)", code: 27},
		{name: "space is the first printable code (S 3.8.3)", code: 32, want: ' ', ok: true},
		{name: "digit", code: '7', want: '7', ok: true},
		{name: "capital letter", code: 'G', want: 'G', ok: true},
		{name: "tilde is the last printable code (S 3.8.3)", code: 126, want: '~', ok: true},
		{name: "127 is undefined (S 3.8.3.1)", code: 127},
		{name: "128 is undefined (S 3.8.3.1)", code: 128},
		{name: "cursor up is input only (S 3.8.4)", code: 129},
		{name: "keypad 9 is input only (S 3.8.4)", code: 154},
		{name: "first extra character (S 3.8.5.3)", code: 155, want: 'ä', ok: true},
		{name: "sz ligature (S 3.8.5.3)", code: 161, want: 'ß', ok: true},
		{name: "oe ligature is outside Latin-1 (S 3.8.5.3)", code: 220, want: 'œ', ok: true},
		{name: "last default extra character (S 3.8.5.3)", code: 223, want: '¿', ok: true},
		{name: "beyond the default Unicode table", code: 224},
		{name: "last extra character slot", code: 251},
		{name: "mouse click is input only (S 3.8.6)", code: 254},
		{name: "255 is undefined (S 3.8.7)", code: 255},
		{name: "codes above 255 are undefined (S 3.8.1)", code: 256},
		{name: "largest ten-bit code", code: zsciiLimit - 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := zsciiOutputRune(tt.code)
			if ok != tt.ok {
				t.Fatalf("zsciiOutputRune(%d) ok = %v, want %v", tt.code, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("zsciiOutputRune(%d) = %q, want %q", tt.code, got, tt.want)
			}
		})
	}
}

func TestAppendZSCII(t *testing.T) {
	var got []byte
	for _, code := range []uint16{'h', 'i', 0, 13, 9, 'o', 161, 300} {
		got = appendZSCII(got, code)
	}
	// The null, the Version 6 tab and the undefined code 300 print nothing.
	const want = "hi\noß"
	if string(got) != want {
		t.Errorf("appendZSCII sequence = %q, want %q", got, want)
	}
}

// TestZSCIIFromRune covers the input side of S 3.8. Both host line endings
// arrive as the single ZSCII line terminator, code 13 (S 3.8.2.5).
func TestZSCIIFromRune(t *testing.T) {
	tests := []struct {
		name string
		r    rune
		want uint8
		ok   bool
	}{
		{name: "line feed becomes ZSCII newline", r: '\n', want: 13, ok: true},
		{name: "carriage return becomes ZSCII newline", r: '\r', want: 13, ok: true},
		{name: "space", r: ' ', want: 32, ok: true},
		{name: "lower case letter", r: 'q', want: 'q', ok: true},
		{name: "tilde", r: '~', want: 126, ok: true},
		{name: "delete (S 3.8.2.2)", r: 8, want: 8, ok: true},
		{name: "escape (S 3.8.2.6)", r: 27, want: 27, ok: true},
		{name: "extra character (S 3.8.5)", r: 'ä', want: 155, ok: true},
		{name: "last default extra character", r: '¿', want: 223, ok: true},
		{name: "tab has no ZSCII input code", r: '\t'},
		{name: "null has no ZSCII input code", r: 0},
		{name: "delete in ASCII form is undefined (S 3.8.3.1)", r: 127},
		{name: "characters outside the default table", r: 'π'},
		{name: "astral characters", r: '\U0001F600'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := zsciiFromRune(tt.r)
			if ok != tt.ok {
				t.Fatalf("zsciiFromRune(%q) ok = %v, want %v", tt.r, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("zsciiFromRune(%q) = %d, want %d", tt.r, got, tt.want)
			}
		})
	}
}

// TestZSCIIRoundTrip checks that every code defined for both input and output
// survives a trip through the output and input mappings.
func TestZSCIIRoundTrip(t *testing.T) {
	for code := uint16(0); code < zsciiLimit; code++ {
		r, ok := zsciiOutputRune(code)
		if !ok {
			continue
		}
		back, ok := zsciiFromRune(r)
		if !ok {
			t.Errorf("ZSCII %d prints as %q, which has no input code", code, r)
			continue
		}
		if uint16(back) != code {
			t.Errorf("ZSCII %d prints as %q, which reads back as %d", code, r, back)
		}
	}
}

// TestDefaultUnicodeTableCoversTheStandardExtraCharacters pins the size of the
// default Unicode translation table: it defines ZSCII 155 to 223 (S 3.8.5.3,
// Table 1). Version 3 stories always use it, because a story may supply its
// own translation table only in Version 5 and later (S 3.8.5.2).
func TestDefaultUnicodeTableCoversTheStandardExtraCharacters(t *testing.T) {
	const wantEntries = 223 - zsciiExtraFirst + 1
	if len(defaultUnicodeTable) != wantEntries {
		t.Fatalf("default Unicode table has %d entries, want %d", len(defaultUnicodeTable), wantEntries)
	}
	// Every entry must be a distinct printable character, or the reverse
	// mapping used for input would be ambiguous.
	seen := make(map[rune]int, len(defaultUnicodeTable))
	for i, r := range defaultUnicodeTable {
		if r < 0xa0 {
			t.Errorf("entry %d (ZSCII %d) is %U, which is a control code", i, zsciiExtraFirst+i, r)
		}
		if prev, dup := seen[r]; dup {
			t.Errorf("entry %d (ZSCII %d) repeats %U from entry %d", i, zsciiExtraFirst+i, r, prev)
		}
		seen[r] = i
	}
}
