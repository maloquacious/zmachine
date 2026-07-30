package zmachine

import (
	"encoding/binary"
	"errors"
	"os"
	"strings"
	"testing"
)

// testHeader describes a small but structurally valid Version 3 story. Tests
// start from validTestHeader and change one field at a time, so that each
// malformed case differs from a known-good story in exactly one way.
type testHeader struct {
	size int // total bytes of the built image

	version       uint8
	flags1        uint8
	flags2        uint16
	release       uint16
	serial        string
	highBase      uint16
	initialPC     uint16
	dictionary    uint16
	objectTable   uint16
	globals       uint16
	staticBase    uint16
	abbreviations uint16
	fileLength    uint16 // scaled header value; see fixLength
	checksum      uint16

	// fixLength writes size/2 into the file length field when true, and leaves
	// fileLength alone when false.
	fixLength bool
	// fixChecksum writes the true checksum into the header when true.
	fixChecksum bool
	// dictSeparators, dictEntryLength and dictEntries describe the dictionary
	// header written at dictionary.
	dictSeparators  int
	dictEntryLength uint8
	dictEntries     int16
	writeDictionary bool
}

// validTestHeader lays out a 1K story:
//
//	0x0000 header
//	0x0040 global variables table (480 bytes, S 6.2)
//	0x0220 object table (property defaults plus objects, S 12.1)
//	0x0280 abbreviations table (192 bytes, S 3.3)
//	0x0340 base of static memory
//	0x0348 dictionary (S 13.1)
//	0x0360 base of high memory; initial program counter
//	0x0400 end of file
func validTestHeader() testHeader {
	return testHeader{
		size:            0x400,
		version:         3,
		release:         42,
		serial:          "123456",
		globals:         0x0040,
		objectTable:     0x0220,
		abbreviations:   0x0280,
		staticBase:      0x0340,
		dictionary:      0x0348,
		highBase:        0x0360,
		initialPC:       0x0360,
		fixLength:       true,
		fixChecksum:     true,
		dictSeparators:  3,
		dictEntryLength: 7,
		dictEntries:     2,
		writeDictionary: true,
	}
}

// build renders the header into a story image. It never panics on
// out-of-range fields, so malformed cases can be described declaratively.
func (h testHeader) build() []byte {
	data := make([]byte, h.size)
	putByte := func(addr int, v uint8) {
		if addr >= 0 && addr < len(data) {
			data[addr] = v
		}
	}
	putWord := func(addr int, v uint16) {
		if addr >= 0 && addr+1 < len(data) {
			binary.BigEndian.PutUint16(data[addr:], v)
		}
	}

	putByte(hdrVersion, h.version)
	putByte(hdrFlags1, h.flags1)
	putWord(hdrRelease, h.release)
	putWord(hdrHighBase, h.highBase)
	putWord(hdrInitialPC, h.initialPC)
	putWord(hdrDictionary, h.dictionary)
	putWord(hdrObjectTable, h.objectTable)
	putWord(hdrGlobals, h.globals)
	putWord(hdrStaticBase, h.staticBase)
	putWord(hdrFlags2, h.flags2)
	for i := 0; i < 6 && i < len(h.serial); i++ {
		putByte(hdrSerial+i, h.serial[i])
	}
	putWord(hdrAbbreviations, h.abbreviations)

	if h.writeDictionary {
		dict := int(h.dictionary)
		putByte(dict, uint8(h.dictSeparators))
		for i := 0; i < h.dictSeparators; i++ {
			putByte(dict+1+i, uint8(".,\""[i%3]))
		}
		putByte(dict+1+h.dictSeparators, h.dictEntryLength)
		putWord(dict+2+h.dictSeparators, uint16(h.dictEntries))
	}

	length := h.fileLength
	if h.fixLength {
		length = uint16(h.size / fileLengthScaleV3)
	}
	putWord(hdrFileLength, length)

	checksum := h.checksum
	if h.fixChecksum {
		end := int(length) * fileLengthScaleV3
		if length == 0 || end > len(data) {
			end = len(data)
		}
		checksum = computeChecksum(data[:end])
	}
	putWord(hdrChecksum, checksum)

	return data
}

