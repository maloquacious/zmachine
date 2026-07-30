package zmachine

import (
	"errors"
	"testing"
)

// TestEncodeDictionaryWord covers S 3.7 and S 13.3: a dictionary word is
// always exactly six Z-characters in Version 3, lower-cased, padded with
// Z-character 5 and truncated without regard for whether a multi-Z-character
// construction survives intact.
func TestEncodeDictionaryWord(t *testing.T) {
	// Z-characters are packed three to a word, most significant first, and the
	// top bit of the second word ends the string (S 3.2).
	pack := func(c ...uint8) [dictionaryTextBytesV3]byte {
		first := uint16(c[0])<<10 | uint16(c[1])<<5 | uint16(c[2])
		second := uint16(c[3])<<10 | uint16(c[4])<<5 | uint16(c[5]) | zstringEndBit
		return [dictionaryTextBytesV3]byte{uint8(first >> 8), uint8(first), uint8(second >> 8), uint8(second)}
	}

	tests := []struct {
		name string
		word string
		want [dictionaryTextBytesV3]byte
	}{
		{
			// S 3.7 gives this example: "i" is 14 followed by pad characters.
			// 'i' is the ninth letter, so its Z-character is 6+8 = 14.
			name: "one letter is padded with Z-character 5",
			word: "i",
			want: pack(14, 5, 5, 5, 5, 5),
		},
		{
			name: "exactly six Z-characters",
			word: "mailbo",
			want: pack(18, 6, 14, 17, 7, 20),
		},
		{
			// The seventh Z-character onwards is dropped, which is why a plural
			// still matches the dictionary entry for its singular.
			name: "more than six Z-characters is truncated",
			word: "mailboxes",
			want: pack(18, 6, 14, 17, 7, 20),
		},
		{
			// 'A' is lower-cased before encoding, so alphabet A1 is never
			// needed (S 3.7).
			name: "upper case is lower-cased",
			word: "Mailbo",
			want: pack(18, 6, 14, 17, 7, 20),
		},
		{
			// The digits live in A2, reached by the shift Z-character 5, so
			// each of them costs two Z-characters (S 3.5.3). '1' is the fourth
			// entry of A2, that is Z-character 9.
			name: "characters from A2",
			word: "a1b",
			want: pack(6, 5, 9, 7, 5, 5),
		},
		{
			// A period is A2 position 12, that is Z-character 18.
			name: "a word separator is a word",
			word: ".",
			want: pack(5, 18, 5, 5, 5, 5),
		},
		{
			// '$' is in neither alphabet, so it becomes a ten-bit ZSCII escape:
			// shift to A2, Z-character 6, then the two halves of ZSCII 36
			// (S 3.4). That leaves room for two more letters only.
			name: "a ten-bit escape",
			word: "$verify",
			want: pack(5, 6, 1, 4, 27, 10),
		},
		{
			// S 3.7: "any multi-Z-character constructions should be left
			// incomplete (rather than omitted) if there's no room to finish
			// them", so the escape below is cut off after its first half.
			name: "an escape left incomplete",
			word: "abcde$",
			want: pack(6, 7, 8, 9, 10, 5),
		},
		{
			// S 3.5.1: Z-character 0 is a space. A space never divides a typed
			// word (S 13.6.1), but a dictionary word may contain one (S 13.3).
			name: "a space",
			word: "a b",
			want: pack(6, 0, 7, 5, 5, 5),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeDictionaryWord([]uint8(tt.word))
			if got != tt.want {
				t.Errorf("encodeDictionaryWord(%q) = % x, want % x", tt.word, got, tt.want)
			}
		})
	}
}

