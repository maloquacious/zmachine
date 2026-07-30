package zmachine

import (
	"encoding/binary"
	"errors"
	"os"
	"testing"
	"unicode/utf8"
)

// Layout of the story built by newTextFixture. It is the story from
// validTestHeader, whose abbreviations table sits at 0x0280 (S 3.3) and whose
// high memory, from 0x0360 to the end of the file, is unused; hand-built
// Z-strings are written there.
const (
	textAbbrevTable = 0x0280
	textAbbrevEnd   = 0x0340 // also the base of static memory in that story
	textStringBase  = 0x0360
	textStoryEnd    = 0x0400
)

// textFixture builds a small Version 3 story containing hand-built Z-strings.
type textFixture struct {
	t    *testing.T
	data []byte
	next uint32
}

func newTextFixture(t *testing.T) *textFixture {
	t.Helper()
	return &textFixture{t: t, data: validTestHeader().build(), next: textStringBase}
}

// putString writes a Z-string at the next free address and returns that
// address. Strings are always word-aligned, so every address is usable as a
// word address (S 1.2.2).
func (f *textFixture) putString(chars ...uint8) uint32 {
	f.t.Helper()
	addr := f.next
	f.next = f.putStringAt(addr, chars...)
	return addr
}

// putStringAt writes a Z-string at addr and returns the address after it.
func (f *textFixture) putStringAt(addr uint32, chars ...uint8) uint32 {
	f.t.Helper()
	words := packZString(f.t, chars...)
	if int(addr)+len(words) > len(f.data) {
		f.t.Fatalf("Z-string of %d byte(s) at 0x%04x does not fit in the fixture", len(words), addr)
	}
	copy(f.data[addr:], words)
	return addr + uint32(len(words))
}

// putWord plants a raw word, for tests that need malformed text.
func (f *textFixture) putWord(addr uint32, value uint16) {
	f.t.Helper()
	binary.BigEndian.PutUint16(f.data[addr:], value)
}

// setAbbreviation points entry index of the abbreviations table at addr. The
// table holds word addresses (S 1.2.2, S 3.3).
func (f *textFixture) setAbbreviation(index uint16, addr uint32) {
	f.t.Helper()
	if addr%wordAddressScale != 0 {
		f.t.Fatalf("abbreviation %d: 0x%04x is not a word address", index, addr)
	}
	f.putWord(textAbbrevTable+uint32(index)*wordAddressScale, uint16(addr/wordAddressScale))
}