func TestLoadStoryValidV3(t *testing.T) {
	h := validTestHeader()
	data := h.build()

	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory() error = %v, want nil", err)
	}

	checks := []struct {
		name string
		got  uint32
		want uint32
	}{
		{"version", uint32(story.Version()), 3},
		{"release", uint32(story.Release()), 42},
		{"length", uint32(story.length), 0x400},
		{"static base", story.staticBase, 0x340},
		{"high base", story.highBase, 0x360},
		{"initial pc", story.initialPC, 0x360},
		{"dictionary", story.dictionary, 0x348},
		{"object table", story.objectTable, 0x220},
		{"globals", story.globals, 0x040},
		{"abbreviations", story.abbreviations, 0x280},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = 0x%04x, want 0x%04x", c.name, c.got, c.want)
		}
	}
	if story.Serial() != "123456" {
		t.Errorf("Serial() = %q, want %q", story.Serial(), "123456")
	}
	if story.Checksum() != story.computed {
		t.Errorf("Checksum() = 0x%04x, computed = 0x%04x, want equal", story.Checksum(), story.computed)
	}
	if story.Size() != 0x400 {
		t.Errorf("Size() = %d, want %d", story.Size(), 0x400)
	}
}

func TestLoadStoryRejectsMalformedHeaders(t *testing.T) {
	tests := []struct {
		name string
		// mutate changes one field of an otherwise valid header.
		mutate func(*testHeader)
		// data overrides the built image entirely when non-nil.
		data []byte
		// wantField is the StoryError.Field expected, if any.
		wantField string
	}{
		{
			name: "nil input",
			data: []byte{},
		},
		{
			name: "zero length input",
			data: nil,
		},
		{
			// S 1.1.1.1: the first 64 bytes are the header, so anything
			// shorter cannot be parsed at all.
			name: "truncated header",
			data: make([]byte, headerSize-1),
		},
		{
			name:      "version 1",
			mutate:    func(h *testHeader) { h.version = 1 },
			wantField: "version number",
		},
		{
			name:      "version 5",
			mutate:    func(h *testHeader) { h.version = 5 },
			wantField: "version number",
		},
		{
			name:      "version 0",
			mutate:    func(h *testHeader) { h.version = 0 },
			wantField: "version number",
		},
		{
			// S 11.1.6: the header stores the file length divided by 2 in V3.
			name:      "declared file length beyond the data",
			mutate:    func(h *testHeader) { h.fixLength = false; h.fileLength = 0x800 },
			wantField: "length of file",
		},
		{
			// S 1.1: dynamic memory must contain at least 64 bytes.
			name:      "static base inside the header",
			mutate:    func(h *testHeader) { h.staticBase = 0x20 },
			wantField: "base of static memory",
		},
		{
			name:      "static base beyond the end of the story",
			mutate:    func(h *testHeader) { h.staticBase = 0x500 },
			wantField: "base of static memory",
		},
		{
			// S 1.1: high memory may overlap static memory but never dynamic
			// memory.
			name:      "high base below static base",
			mutate:    func(h *testHeader) { h.highBase = 0x0100 },
			wantField: "base of high memory",
		},
		{
			name:      "high base beyond the end of the story",
			mutate:    func(h *testHeader) { h.highBase = 0x0500 },
			wantField: "base of high memory",
		},
		{
			name:      "initial pc inside the header",
			mutate:    func(h *testHeader) { h.initialPC = 0x10 },
			wantField: "initial program counter",
		},
		{
			name:      "initial pc beyond the end of the story",
			mutate:    func(h *testHeader) { h.initialPC = 0x400 },
			wantField: "initial program counter",
		},
		{
			// S 12.1: the object table is held in dynamic memory.
			name:      "object table crosses into static memory",
			mutate:    func(h *testHeader) { h.objectTable = 0x0320 },
			wantField: "object table",
		},
		{
			name:      "object table inside the header",
			mutate:    func(h *testHeader) { h.objectTable = 0x30 },
			wantField: "object table",
		},
		{
			// S 6.2: the 240-word global table is held in dynamic memory.
			name:      "globals table crosses into static memory",
			mutate:    func(h *testHeader) { h.globals = 0x0200 },
			wantField: "global variables table",
		},
		{
			name:      "globals table inside the header",
			mutate:    func(h *testHeader) { h.globals = 0x00 },
			wantField: "global variables table",
		},
		{
			// S 3.3: the V3 abbreviations table is 96 word addresses.
			name:      "abbreviations table beyond the end of the story",
			mutate:    func(h *testHeader) { h.abbreviations = 0x03f0 },
			wantField: "abbreviations table",
		},
		{
			name:      "dictionary beyond the end of the story",
			mutate:    func(h *testHeader) { h.dictionary = 0x0400; h.writeDictionary = false },
			wantField: "dictionary",
		},
		{
			name:      "dictionary inside the header",
			mutate:    func(h *testHeader) { h.dictionary = 0x02; h.writeDictionary = false },
			wantField: "dictionary",
		},
		{
			// S 13.2: entry length must be at least 4 in Versions 1 to 3.
			name:      "dictionary entry length below the V3 minimum",
			mutate:    func(h *testHeader) { h.dictEntryLength = 3 },
			wantField: "dictionary",
		},
		{
			name:      "dictionary entries run past the end of the story",
			mutate:    func(h *testHeader) { h.dictEntries = 1000 },
			wantField: "dictionary",
		},
		{
			// A negative count marks an unsorted user dictionary (S 15,
			// tokenise); the header dictionary is the main dictionary.
			name:      "dictionary entry count negative",
			mutate:    func(h *testHeader) { h.dictEntries = -4 },
			wantField: "dictionary",
		},
		{
			// S 13.2: the separator list is counted in bytes and must fit.
			name:      "dictionary header runs past the end of the story",
			mutate:    func(h *testHeader) { h.dictionary = 0x03fe; h.dictSeparators = 200 },
			wantField: "dictionary",
		},
		{
			// S 1.1.4: Version 1-3 stories are at most 128K.
			name: "story larger than the V3 maximum",
			data: func() []byte {
				h := validTestHeader()
				h.size = maxStoryLength + 2
				return h.build()
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.data
			if tt.mutate != nil {
				h := validTestHeader()
				tt.mutate(&h)
				data = h.build()
			}

			story, err := LoadStory(data)
			if err == nil {
				t.Fatalf("LoadStory() = %+v, want error", story)
			}
			if story != nil {
				t.Errorf("LoadStory() story = %+v, want nil on error", story)
			}
			if !errors.Is(err, ErrInvalidStory) {
				t.Errorf("errors.Is(err, ErrInvalidStory) = false; err = %v", err)
			}
			var storyErr *StoryError
			if !errors.As(err, &storyErr) {
				t.Fatalf("errors.As(err, *StoryError) = false; err = %v", err)
			}
			if tt.wantField != "" && storyErr.Field != tt.wantField {
				t.Errorf("StoryError.Field = %q, want %q (err = %v)", storyErr.Field, tt.wantField, err)
			}
			if !strings.HasPrefix(err.Error(), "zmachine: invalid story: ") {
				t.Errorf("error message = %q, want the package prefix", err.Error())
			}
		})
	}
}

