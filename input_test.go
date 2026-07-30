package zmachine

import (
	"context"
	"testing"
)

// sreadCode is a single sread instruction naming the fixture's two buffers
// (S 15, read).
func sreadCode() []byte {
	return encodeVar(countVAR, 0x04, largeOp(fixtureTextBuffer), largeOp(fixtureParseBuffer))
}

// inputFixture builds a story with a text buffer, a parse buffer, a small
// dictionary and one sread instruction.
func inputFixture(t *testing.T, textSize, maxWords uint8) *objectFixture {
	t.Helper()
	return newObjectFixture(t).
		object(1, testObject{name: "West of House"}).
		words("open", "mailbox", "the", "north", ".", ",").
		buffers(textSize, maxWords).
		code(sreadCode()...)
}

// runSRead executes one sread with the given line of input.
func runSRead(t *testing.T, f *objectFixture, line string) *Machine {
	t.Helper()
	m := f.machine()
	m.input, m.hasInput = line, true
	mustStep(t, m)
	return m
}

// textBufferBytes returns the bytes of the text buffer, excluding its size
// byte, up to and including the zero terminator (S 15, read).
func textBufferBytes(t *testing.T, m *memory) []byte {
	t.Helper()
	size, err := m.readByte(fixtureTextBuffer)
	if err != nil {
		t.Fatalf("readByte() error = %v", err)
	}
	out := make([]byte, 0, size)
	for i := uint32(0); i < uint32(size); i++ {
		b, err := m.readByte(fixtureTextBuffer + textBufferOffsetV3 + i)
		if err != nil {
			t.Fatalf("readByte() error = %v", err)
		}
		out = append(out, b)
		if b == 0 {
			break
		}
	}
	return out
}

// parsedWord is one 4-byte block of the parse buffer (S 15, read).
type parsedWord struct {
	entry    uint16
	length   uint8
	position uint8
}

// parseBufferWords returns the blocks the parse buffer holds.
func parseBufferWords(t *testing.T, m *memory) []parsedWord {
	t.Helper()
	count, err := m.readByte(fixtureParseBuffer + 1)
	if err != nil {
		t.Fatalf("readByte() error = %v", err)
	}
	words := make([]parsedWord, 0, count)
	for i := uint32(0); i < uint32(count); i++ {
		block := uint32(fixtureParseBuffer) + parseHeaderSize + i*parseEntrySize
		entry, err := m.readWord(block)
		if err != nil {
			t.Fatalf("readWord() error = %v", err)
		}
		length, err := m.readByte(block + 2)
		if err != nil {
			t.Fatalf("readByte() error = %v", err)
		}
		position, err := m.readByte(block + 3)
		if err != nil {
			t.Fatalf("readByte() error = %v", err)
		}
		words = append(words, parsedWord{entry: entry, length: length, position: position})
	}
	return words
}

// TestSReadTextBufferLayout covers the Version 3 text buffer of S 15, read:
// byte 0 holds the maximum, the text is stored from byte 1 onward "with a zero
// terminator (but without any other terminator, such as a carriage return
// code)", and it is reduced to lower case.
//
// Version 5 moves the text to byte 2 and puts a length in byte 1 instead, so
// getting this layout right is version-specific.
func TestSReadTextBufferLayout(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{name: "a plain command", line: "open mailbox", want: "open mailbox"},
		{name: "reduced to lower case", line: "OPEN MailBox", want: "open mailbox"},
		{name: "an empty line", line: "", want: ""},
		{name: "a trailing newline is not stored", line: "open\n", want: "open"},
		{name: "a carriage return is not stored", line: "open\r\n", want: "open"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := runSRead(t, inputFixture(t, 40, 10), tt.line)
			got := textBufferBytes(t, m.mem)
			if len(got) == 0 || got[len(got)-1] != 0 {
				t.Fatalf("text buffer = % x, want a zero terminator", got)
			}
			if stored := string(got[:len(got)-1]); stored != tt.want {
				t.Errorf("text buffer holds %q, want %q", stored, tt.want)
			}
		})
	}
}