func (f *textFixture) memory() *memory {
	f.t.Helper()
	story, err := LoadStory(f.data)
	if err != nil {
		f.t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	return newMemory(story)
}

// packZString packs Z-characters three to a word and sets the end-of-string
// bit on the last word (S 3.2). A short final word is padded with Z-character
// 5, which is the conventional pad and prints nothing (S 3.6, S 3.2.4).
func packZString(t *testing.T, chars ...uint8) []byte {
	t.Helper()
	padded := append([]uint8(nil), chars...)
	for len(padded)%zcharsPerWord != 0 {
		padded = append(padded, zcharShiftA2)
	}
	if len(padded) == 0 {
		padded = []uint8{zcharShiftA2, zcharShiftA2, zcharShiftA2}
	}

	out := make([]byte, 0, len(padded)/zcharsPerWord*zstringWordSize)
	for i := 0; i < len(padded); i += zcharsPerWord {
		for _, z := range padded[i : i+zcharsPerWord] {
			if z > zcharMask {
				t.Fatalf("Z-character %d does not fit in 5 bits", z)
			}
		}
		word := uint16(padded[i])<<10 | uint16(padded[i+1])<<5 | uint16(padded[i+2])
		if i+zcharsPerWord == len(padded) {
			word |= zstringEndBit
		}
		out = append(out, uint8(word>>8), uint8(word))
	}
	return out
}

// decodeForTest decodes a hand-built Z-string with abbreviations forbidden, so
// that alphabet behaviour can be tested without a story.
func decodeForTest(t *testing.T, chars ...uint8) (string, error) {
	t.Helper()
	unpacked, size, err := unpackZChars(packZString(t, chars...))
	if err != nil {
		t.Fatalf("unpackZChars() error = %v, want nil", err)
	}
	if size != len(packZString(t, chars...)) {
		t.Fatalf("unpackZChars() consumed %d byte(s), want %d", size, len(packZString(t, chars...)))
	}
	return decodeZText(unpacked, 0, nil)
}

func TestDecodeZTextAlphabets(t *testing.T) {
	// Z-characters 6 to 31 index the alphabet table (S 3.5.3).
	all := func(first uint8) []uint8 {
		chars := make([]uint8, 0, 26)
		for z := first; z < 32; z++ {
			chars = append(chars, z)
		}
		return chars
	}
	shifted := func(shift uint8, chars []uint8) []uint8 {
		out := make([]uint8, 0, 2*len(chars))
		for _, z := range chars {
			out = append(out, shift, z)
		}
		return out
	}

	tests := []struct {
		name  string
		chars []uint8
		want  string
	}{
		{
			name:  "lower case text needs no shifts because A0 is current (S 3.2.1)",
			chars: []uint8{13, 10, 17, 17, 20},
			want:  "hello",
		},
		{
			name:  "the whole of A0",
			chars: all(6),
			want:  alphabetA0,
		},
		{
			name:  "the whole of A1 (S 3.2.3)",
			chars: shifted(zcharShiftA1, all(6)),
			want:  alphabetA1,
		},
		{
			// Z-characters 6 and 7 of A2 are the escape and a new-line, so the
			// table proper starts at 8 (S 3.5.3).
			name:  "the printable part of A2",
			chars: shifted(zcharShiftA2, all(8)),
			want:  "0123456789.,!?_#'\"/\\-:()",
		},
		{
			name:  "Z-character 0 is a space (S 3.5.1)",
			chars: []uint8{6, 0, 6},
			want:  "a a",
		},
		{
			name:  "Z-character 7 of A2 is a new-line (S 3.5.3)",
			chars: []uint8{zcharShiftA2, 7},
			want:  "\n",
		},
		{
			// S 3.2.3: in Version 3 a shift changes the alphabet for one
			// character only. Versions 1 and 2 would lock here (S 3.2.2).
			name:  "a shift affects only the next character",
			chars: []uint8{zcharShiftA1, 6, 6},
			want:  "Aa",
		},
		{
			name:  "a shift to A2 affects only the next character",
			chars: []uint8{zcharShiftA2, 18, 6},
			want:  ".a",
		},
		{
			// S 3.2.4: an indefinite sequence of shift characters is legal but
			// prints nothing. Only the last one is in force.
			name:  "a run of shifts prints nothing",
			chars: []uint8{zcharShiftA1, zcharShiftA1, zcharShiftA2, 18},
			want:  ".",
		},
		{
			name:  "a shift at the very end of a string prints nothing (S 3.2.4)",
			chars: []uint8{6, 6, zcharShiftA1},
			want:  "aa",
		},
		{
			name:  "the padding a short final word carries prints nothing (S 3.6)",
			chars: []uint8{6},
			want:  "a",
		},
		{
			// S 3.4: Z-character 6 in A2 means the next two Z-characters give
			// a ten-bit ZSCII code, top 5 bits first. 1<<5|4 is 36, '$'.
			name:  "the ten-bit ZSCII escape",
			chars: []uint8{zcharShiftA2, zcharEscape, 1, 4},
			want:  "$",
		},
		{
			name:  "an escape sequence does not lock the alphabet",
			chars: []uint8{zcharShiftA2, zcharEscape, 1, 4, 6},
			want:  "$a",
		},
		{
			// Only A2 gives Z-character 6 its escape meaning; in A0 and A1 it
			// is an ordinary letter (S 3.4, S 3.5.3).
			name:  "Z-character 6 outside A2 is a letter",
			chars: []uint8{zcharEscape, zcharShiftA1, zcharEscape},
			want:  "aA",
		},
		{
			// S 3.6.1: a string may end with a multi-Z-character construction
			// incomplete, and the partial construction is simply ignored.
			name:  "an escape truncated after the top half is ignored",
			chars: []uint8{zcharShiftA2, zcharEscape, 1},
			want:  "",
		},
		{
			name:  "an escape introduced at the very end of a string is ignored (S 3.6.1)",
			chars: []uint8{6, zcharShiftA2, zcharEscape},
			want:  "a",
		},
		{
			// A ten-bit escape may name a code Version 3 does not define, in
			// which case nothing is printed (S 3.8, Appendix A).
			name:  "an escape naming an undefined ZSCII code prints nothing",
			chars: []uint8{6, zcharShiftA2, zcharEscape, 31, 31, 6},
			want:  "aa",
		},
		{
			name:  "an escape naming an extra character (S 3.8.5)",
			chars: []uint8{zcharShiftA2, zcharEscape, 4, 27},
			want:  "ä",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeForTest(t, tt.chars...)
			if err != nil {
				t.Fatalf("decodeZText() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("decodeZText() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestUnpackZCharsEndOfStringBit covers S 3.2: the top bit is set only on the
// last word of the text and so marks the end.
func TestUnpackZCharsEndOfStringBit(t *testing.T) {
	// Three words; only the second carries the end bit, so the third must not
	// be read at all.
	data := []byte{
		0x18, 0xc4, // 6, 6, 4    no end bit
		0xe4, 0xc6, // 25, 6, 6   end bit set
		0x1c, 0xe7, // never reached
	}
	chars, size, err := unpackZChars(data)
	if err != nil {
		t.Fatalf("unpackZChars() error = %v, want nil", err)
	}
	if size != 4 {
		t.Errorf("unpackZChars() consumed %d byte(s), want 4", size)
	}
	want := []uint8{6, 6, 4, 25, 6, 6}
	if len(chars) != len(want) {
		t.Fatalf("unpackZChars() = %v, want %v", chars, want)
	}
	for i := range want {
		if chars[i] != want[i] {
			t.Fatalf("unpackZChars() = %v, want %v", chars, want)
		}
	}

	// The trailing 4 of the first word shifts the 25 that opens the second, so
	// a single-character shift spans the word boundary (S 3.2.3).
	text, err := decodeZText(chars, 0, nil)
	if err != nil {
		t.Fatalf("decodeZText() error = %v, want nil", err)
	}
	if text != "aaTaa" {
		t.Errorf("decodeZText() = %q, want %q", text, "aaTaa")
	}
}

func TestUnpackZCharsRejectsUnterminatedText(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: nil},
		{name: "a single byte cannot form a word", data: []byte{0x80}},
		{name: "no end bit anywhere", data: []byte{0x18, 0xc5, 0x18, 0xc5}},
		{
			// The end bit is on a byte that is never part of a complete word.
			name: "truncated final word",
			data: []byte{0x18, 0xc5, 0x80},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chars, size, err := unpackZChars(tt.data)
			if err == nil {
				t.Fatalf("unpackZChars() = %v, %d, nil; want an error", chars, size)
			}
			assertTextError(t, err)
		})
	}
}

func TestDecodeStringAt(t *testing.T) {
	f := newTextFixture(t)
	// "hi" followed by a second string, so that the returned address can be
	// used to keep decoding.
	first := f.putString(13, 14)
	second := f.putString(20, 16)
	m := f.memory()

	text, next, err := decodeStringAt(m, first)
	if err != nil {
		t.Fatalf("decodeStringAt() error = %v, want nil", err)
	}
	if text != "hi" {
		t.Errorf("decodeStringAt() = %q, want %q", text, "hi")
	}
	if next != second {
		t.Errorf("decodeStringAt() next = 0x%04x, want 0x%04x", next, second)
	}

	text, _, err = decodeStringAt(m, next)
	if err != nil {
		t.Fatalf("decodeStringAt() error = %v, want nil", err)
	}
	if text != "ok" {
		t.Errorf("decodeStringAt() = %q, want %q", text, "ok")
	}
}

// TestDecodeStringAtPacked covers S 1.2.3: print_paddr names a string by a
// packed address, which in Versions 1 to 3 is half its byte address.
func TestDecodeStringAtPacked(t *testing.T) {
	f := newTextFixture(t)
	addr := f.putString(24, 10, 17, 17, 20)
	m := f.memory()

	text, next, err := decodeStringAtPacked(m, uint16(addr/packedScaleV3))
	if err != nil {
		t.Fatalf("decodeStringAtPacked() error = %v, want nil", err)
	}
	if text != "sello" {
		t.Errorf("decodeStringAtPacked() = %q, want %q", text, "sello")
	}
	if next != addr+4 {
		t.Errorf("decodeStringAtPacked() next = 0x%04x, want 0x%04x", next, addr+4)
	}
}

// TestAbbreviations covers S 3.3: Z-characters 1, 2 and 3 select the block of
// 32 abbreviations to print, and the next Z-character selects the entry.
func TestAbbreviations(t *testing.T) {
	f := newTextFixture(t)
	// Entries 0, 32 and 64 are the first entry of each block, and entry 95 is
	// the last entry of the table.
	f.setAbbreviation(0, f.putString(zcharShiftA1, 6, 6))               // "Aa"
	f.setAbbreviation(32, f.putString(zcharShiftA2, 18))                // "."
	f.setAbbreviation(64, f.putString(0))                               // " "
	f.setAbbreviation(95, f.putString(zcharShiftA2, zcharEscape, 1, 4)) // "$"
	m := f.memory()

	tests := []struct {
		name  string
		chars []uint8
		want  string
	}{
		{name: "block 1 entry 0", chars: []uint8{1, 0}, want: "Aa"},
		{name: "block 2 entry 0 is table entry 32", chars: []uint8{2, 0}, want: "."},
		{name: "block 3 entry 0 is table entry 64", chars: []uint8{3, 0}, want: " "},
		{name: "block 3 entry 31 is table entry 95", chars: []uint8{3, 31}, want: "$"},
		{name: "abbreviations sit inside ordinary text", chars: []uint8{6, 1, 0, 6}, want: "aAaa"},
		{
			// S 3.2.3: the shift is spent on the abbreviation's Z-character,
			// and the abbreviation's own text starts again in A0 (S 3.2.1).
			name:  "a shift before an abbreviation does not leak into it",
			chars: []uint8{zcharShiftA1, 1, 0, 6},
			want:  "Aaa",
		},
		{
			// S 3.6.1: a string may end with the abbreviation incomplete.
			name:  "an abbreviation truncated at the end of a string is ignored",
			chars: []uint8{6, 6, 1},
			want:  "aa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := f.putString(tt.chars...)
			m := f.memory()
			got, _, err := decodeStringAt(m, addr)
			if err != nil {
				t.Fatalf("decodeStringAt() error = %v, want nil", err)
			}
			if got != tt.want {
				t.Errorf("decodeStringAt() = %q, want %q", got, tt.want)
			}
		})
	}

	// Direct lookups, so that the index arithmetic is pinned independently of
	// the decoder.
	for _, tt := range []struct {
		index uint16
		want  string
	}{{0, "Aa"}, {32, "."}, {64, " "}, {95, "$"}} {
		got, err := abbreviationText(m, tt.index)
		if err != nil {
			t.Fatalf("abbreviationText(%d) error = %v, want nil", tt.index, err)
		}
		if got != tt.want {
			t.Errorf("abbreviationText(%d) = %q, want %q", tt.index, got, tt.want)
		}
	}
}

// TestAbbreviationsAreNotRecursive covers S 3.3.1: an abbreviation string must
// not itself use abbreviations. Enforcing it also bounds the decoder, so a
// table entry that refers to itself cannot exhaust the stack.
func TestAbbreviationsAreNotRecursive(t *testing.T) {
	t.Run("an abbreviation using an abbreviation", func(t *testing.T) {
		f := newTextFixture(t)
		f.setAbbreviation(1, f.putString(6, 6))    // "aa"
		f.setAbbreviation(0, f.putString(6, 1, 1)) // "a" then abbreviation 1
		addr := f.putString(1, 0)
		m := f.memory()

		if got, _, err := decodeStringAt(m, addr); err == nil {
			t.Fatalf("decodeStringAt() = %q, want an error", got)
		} else {
			assertTextError(t, err)
		}
	})

	t.Run("an abbreviation naming itself", func(t *testing.T) {
		f := newTextFixture(t)
		self := f.putString(1, 0)
		f.setAbbreviation(0, self)
		m := f.memory()

		if got, _, err := decodeStringAt(m, self); err == nil {
			t.Fatalf("decodeStringAt() = %q, want an error", got)
		} else {
			assertTextError(t, err)
		}
	})
}

func TestAbbreviationErrors(t *testing.T) {
	t.Run("an index outside the table", func(t *testing.T) {
		// The decoder cannot build such an index - 32(z-1)+x is at most 95 -
		// so the bound is checked directly.
		m := newTextFixture(t).memory()
		for _, index := range []uint16{abbreviationCount, 0xffff} {
			if got, err := abbreviationText(m, index); err == nil {
				t.Errorf("abbreviationText(%d) = %q, want an error", index, got)
			} else {
				assertTextError(t, err)
			}
		}
	})

	t.Run("a table entry pointing outside the story", func(t *testing.T) {
		f := newTextFixture(t)
		// The largest word address, 0xffff, expands to 0x1fffe (S 1.2.2),
		// which is far beyond the end of this story.
		f.putWord(textAbbrevTable, 0xffff)
		addr := f.putString(1, 0)
		m := f.memory()

		_, _, err := decodeStringAt(m, addr)
		if err == nil {
			t.Fatal("decodeStringAt() = nil, want an error")
		}
		if !errors.Is(err, ErrMemoryAccess) {
			t.Errorf("errors.Is(err, ErrMemoryAccess) = false; err = %v", err)
		}
	})

	t.Run("a story with no abbreviations table", func(t *testing.T) {
		// S 3.3: a story that uses no abbreviations may leave the header entry
		// zero. Using one anyway must be reported, not followed to address 0.
		h := validTestHeader()
		h.abbreviations = 0
		data := h.build()
		story, err := LoadStory(data)
		if err != nil {
			t.Fatalf("LoadStory() error = %v, want nil", err)
		}
		m := newMemory(story)

		if got, err := abbreviationText(m, 0); err == nil {
			t.Errorf("abbreviationText(0) = %q, want an error", got)
		} else {
			assertTextError(t, err)
		}
	})
}

func TestDecodeStringAtMalformed(t *testing.T) {
	t.Run("a string that never terminates", func(t *testing.T) {
		f := newTextFixture(t)
		// High memory in the fixture is all zero bytes, so no word from here
		// to the end of the story has the end-of-string bit set.
		m := f.memory()

		got, _, err := decodeStringAt(m, textStringBase)
		if err == nil {
			t.Fatalf("decodeStringAt() = %q, want an error", got)
		}
		assertTextError(t, err)
	})

	t.Run("a string starting past the end of the story", func(t *testing.T) {
		m := newTextFixture(t).memory()

		for _, addr := range []uint32{textStoryEnd, textStoryEnd + 1, 0xfffe} {
			_, _, err := decodeStringAt(m, addr)
			if err == nil {
				t.Fatalf("decodeStringAt(0x%04x) = nil, want an error", addr)
			}
			// The string does not begin inside memory at all, so this is a
			// memory fault rather than malformed text.
			if !errors.Is(err, ErrMemoryAccess) {
				t.Errorf("errors.Is(err, ErrMemoryAccess) = false; err = %v", err)
			}
		}
	})

	t.Run("a truncated final word", func(t *testing.T) {
		f := newTextFixture(t)
		// A word at the last two bytes of the story with no end bit; the next
		// word would straddle the end.
		f.putWord(textStoryEnd-2, 0x18c5)
		m := f.memory()

		_, _, err := decodeStringAt(m, textStoryEnd-2)
		if err == nil {
			t.Fatal("decodeStringAt() = nil, want an error")
		}
		assertTextError(t, err)
	})

	t.Run("a string whose last word is the last word of the story", func(t *testing.T) {
		f := newTextFixture(t)
		f.putStringAt(textStoryEnd-2, 6, 6, 6)
		m := f.memory()

		text, next, err := decodeStringAt(m, textStoryEnd-2)
		if err != nil {
			t.Fatalf("decodeStringAt() error = %v, want nil", err)
		}
		if text != "aaa" {
			t.Errorf("decodeStringAt() = %q, want %q", text, "aaa")
		}
		if next != textStoryEnd {
			t.Errorf("decodeStringAt() next = 0x%04x, want 0x%04x", next, uint32(textStoryEnd))
		}
	})
}

// TestDecodeZork1 decodes real Version 3 text. Every expectation below was
// worked out from the story's own bytes before the decoder was written:
//
//   - The header gives the object table at $03e6 and the abbreviations table
//     at $01f0. Object entries follow 31 words of property defaults (S 12.2),
//     so object n starts at $03e6+62+9(n-1); its last word is the address of
//     its property table, which begins with a text-length byte and then the
//     short name as a Z-string (S 12.4).
//
//   - Object 13's name is the words $4460 $1a83 $2809 $6a68 $a4a5, that is the
//     Z-characters 4,17 1,6 1,10 1,0 4,9 10,6 9,5,5. Z-character 4 shifts to
//     A1 so 17 is 'L' (S 3.2.3); the three pairs beginning 1 are abbreviations
//     6, 10 and 0 (S 3.3), which hold "and ", "of " and "the "; 4,9 is 'D' and
//     10,6,9 are "ead" in A0; the trailing 5,5 are padding and print nothing
//     (S 3.2.4, S 3.6).
//
//   - The dictionary is at $3899 with three word separators, so its entries
//     start at $38a0 (S 13.2). Entry 0 is $14c1 $936a, that is the
//     Z-characters 5,6 1,4 27,10: 5 shifts to A2 and 6 there is the ten-bit
//     ZSCII escape (S 3.4), whose halves 1 and 4 give ZSCII 36, '$'; then 27
//     and 10 are 'v' and 'e' in A0.
func TestDecodeZork1(t *testing.T) {
	const path = "testdata/stories/zork1-r119-880429.z3"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("story fixture unavailable: %v", err)
	}
	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory(%s) error = %v, want nil", path, err)
	}
	m := newMemory(story)

	t.Run("object short names", func(t *testing.T) {
		tests := []struct {
			object uint32
			want   string
		}{
			{object: 1, want: "forest"},
			{object: 2, want: "Temple"},
			{object: 3, want: "Coal Mine"},
			{object: 5, want: "Up a Tree"},
			{object: 13, want: "Land of the Dead"},
			{object: 180, want: "tan label"},
		}
		for _, tt := range tests {
			entry := story.objectTable + propertyDefaultsSizeV3 + 9*(tt.object-1)
			propTable, err := m.readWord(entry + 7)
			if err != nil {
				t.Fatalf("object %d: reading the property table address: %v", tt.object, err)
			}
			length, err := m.readByte(uint32(propTable))
			if err != nil {
				t.Fatalf("object %d: reading the short name length: %v", tt.object, err)
			}
			got, next, err := decodeStringAt(m, uint32(propTable)+1)
			if err != nil {
				t.Fatalf("object %d: decodeStringAt() error = %v, want nil", tt.object, err)
			}
			if got != tt.want {
				t.Errorf("object %d short name = %q, want %q", tt.object, got, tt.want)
			}
			// S 12.4: the length byte counts the words of the short name, so
			// it must agree with where decoding stopped.
			if wantNext := uint32(propTable) + 1 + uint32(length)*zstringWordSize; next != wantNext {
				t.Errorf("object %d: decoding stopped at 0x%04x, want 0x%04x", tt.object, next, wantNext)
			}
		}
	})

	t.Run("abbreviations", func(t *testing.T) {
		for index, want := range map[uint16]string{
			0: "the ", 1: "The ", 2: "You ", 3: ", ", 6: "and ", 10: "of ",
		} {
			got, err := abbreviationText(m, index)
			if err != nil {
				t.Fatalf("abbreviationText(%d) error = %v, want nil", index, err)
			}
			if got != want {
				t.Errorf("abbreviationText(%d) = %q, want %q", index, got, want)
			}
		}
	})

	t.Run("dictionary words", func(t *testing.T) {
		separators, err := m.readByte(story.dictionary)
		if err != nil {
			t.Fatalf("reading the separator count: %v", err)
		}
		entryLength, err := m.readByte(story.dictionary + 1 + uint32(separators))
		if err != nil {
			t.Fatalf("reading the entry length: %v", err)
		}
		count, err := m.readWord(story.dictionary + 2 + uint32(separators))
		if err != nil {
			t.Fatalf("reading the entry count: %v", err)
		}
		base := story.dictionary + 4 + uint32(separators)
		if separators != 3 || entryLength != 7 || count != 684 || base != 0x38a0 {
			t.Fatalf("dictionary header = %d separators, %d bytes per entry, %d entries at 0x%04x; want 3, 7, 684, 0x38a0",
				separators, entryLength, count, base)
		}

		tests := []struct {
			entry uint32
			want  string
		}{
			// The '$' can only come from a ten-bit escape: it is not in any
			// alphabet (S 3.5.3).
			{entry: 0, want: "$ve"},
			{entry: 1, want: "."},
			{entry: 2, want: ","},
			{entry: uint32(count) - 1, want: "zzmgck"},
		}
		for _, tt := range tests {
			addr := base + tt.entry*uint32(entryLength)
			got, next, err := decodeStringAt(m, addr)
			if err != nil {
				t.Fatalf("dictionary entry %d: decodeStringAt() error = %v, want nil", tt.entry, err)
			}
			if got != tt.want {
				t.Errorf("dictionary entry %d = %q, want %q", tt.entry, got, tt.want)
			}
			// S 13.3: a Version 3 entry holds four bytes of encoded text,
			// which is six Z-characters (S 3.7).
			if next != addr+4 {
				t.Errorf("dictionary entry %d: decoding stopped at 0x%04x, want 0x%04x", tt.entry, next, addr+4)
			}
		}
	})
}