// TestEncodeDictionaryWordRoundTrip checks the encoder against the decoder of
// S 3, so that the two halves of the text machinery cannot drift apart. Only
// words of six Z-characters or fewer round trip, since encoding is lossy past
// that point.
func TestEncodeDictionaryWordRoundTrip(t *testing.T) {
	for _, word := range []string{"i", "open", "north", "sword", "a1b", "."} {
		encoded := encodeDictionaryWord([]uint8(word))
		chars, _, err := unpackZChars(encoded[:])
		if err != nil {
			t.Fatalf("unpackZChars(%q) error = %v", word, err)
		}
		got, err := decodeZText(chars, 0, nil)
		if err != nil {
			t.Fatalf("decodeZText(%q) error = %v", word, err)
		}
		if got != word {
			t.Errorf("round trip of %q = %q", word, got)
		}
	}
}

// TestDictionaryLookup covers S 13.6.2 and S 15, read: a word is encoded and
// searched for in the dictionary, and a word that is not there is recorded as
// address 0.
func TestDictionaryLookup(t *testing.T) {
	f := newObjectFixture(t).words("open", "north", "mailbox", "sword", ".", ",")
	m := f.memory()
	table, err := m.storyDictionary()
	if err != nil {
		t.Fatalf("storyDictionary() error = %v", err)
	}
	if table.count != 6 {
		t.Fatalf("dictionary holds %d entries, want 6", table.count)
	}
	if len(table.separators) != fixtureSeparatorCount {
		t.Errorf("dictionary has %d word separators, want %d", len(table.separators), fixtureSeparatorCount)
	}

	// Every word written into the fixture must be found, and the address must
	// be one of the entry addresses.
	for _, word := range []string{"open", "north", "mailbox", "sword", ".", ","} {
		addr, err := m.lookupDictionaryWord(table, encodeDictionaryWord([]uint8(word)))
		if err != nil {
			t.Fatalf("lookupDictionaryWord(%q) error = %v", word, err)
		}
		if addr < fixtureDictEntries || (uint32(addr)-fixtureDictEntries)%fixtureEntryLength != 0 {
			t.Errorf("lookupDictionaryWord(%q) = 0x%04x, which is not an entry address", word, addr)
		}
		text, err := m.readEntryText(uint32(addr))
		if err != nil {
			t.Fatalf("readEntryText() error = %v", err)
		}
		encoded := encodeDictionaryWord([]uint8(word))
		want := uint32(encoded[0])<<24 | uint32(encoded[1])<<16 | uint32(encoded[2])<<8 | uint32(encoded[3])
		if text != want {
			t.Errorf("entry found for %q holds 0x%08x, want 0x%08x", word, text, want)
		}
	}

	// A word truncated to the same six Z-characters matches the same entry.
	mailbox, err := m.lookupDictionaryWord(table, encodeDictionaryWord([]uint8("mailbox")))
	if err != nil {
		t.Fatalf("lookupDictionaryWord() error = %v", err)
	}
	plural, err := m.lookupDictionaryWord(table, encodeDictionaryWord([]uint8("mailboxes")))
	if err != nil {
		t.Fatalf("lookupDictionaryWord() error = %v", err)
	}
	if plural != mailbox {
		t.Errorf("lookupDictionaryWord(\"mailboxes\") = 0x%04x, want the entry for \"mailbox\" at 0x%04x", plural, mailbox)
	}

	// S 15, read: a word not in the dictionary is recorded as 0.
	for _, word := range []string{"zzyzx", "opened", "sworn", ""} {
		addr, err := m.lookupDictionaryWord(table, encodeDictionaryWord([]uint8(word)))
		if err != nil {
			t.Fatalf("lookupDictionaryWord(%q) error = %v", word, err)
		}
		if addr != 0 {
			t.Errorf("lookupDictionaryWord(%q) = 0x%04x, want 0", word, addr)
		}
	}
}