// TestSReadTruncatesLongInput covers the maximum the text buffer declares.
// S 15, read: "byte 0 of the text-buffer should initially contain the maximum
// number of letters which can be typed ... (This means that if byte 0 contains
// n then the buffer must contain n+1 bytes ...)". The buffer is n+1 bytes and
// the text needs a zero terminator, so at most n-1 characters fit. A host that
// sends more gets the tail dropped, exactly as an interpreter refusing further
// keystrokes would, rather than an error.
func TestSReadTruncatesLongInput(t *testing.T) {
	m := runSRead(t, inputFixture(t, 6, 10), "abcdefghij")

	got := textBufferBytes(t, m.mem)
	if stored := string(got[:len(got)-1]); stored != "abcde" {
		t.Errorf("text buffer holds %q, want %q", stored, "abcde")
	}
	// The terminator must be the last byte of the declared buffer and nothing
	// may have been written past it.
	after, err := m.mem.readByte(fixtureTextBuffer + 7)
	if err != nil {
		t.Fatalf("readByte() error = %v", err)
	}
	if after != 0xff {
		t.Errorf("byte after the text buffer = 0x%02x, want the 0xff the fixture leaves there", after)
	}
}

// TestSReadTokenises covers the lexical analysis of S 13.6.1: "Spaces divide up
// words and are otherwise ignored. Word separators also divide words, but each
// one of them is considered a word in its own right." S 13.6.1 gives
// "fred,go fishing" as four words, which is the shape of the erratic case here.
func TestSReadTokenises(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		words []string
	}{
		{name: "one word", line: "open", words: []string{"open"}},
		{name: "two words", line: "open mailbox", words: []string{"open", "mailbox"}},
		{name: "runs of spaces are ignored", line: "  open   mailbox  ", words: []string{"open", "mailbox"}},
		{name: "a separator is a word of its own", line: "fred,go fishing", words: []string{"fred", ",", "go", "fishing"}},
		{name: "a separator with no spaces at all", line: "open.mailbox", words: []string{"open", ".", "mailbox"}},
		{name: "adjacent separators", line: ".,.", words: []string{".", ",", "."}},
		{name: "nothing at all", line: "   ", words: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := runSRead(t, inputFixture(t, 40, 10), tt.line)
			got := parseBufferWords(t, m.mem)
			if len(got) != len(tt.words) {
				t.Fatalf("parse buffer holds %d word(s), want %d", len(got), len(tt.words))
			}

			text := textBufferBytes(t, m.mem)
			for i, want := range tt.words {
				if int(got[i].length) != len(want) {
					t.Errorf("word %d: length = %d, want %d", i, got[i].length, len(want))
				}
				// S 15, read: the last byte of each block is "the position in
				// the text-buffer of the first letter of the word". In Version
				// 3 the text begins at byte 1, so the first character of the
				// line is at position 1.
				start := int(got[i].position) - textBufferOffsetV3
				if start < 0 || start+len(want) > len(text) {
					t.Fatalf("word %d: position %d is outside the text", i, got[i].position)
				}
				if stored := string(text[start : start+len(want)]); stored != want {
					t.Errorf("word %d: text at position %d = %q, want %q", i, got[i].position, stored, want)
				}
			}
		})
	}
}