// FuzzDecodeZString asserts that arbitrary bytes decoded as Version 3 text
// never panic, always terminate, and never produce more output than their
// input can account for.
func FuzzDecodeZString(f *testing.F) {
	f.Add([]byte{0x80, 0x00})
	f.Add([]byte{0x18, 0xc5, 0x9b, 0x26})
	f.Add([]byte{0x14, 0xc1, 0x93, 0x6a}) // "$ve", a ten-bit escape
	f.Add([]byte{0x44, 0x60, 0x1a, 0x83, 0x28, 0x09, 0x6a, 0x68, 0xa4, 0xa5})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00})
	f.Add(make([]byte, 256))

	// An expander that always succeeds, so that fuzzing reaches the rest of
	// the decoder instead of stopping at the first abbreviation. It emits two
	// bytes for the two Z-characters an abbreviation consumes, which keeps the
	// output bound below intact.
	expand := func(index uint16) (string, error) {
		if index >= abbreviationCount {
			return "", textErrorf(0, "abbreviation %d is out of range", index)
		}
		return "<>", nil
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if chars, size, err := unpackZChars(data); err == nil {
			if size <= 0 || size > len(data) || size%zstringWordSize != 0 {
				t.Fatalf("unpackZChars() consumed %d of %d byte(s)", size, len(data))
			}
			if want := size / zstringWordSize * zcharsPerWord; len(chars) != want {
				t.Fatalf("unpackZChars() returned %d Z-character(s) from %d byte(s), want %d", len(chars), size, want)
			}
			for _, z := range chars {
				if z > zcharMask {
					t.Fatalf("Z-character %d does not fit in 5 bits", z)
				}
			}
			text, err := decodeZText(chars, 0, expand)
			if err != nil {
				t.Fatalf("decodeZText() error = %v, want nil with a total expander", err)
			}
			// Every Z-character yields at most one character, and no character
			// this package can print is longer than utf8.UTFMax bytes.
			if len(text) > utf8.UTFMax*len(chars) {
				t.Fatalf("decodeZText() produced %d byte(s) from %d Z-character(s)", len(text), len(chars))
			}
		}

		// The same input read through a story: the fuzz data fills the
		// abbreviations table and high memory, so abbreviation entries point
		// wherever the fuzzer chooses and strings run into whatever follows.
		// The header, the dictionary and the tables validated at load time are
		// left intact so that the story always loads.
		image := validTestHeader().build()
		n := copy(image[textAbbrevTable:textAbbrevEnd], data)
		if n < len(data) {
			copy(image[textStringBase:textStoryEnd], data[n:])
		}
		story, err := LoadStory(image)
		if err != nil {
			t.Fatalf("LoadStory() error = %v, want nil", err)
		}
		m := newMemory(story)

		// A string holds at most three Z-characters per word of the story, and
		// each Z-character prints at most one character or expands to at most
		// one abbreviation, which is itself a string (S 3.3.1 forbids going
		// deeper). That squares the bound, and every printed character is at
		// most utf8.UTFMax bytes.
		maxChars := uint64(m.size()) / zstringWordSize * zcharsPerWord
		maxBytes := utf8.UTFMax * maxChars * maxChars

		for addr := uint32(textAbbrevTable); addr < textStoryEnd; addr += 0x40 {
			text, next, err := decodeStringAt(m, addr)
			if err != nil {
				if !errors.Is(err, ErrInvalidText) && !errors.Is(err, ErrMemoryAccess) {
					t.Fatalf("decodeStringAt(0x%04x) error = %v, want it to be classified", addr, err)
				}
				continue
			}
			if next <= addr || next > m.size() {
				t.Fatalf("decodeStringAt(0x%04x) next = 0x%04x, outside (0x%04x, 0x%04x]", addr, next, addr, m.size())
			}
			if uint64(len(text)) > maxBytes {
				t.Fatalf("decodeStringAt(0x%04x) produced %d byte(s), more than the %d the story can encode",
					addr, len(text), maxBytes)
			}
		}
	})
}

// assertTextError checks that a decoding failure is classified and carries
// context.
func assertTextError(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, ErrInvalidText) {
		t.Errorf("errors.Is(err, ErrInvalidText) = false; err = %v", err)
	}
	var textErr *TextError
	if !errors.As(err, &textErr) {
		t.Fatalf("errors.As(err, *TextError) = false; err = %v", err)
	}
	if textErr.Detail == "" {
		t.Error("TextError.Detail is empty; errors must carry context")
	}
}