// TestDictionaryHeaderIsValidated covers S 13.2: the entry length must be at
// least 4 in Version 3, the entry count must not be negative, and the entries
// must lie inside the story.
//
// LoadStory already refuses a story whose own dictionary breaks any of these,
// so the malformed headers below are written into scratch memory and read from
// there: the reader must not depend on having been handed a validated table.
func TestDictionaryHeaderIsValidated(t *testing.T) {
	const scratch = fixtureTextBuffer

	tests := []struct {
		name   string
		header []byte
		target error
	}{
		{
			// No separators, an entry length of 3, no entries.
			name:   "entry length below the Version 3 minimum",
			header: []byte{0, 3, 0, 0},
			target: ErrExecutionFault,
		},
		{
			// An entry count of -1 marks an unsorted user dictionary, which
			// Version 3 has no instruction to supply (S 15, tokenise).
			name:   "a negative entry count",
			header: []byte{0, 7, 0xff, 0xff},
			target: ErrExecutionFault,
		},
		{
			// 32767 entries of 7 bytes cannot fit in a 4K story.
			name:   "entries beyond the end of the story",
			header: []byte{0, 7, 0x7f, 0xff},
			target: ErrMemoryAccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newObjectFixture(t).at(scratch, tt.header...).memory()
			if _, err := m.readDictionary(scratch); !errors.Is(err, tt.target) {
				t.Errorf("readDictionary() error = %v, want one wrapping %v", err, tt.target)
			}
		})
	}

	t.Run("a header past the end of the story", func(t *testing.T) {
		m := newObjectFixture(t).memory()
		if _, err := m.readDictionary(m.size()); !errors.Is(err, ErrMemoryAccess) {
			t.Errorf("readDictionary() error = %v, want one wrapping ErrMemoryAccess", err)
		}
	})
}

// TestZork1Dictionary checks the encoder and the lookup against a real Version
// 3 story.
//
// The expected addresses were read out of the story image independently of the
// code under test, by following S 13.2: the dictionary is at the address in the
// header word at $08 ($3899), it declares three word separators - ZSCII 46, 44
// and 34, that is '.', ',' and '"' - an entry length of 7 and 684 entries, so
// entry n lies at $38a0 + 7n. Encoding each word below with the rules of S 3.7
// and searching the sorted entries gives exactly the addresses listed. The
// entry for "mailbox" is also what "mailboxes" must find, because both truncate
// to the same six Z-characters.
func TestZork1Dictionary(t *testing.T) {
	m := newMemory(loadZork1(t))
	table, err := m.storyDictionary()
	if err != nil {
		t.Fatalf("storyDictionary() error = %v", err)
	}

	if table.addr != 0x3899 || table.entries != 0x38a0 {
		t.Fatalf("dictionary at 0x%04x with entries at 0x%04x, want 0x3899 and 0x38a0", table.addr, table.entries)
	}
	if table.entryLength != 7 || table.count != 684 {
		t.Fatalf("dictionary has %d entries of %d bytes, want 684 of 7", table.count, table.entryLength)
	}
	for _, code := range []uint8{'.', ',', '"'} {
		if !table.isSeparator(code) {
			t.Errorf("%q is not a word separator, but S 13.2 makes it one in this story", code)
		}
	}
	if table.isSeparator(' ') {
		t.Errorf("a space is a word separator, which S 13.2.1 forbids")
	}

	tests := []struct {
		word string
		want uint16
	}{
		{word: "open", want: 0x43b3},
		{word: "mailbox", want: 0x4263},
		{word: "mailboxes", want: 0x4263},
		{word: "north", want: 0x4343},
		{word: "take", want: 0x4898},
		{word: "lamp", want: 0x4175},
		{word: "sword", want: 0x488a},
		{word: "leaflet", want: 0x419f},
		{word: "xyzzy", want: 0x4b0e},
		// The first entry in the dictionary is the debugging verb "$ve",
		// whose '$' takes a ten-bit escape.
		{word: "$ve", want: 0x38a0},
		{word: ".", want: 0x38a7},
		{word: ",", want: 0x38ae},
		// Not in Zork I's dictionary.
		{word: "zzyzx", want: 0},
	}

	for _, tt := range tests {
		got, err := m.lookupDictionaryWord(table, encodeDictionaryWord([]uint8(tt.word)))
		if err != nil {
			t.Fatalf("lookupDictionaryWord(%q) error = %v", tt.word, err)
		}
		if got != tt.want {
			t.Errorf("lookupDictionaryWord(%q) = 0x%04x, want 0x%04x", tt.word, got, tt.want)
		}
	}
}