// TestLoadStoryEarlyV3WithoutFileLength covers S 11.1: some early Version 3
// files carry no length or checksum, in which case the whole input is the
// story.
func TestLoadStoryEarlyV3WithoutFileLength(t *testing.T) {
	h := validTestHeader()
	h.fixLength = false
	h.fileLength = 0
	h.fixChecksum = false
	h.checksum = 0
	data := h.build()

	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	if story.Size() != len(data) {
		t.Errorf("Size() = %d, want %d", story.Size(), len(data))
	}
	if story.Checksum() != 0 {
		t.Errorf("Checksum() = 0x%04x, want 0", story.Checksum())
	}
}

// TestLoadStoryIgnoresPaddingAfterDeclaredLength covers S 15 (verify): a story
// file may be padded beyond the declared length, and the padding is part of
// neither the story image nor the checksum.
func TestLoadStoryIgnoresPaddingAfterDeclaredLength(t *testing.T) {
	h := validTestHeader()
	data := h.build()
	padded := append(data, make([]byte, 0x100)...)
	for i := len(data); i < len(padded); i++ {
		padded[i] = 0xff
	}

	story, err := LoadStory(padded)
	if err != nil {
		t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	if story.Size() != len(data) {
		t.Errorf("Size() = %d, want the declared length %d", story.Size(), len(data))
	}
	if story.computed != story.Checksum() {
		t.Errorf("computed checksum 0x%04x != header checksum 0x%04x; padding must be excluded",
			story.computed, story.Checksum())
	}
}

// TestLoadStoryCopiesInput checks that a Story is immutable even if the caller
// keeps writing to the slice it passed in. Stories are shared between machines
// and must be safe for concurrent use.
func TestLoadStoryCopiesInput(t *testing.T) {
	data := validTestHeader().build()

	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	before := story.image[hdrStaticBase]

	for i := range data {
		data[i] = 0xff
	}

	if got := story.image[hdrStaticBase]; got != before {
		t.Errorf("story image byte changed to 0x%02x after the caller modified its slice, want 0x%02x", got, before)
	}
	if story.staticBase != 0x340 {
		t.Errorf("static base = 0x%04x after mutating the caller's slice, want 0x0340", story.staticBase)
	}
}

// TestLoadStoryZork1 proves the parser against a real Version 3 story rather
// than only against constructed fixtures.
func TestLoadStoryZork1(t *testing.T) {
	const path = "testdata/stories/zork1-r119-880429.z3"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("story fixture unavailable: %v", err)
	}

	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory(%s) error = %v, want nil", path, err)
	}

	if story.Version() != 3 {
		t.Errorf("Version() = %d, want 3", story.Version())
	}
	if story.Release() != 119 {
		t.Errorf("Release() = %d, want 119", story.Release())
	}
	if story.Serial() != "880429" {
		t.Errorf("Serial() = %q, want %q", story.Serial(), "880429")
	}
	if story.Size() != len(data) {
		t.Errorf("Size() = %d, want %d", story.Size(), len(data))
	}
	if story.Checksum() != story.computed {
		t.Errorf("header checksum 0x%04x != computed 0x%04x", story.Checksum(), story.computed)
	}

	// Values read from the story's own header; they are asserted so that a
	// regression in field offsets cannot pass unnoticed.
	want := map[string]uint32{
		"static base":   0x2c12,
		"high base":     0x4b54,
		"initial pc":    0x50d5,
		"dictionary":    0x3899,
		"object table":  0x03e6,
		"globals":       0x02b0,
		"abbreviations": 0x01f0,
	}
	got := map[string]uint32{
		"static base":   story.staticBase,
		"high base":     story.highBase,
		"initial pc":    story.initialPC,
		"dictionary":    story.dictionary,
		"object table":  story.objectTable,
		"globals":       story.globals,
		"abbreviations": story.abbreviations,
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = 0x%04x, want 0x%04x", name, got[name], w)
		}
	}
}