// TestSReadDictionaryAddresses covers S 15, read: each block holds "the byte
// address of the word in the dictionary, if it is in the dictionary, or 0 if it
// isn't".
func TestSReadDictionaryAddresses(t *testing.T) {
	f := inputFixture(t, 40, 10)
	m := runSRead(t, f, "open the frobozz , mailbox")

	table, err := m.mem.storyDictionary()
	if err != nil {
		t.Fatalf("storyDictionary() error = %v", err)
	}
	lookup := func(word string) uint16 {
		addr, err := m.mem.lookupDictionaryWord(table, encodeDictionaryWord([]uint8(word)))
		if err != nil {
			t.Fatalf("lookupDictionaryWord(%q) error = %v", word, err)
		}
		return addr
	}

	got := parseBufferWords(t, m.mem)
	want := []uint16{lookup("open"), lookup("the"), 0, lookup(","), lookup("mailbox")}
	if len(got) != len(want) {
		t.Fatalf("parse buffer holds %d word(s), want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].entry != want[i] {
			t.Errorf("word %d: dictionary address = 0x%04x, want 0x%04x", i, got[i].entry, want[i])
		}
	}
	if want[2] != 0 {
		t.Errorf("the fixture dictionary unexpectedly contains \"frobozz\"")
	}
}

// TestSReadRespectsMaxWords covers S 15, read: one block is written for each
// word "except that it should stop before going beyond the maximum number of
// words specified". The words past the maximum are dropped and nothing is
// written past the blocks the buffer declares.
func TestSReadRespectsMaxWords(t *testing.T) {
	const maxWords = 2
	f := inputFixture(t, 40, maxWords)
	m := runSRead(t, f, "open the mailbox north")

	count, err := m.mem.readByte(fixtureParseBuffer + 1)
	if err != nil {
		t.Fatalf("readByte() error = %v", err)
	}
	if count != maxWords {
		t.Errorf("parse buffer word count = %d, want %d", count, maxWords)
	}
	// The fixture leaves 0xff everywhere it has not written, so the block after
	// the last one the buffer declares must still be untouched.
	after := uint32(fixtureParseBuffer) + parseHeaderSize + maxWords*parseEntrySize
	for i := uint32(0); i < parseEntrySize; i++ {
		b, err := m.mem.readByte(after + i)
		if err != nil {
			t.Fatalf("readByte() error = %v", err)
		}
		if b != 0xff {
			t.Errorf("byte 0x%04x past the parse buffer = 0x%02x, want it untouched", after+i, b)
		}
	}
}

// TestSReadRedisplaysStatusLine covers S 15, read: "In Versions 1 to 3, the
// status line is automatically redisplayed first", which S 8.2.4 makes one of
// only two moments at which it is updated.
func TestSReadRedisplaysStatusLine(t *testing.T) {
	f := inputFixture(t, 40, 10)
	m := f.machine()
	if err := m.writeGlobal(globalFirst, 1); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if err := m.writeGlobal(globalFirst+1, unsigned(-5)); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if err := m.writeGlobal(globalFirst+2, 12); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if m.status.Available {
		t.Fatalf("the status line is available before the first read, which S 8.2.4 forbids")
	}

	m.input, m.hasInput = "look", true
	mustStep(t, m)

	if !m.status.Available {
		t.Fatalf("the status line was not updated by read")
	}
	if m.status.Name != "West of House" {
		t.Errorf("status line Name = %q, want %q", m.status.Name, "West of House")
	}
	if m.status.Score != -5 || m.status.Turns != 12 {
		t.Errorf("status line Score, Turns = %d, %d, want -5 and 12", m.status.Score, m.status.Turns)
	}
}

// TestStatusLineAtInputBoundary covers S 15, read together with spec S 4: the
// status line is redisplayed before the keyboard is read, and the host's wait
// for the keyboard is the suspension boundary. The Result handed back there
// must therefore carry the status line the player would be looking at while
// typing, not the one from the previous turn.
func TestStatusLineAtInputBoundary(t *testing.T) {
	f := inputFixture(t, 40, 10)
	f.code(join(sreadCode(), encodeShort(0x0a))...)
	m := f.machine()
	if err := m.writeGlobal(globalFirst, 1); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}
	if err := m.writeGlobal(globalFirst+2, 7); err != nil {
		t.Fatalf("writeGlobal() error = %v", err)
	}

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Status != WaitingForInput {
		t.Fatalf("Status = %v, want %v", result.Status, WaitingForInput)
	}
	if !result.StatusLine.Available {
		t.Fatalf("the status line is not available at the input boundary")
	}
	if result.StatusLine.Name != "West of House" || result.StatusLine.Turns != 7 {
		t.Errorf("status line = %+v, want West of House on turn 7", result.StatusLine)
	}
}

// TestSReadBufferErrors covers the bounds S 15 puts on the two buffers.
// "Interpreters are asked to halt with a suitable error message if the text or
// parse buffers have length of less than 3 or 6 bytes, respectively", and a
// buffer outside dynamic memory cannot be written at all.
func TestSReadBufferErrors(t *testing.T) {
	tests := []struct {
		name   string
		text   uint16
		parse  uint16
		target error
		setup  func(*objectFixture)
	}{
		{
			name:   "a text buffer of fewer than three bytes",
			text:   fixtureTextBuffer,
			parse:  fixtureParseBuffer,
			target: ErrExecutionFault,
			setup:  func(f *objectFixture) { f.at(fixtureTextBuffer, 1) },
		},
		{
			name:   "a parse buffer with room for no words",
			text:   fixtureTextBuffer,
			parse:  fixtureParseBuffer,
			target: ErrExecutionFault,
			setup:  func(f *objectFixture) { f.at(fixtureParseBuffer, 0) },
		},
		{
			// Static memory is readable but a story may never write to it
			// (S 1.1.2).
			name:   "a text buffer in static memory",
			text:   fixtureStaticBase,
			parse:  fixtureParseBuffer,
			target: ErrMemoryAccess,
		},
		{
			name:   "a parse buffer in static memory",
			text:   fixtureTextBuffer,
			parse:  fixtureStaticBase,
			target: ErrMemoryAccess,
		},
		{
			name:   "a text buffer past the end of the story",
			text:   fixtureStorySize + 16,
			parse:  fixtureParseBuffer,
			target: ErrMemoryAccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := inputFixture(t, 40, 10)
			if tt.setup != nil {
				tt.setup(f)
			}
			f.code(encodeVar(countVAR, 0x04, largeOp(tt.text), largeOp(tt.parse))...)

			m := f.machine()
			m.input, m.hasInput = "open mailbox", true
			err := stepErr(t, m)
			assertExecutionError(t, err, fixtureCodeBase, tt.target)
		})
	}
}

// TestSReadOperandCount covers S 15, read: the Version 3 form is
// "sread text parse". The time and routine operands arrive in Version 4, so a
// Version 3 story supplying them is malformed.
func TestSReadOperandCount(t *testing.T) {
	f := inputFixture(t, 40, 10)
	f.code(encodeVar(countVAR, 0x04, largeOp(fixtureTextBuffer))...)

	m := f.machine()
	m.input, m.hasInput = "look", true
	assertExecutionError(t, stepErr(t, m), fixtureCodeBase, ErrExecutionFault)
}

// TestSReadConsumesOneLine covers spec S 11: Run supplies exactly one line, and
// "once that supplied line has been consumed, a subsequent request for line
// input SHALL cause execution to stop and return WaitingForInput".
func TestSReadConsumesOneLine(t *testing.T) {
	code := join(sreadCode(), sreadCode(), encodeShort(0x0a))
	f := inputFixture(t, 40, 10)
	f.code(code...)
	m := f.machine()

	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.Status != WaitingForInput {
		t.Fatalf("Start() Status = %v, want %v", result.Status, WaitingForInput)
	}

	result, err = m.Run(context.Background(), "open mailbox")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != WaitingForInput {
		t.Errorf("Run() Status = %v, want %v: the second read has no line", result.Status, WaitingForInput)
	}
	// The first line was consumed and reached the story.
	got := textBufferBytes(t, m.mem)
	if stored := string(got[:len(got)-1]); stored != "open mailbox" {
		t.Errorf("text buffer holds %q, want %q", stored, "open mailbox")
	}

	if result, err = m.Run(context.Background(), "north"); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != Halted {
		t.Errorf("Status = %v, want %v", result.Status, Halted)
	}
}