// FuzzLoadStory asserts that arbitrary bytes never panic and that any Story
// LoadStory accepts satisfies the Version 3 memory-map invariants.
func FuzzLoadStory(f *testing.F) {
	f.Add([]byte(nil))
	f.Add(make([]byte, headerSize))
	f.Add(validTestHeader().build())

	h := validTestHeader()
	h.fixLength = false
	h.fileLength = 0
	f.Add(h.build())

	if data, err := os.ReadFile("testdata/stories/zork1-r119-880429.z3"); err == nil {
		f.Add(data[:headerSize*4])
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		story, err := LoadStory(data)
		if err != nil {
			if !errors.Is(err, ErrInvalidStory) {
				t.Fatalf("LoadStory() error = %v, want it to wrap ErrInvalidStory", err)
			}
			if story != nil {
				t.Fatalf("LoadStory() returned a story with an error")
			}
			return
		}
		assertStoryInvariants(t, story)

		// A memory view of any accepted story must also hold together.
		m := newMemory(story)
		if m.size() != story.length {
			t.Fatalf("memory size %d != story length %d", m.size(), story.length)
		}
		if _, err := m.readByte(m.size()); err == nil {
			t.Fatalf("read one byte past the end of memory succeeded")
		}
		if _, err := m.readWord(story.initialPC); err != nil && story.initialPC+1 < m.size() {
			t.Fatalf("readWord(initialPC) = %v, want nil", err)
		}
	})
}

// assertStoryInvariants states the properties every accepted Story must have.
func assertStoryInvariants(t *testing.T, s *Story) {
	t.Helper()

	if s.version != versionV3 {
		t.Fatalf("version = %d, want %d", s.version, versionV3)
	}
	if uint32(len(s.image)) != s.length {
		t.Fatalf("image length %d != recorded length %d", len(s.image), s.length)
	}
	if s.length > maxStoryLength {
		t.Fatalf("length %d exceeds the V3 maximum %d", s.length, maxStoryLength)
	}
	if s.staticBase < headerSize || s.staticBase > s.length {
		t.Fatalf("static base 0x%04x outside [0x%04x, 0x%04x]", s.staticBase, uint32(headerSize), s.length)
	}
	if s.highBase < s.staticBase || s.highBase > s.length {
		t.Fatalf("high base 0x%04x outside [0x%04x, 0x%04x]", s.highBase, s.staticBase, s.length)
	}
	if s.initialPC < headerSize || s.initialPC >= s.length {
		t.Fatalf("initial pc 0x%04x outside [0x%04x, 0x%04x)", s.initialPC, uint32(headerSize), s.length)
	}
	if s.objectTable < headerSize || s.objectTable+propertyDefaultsSizeV3 > s.staticBase {
		t.Fatalf("object table 0x%04x does not fit in dynamic memory (0x%04x)", s.objectTable, s.staticBase)
	}
	if s.globals < headerSize || s.globals+globalsTableSize > s.staticBase {
		t.Fatalf("globals 0x%04x does not fit in dynamic memory (0x%04x)", s.globals, s.staticBase)
	}
	if s.abbreviations != 0 && (s.abbreviations < headerSize || s.abbreviations+abbreviationsTableSizeV3 > s.length) {
		t.Fatalf("abbreviations 0x%04x does not fit in the story (0x%04x)", s.abbreviations, s.length)
	}
	if s.dictionary < headerSize || s.dictionary >= s.length {
		t.Fatalf("dictionary 0x%04x outside [0x%04x, 0x%04x)", s.dictionary, uint32(headerSize), s.length)
	}
}