// TestSReadDropsUnrepresentableCharacters checks that a host line containing
// characters ZSCII cannot express does not reach the story. ZSCII is
// effectively an 8-bit code (S 3.8.1) and only the values of S 3.8 are legal,
// so anything else is dropped rather than stored as a byte the story would
// print as nonsense.
func TestSReadDropsUnrepresentableCharacters(t *testing.T) {
	m := runSRead(t, inputFixture(t, 40, 10), "op中en")

	got := textBufferBytes(t, m.mem)
	if stored := string(got[:len(got)-1]); stored != "open" {
		t.Errorf("text buffer holds %q, want %q", stored, "open")
	}
}

// FuzzObjectTables asserts that walking arbitrary bytes as a Version 3 object
// table, property table and dictionary word never panics, never allocates
// without bound and always terminates. A story is untrusted input (spec S 26)
// and its tables are as untrusted as its code.
//
// The bytes replace the whole of dynamic memory from the object table upward,
// which is where the object entries, every property table and the buffers live.
// The header, the globals and the dictionary are left valid, so that LoadStory
// accepts the image and the walk itself is what is being tested.
func FuzzObjectTables(f *testing.F) {
	f.Add([]byte{0x00}, "open")
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, "mailbox")
	f.Add(repeatByte(0x21, 64), "fred,go fishing")
	f.Add(repeatByte(0x00, 64), "")

	// The fixture is built without a testing.T so that the seed corpus and the
	// target share one image.
	header := validTestHeader()
	header.size = fixtureStorySize
	header.objectTable = fixtureObjectTable
	header.staticBase = fixtureStaticBase
	header.dictionary = fixtureDictionary
	header.highBase = fixtureCodeBase
	header.initialPC = fixtureCodeBase
	header.abbreviations = 0
	header.dictSeparators = fixtureSeparatorCount
	header.dictEntryLength = fixtureEntryLength
	header.dictEntries = 0
	base := header.build()

	f.Fuzz(func(t *testing.T, data []byte, word string) {
		const region = fixtureStaticBase - fixtureObjectTable
		if len(data) > region {
			t.Skip()
		}
		image := make([]byte, len(base))
		copy(image, base)
		copy(image[fixtureObjectTable:], data)

		story, err := LoadStory(image)
		if err != nil {
			t.Skip()
		}
		m := newMemory(story)

		// Object 0 and objects past the end of the table must be refused, and
		// every accessor over the objects that do exist must return rather than
		// run on. Errors are the expected outcome for most inputs; the
		// invariant is only that the call comes back.
		for number := uint16(0); number < 8; number++ {
			_, _ = m.objectShortName(number)
			_, _ = m.objectParent(number)
			_, _ = m.objectSibling(number)
			_, _ = m.objectChild(number)
			for attribute := uint16(0); attribute < objectAttributeCountV3; attribute += 7 {
				_, _ = m.objectAttribute(number, attribute)
				_ = m.setObjectAttribute(number, attribute, true)
			}
			for property := uint16(0); property <= propertyNumberMaxV3+1; property += 5 {
				_, _ = m.propertyValue(number, property)
				_, _ = m.propertyAddress(number, property)
				_, _ = m.nextPropertyNumber(number, property)
				_ = m.putProperty(number, property, 0x1234)
			}
			_, _ = m.propertyLength(uint16(fixtureProperties + number))
			_ = m.removeObject(number)
			_ = m.insertObject(number, number+1)
		}

		// Encoding is total: any string must produce four bytes, and looking
		// them up must terminate.
		if len(word) <= 64 {
			encoded := encodeDictionaryWord([]uint8(word))
			if table, err := m.storyDictionary(); err == nil {
				_, _ = m.lookupDictionaryWord(table, encoded)
			}
		}
	})
}
