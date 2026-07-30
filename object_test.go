package zmachine

import (
	"errors"
	"os"
	"testing"
)

// Layout of the story built by newObjectFixture. It has far more room in
// dynamic memory than the fixture in machine_test.go, because an object table,
// the property tables it points at and the buffers sread writes must all live
// there (S 12.1, S 12.4):
//
//	0x0000 header
//	0x0040 global variables table (480 bytes, S 6.2)
//	0x0220 object table: 31 words of property defaults, then object entries
//	0x0500 property tables
//	0x0700 text buffer
//	0x0780 parse buffer
//	0x0800 base of static memory, and the dictionary
//	0x0a00 base of high memory; initial program counter
//	0x1000 end of file
const (
	fixtureObjectTable    = 0x0220
	fixtureProperties     = 0x0500
	fixtureTextBuffer     = 0x0700
	fixtureParseBuffer    = 0x0780
	fixtureStaticBase     = 0x0800
	fixtureDictionary     = 0x0800
	fixtureDictEntries    = fixtureDictionary + 7 // past a header with three separators
	fixtureDictCountAt    = fixtureDictionary + 5
	fixtureCodeBase       = 0x0a00
	fixtureStorySize      = 0x1000
	fixtureEntryLength    = 7
	fixtureFirstObject    = fixtureObjectTable + propertyDefaultsSizeV3
	fixtureSeparatorCount = 3
)

// testProperty is one property to write into an object's property table
// (S 12.4.1). The fixture writes properties in the order given, so a test can
// build a table that is deliberately not in descending order.
type testProperty struct {
	number uint8
	data   []byte
}

// testObject describes one object entry and its property table (S 12.3.1,
// S 12.4).
type testObject struct {
	name       string
	attributes []uint16
	parent     uint16
	sibling    uint16
	child      uint16
	properties []testProperty
}

// objectFixture assembles a story with a hand-built object table, property
// tables and dictionary, so that each rule is proved with the smallest state
// that can express it rather than with a whole story.
type objectFixture struct {
	t    *testing.T
	data []byte
	// next is the next free address in the property table area.
	next uint32
}

// newObjectFixture starts a story laid out as described above, with no
// abbreviations table and an empty dictionary.
func newObjectFixture(t *testing.T) *objectFixture {
	t.Helper()
	h := validTestHeader()
	h.size = fixtureStorySize
	h.objectTable = fixtureObjectTable
	h.staticBase = fixtureStaticBase
	h.dictionary = fixtureDictionary
	h.highBase = fixtureCodeBase
	h.initialPC = fixtureCodeBase
	// Abbreviations would overlap the object entries, and nothing here needs
	// them: a story may legally declare no abbreviations table (S 3.3).
	h.abbreviations = 0
	h.dictSeparators = fixtureSeparatorCount
	h.dictEntryLength = fixtureEntryLength
	h.dictEntries = 0
	return &objectFixture{t: t, data: h.build(), next: fixtureProperties}
}

// at writes bytes into the image at addr.
func (f *objectFixture) at(addr uint32, bytes ...byte) *objectFixture {
	f.t.Helper()
	if int(addr)+len(bytes) > len(f.data) {
		f.t.Fatalf("%d byte(s) at 0x%04x do not fit in the %d-byte fixture", len(bytes), addr, len(f.data))
	}
	copy(f.data[addr:], bytes)
	return f
}

// code writes bytes at the initial program counter.
func (f *objectFixture) code(bytes ...byte) *objectFixture {
	f.t.Helper()
	return f.at(fixtureCodeBase, bytes...)
}

// defaults writes the property defaults table, whose n-th word is the value
// property n takes for an object that does not provide it (S 12.2).
func (f *objectFixture) defaults(values map[uint8]uint16) *objectFixture {
	f.t.Helper()
	for number, value := range values {
		if number == 0 || number > propertyNumberMaxV3 {
			f.t.Fatalf("property %d is outside the Version 3 range 1 to %d", number, propertyNumberMaxV3)
		}
		addr := uint32(fixtureObjectTable) + uint32(number-1)*wordAddressScale
		f.at(addr, uint8(value>>8), uint8(value))
	}
	return f
}

// object writes an object entry and the property table it points at.
func (f *objectFixture) object(number uint16, o testObject) *objectFixture {
	f.t.Helper()
	if number == 0 || number > maxObjectsV3 {
		f.t.Fatalf("object %d is outside the Version 3 range 1 to %d", number, maxObjectsV3)
	}

	table := f.next
	// S 12.4: a text-length byte giving the number of 2-byte words of the short
	// name, then the name itself.
	encoded := encodeText(f.t, o.name)
	if len(encoded)%zstringWordSize != 0 {
		f.t.Fatalf("encoded short name %q is %d bytes, not a whole number of words", o.name, len(encoded))
	}
	body := []byte{uint8(len(encoded) / zstringWordSize)}
	body = append(body, encoded...)
	for _, p := range o.properties {
		if p.number == 0 || p.number > propertyNumberMaxV3 {
			f.t.Fatalf("property %d is outside the Version 3 range 1 to %d", p.number, propertyNumberMaxV3)
		}
		if len(p.data) < 1 || len(p.data) > 8 {
			f.t.Fatalf("property %d has %d data bytes; S 12.4.1 allows 1 to 8", p.number, len(p.data))
		}
		// S 12.4.1: the size byte is 32 times the number of data bytes minus
		// one, plus the property number.
		body = append(body, uint8(len(p.data)-1)<<propertySizeShift|p.number)
		body = append(body, p.data...)
	}
	// S 12.4.1: a property list is terminated by a size byte of 0.
	body = append(body, 0)
	f.at(table, body...)
	f.next += uint32(len(body))

	entry := make([]byte, objectEntrySizeV3)
	for _, a := range o.attributes {
		if a >= objectAttributeCountV3 {
			f.t.Fatalf("attribute %d is outside the Version 3 range 0 to %d", a, objectAttributeCountV3-1)
		}
		// S 12.3.1: attribute 0 is bit 7 of the first byte.
		entry[a/attributeBitsPerByte] |= 0x80 >> (a % attributeBitsPerByte)
	}
	entry[objectParentOffset] = uint8(o.parent)
	entry[objectSiblingOffset] = uint8(o.sibling)
	entry[objectChildOffset] = uint8(o.child)
	entry[objectPropertiesOffset] = uint8(table >> 8)
	entry[objectPropertiesOffset+1] = uint8(table)

	return f.at(fixtureFirstObject+uint32(number-1)*objectEntrySizeV3, entry...)
}

// words writes the dictionary entries for the given words, sorted into the
// numerical order of their encoded text that S 13.5 requires.
func (f *objectFixture) words(list ...string) *objectFixture {
	f.t.Helper()

	type entry struct {
		key  uint32
		text [dictionaryTextBytesV3]byte
	}
	entries := make([]entry, 0, len(list))
	for _, word := range list {
		text := encodeDictionaryWord([]uint8(word))
		key := uint32(text[0])<<24 | uint32(text[1])<<16 | uint32(text[2])<<8 | uint32(text[3])
		entries = append(entries, entry{key: key, text: text})
	}
	// A short insertion sort keeps the fixture free of any dependency on the
	// encoder's own ordering.
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j-1].key > entries[j].key; j-- {
			entries[j-1], entries[j] = entries[j], entries[j-1]
		}
	}

	f.at(fixtureDictCountAt, uint8(len(entries)>>8), uint8(len(entries)))
	for i, e := range entries {
		addr := uint32(fixtureDictEntries) + uint32(i)*fixtureEntryLength
		f.at(addr, e.text[0], e.text[1], e.text[2], e.text[3], 0, 0, 0)
	}
	return f
}

// buffers writes a text buffer and a parse buffer, whose first bytes hold the
// maximum number of letters and of words respectively (S 15, read).
//
// The whole buffer area is filled with 0xff first, so that a test can tell what
// sread wrote from what it left alone.
func (f *objectFixture) buffers(textSize, maxWords uint8) *objectFixture {
	f.t.Helper()
	return f.at(fixtureTextBuffer, repeatByte(0xff, fixtureStaticBase-fixtureTextBuffer)...).
		at(fixtureTextBuffer, textSize).
		at(fixtureParseBuffer, maxWords)
}

// repeatByte returns n copies of b.
func repeatByte(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

// load validates the image and returns the immutable Story.
func (f *objectFixture) load() *Story {
	f.t.Helper()
	story, err := LoadStory(f.data)
	if err != nil {
		f.t.Fatalf("LoadStory() error = %v, want nil", err)
	}
	return story
}

// memory returns a memory view of the fixture, for the helpers that need no
// Machine at all.
func (f *objectFixture) memory() *memory {
	f.t.Helper()
	return newMemory(f.load())
}

// machine loads the story and creates a seeded Machine from it.
func (f *objectFixture) machine() *Machine {
	f.t.Helper()
	m, err := New(f.load(), WithRandomSeed(1))
	if err != nil {
		f.t.Fatalf("New() error = %v, want nil", err)
	}
	return m
}

// relatives returns an object's parent, sibling and child.
func relatives(t *testing.T, m *memory, number uint16) (uint16, uint16, uint16) {
	t.Helper()
	parent, err := m.objectParent(number)
	if err != nil {
		t.Fatalf("objectParent(%d) error = %v", number, err)
	}
	sibling, err := m.objectSibling(number)
	if err != nil {
		t.Fatalf("objectSibling(%d) error = %v", number, err)
	}
	child, err := m.objectChild(number)
	if err != nil {
		t.Fatalf("objectChild(%d) error = %v", number, err)
	}
	return parent, sibling, child
}

// childrenOf returns the numbers of an object's children in chain order: the
// child, then that object's sibling, and so on (S 12.5(b)).
func childrenOf(t *testing.T, m *memory, parent uint16) []uint16 {
	t.Helper()
	var chain []uint16
	current, err := m.objectChild(parent)
	if err != nil {
		t.Fatalf("objectChild(%d) error = %v", parent, err)
	}
	for current != objectNothing {
		if len(chain) > maxObjectsV3 {
			t.Fatalf("the child chain of object %d does not end", parent)
		}
		chain = append(chain, current)
		current, err = m.objectSibling(current)
		if err != nil {
			t.Fatalf("objectSibling(%d) error = %v", current, err)
		}
	}
	return chain
}

// equalObjects compares two chains of object numbers.
func equalObjects(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// familyFixture builds a parent object 1 with three children, 2, 3 and 4, in
// that chain order, plus an unrelated object 5 with no parent.
//
//	1 --child--> 2 --sibling--> 3 --sibling--> 4
func familyFixture(t *testing.T) *objectFixture {
	t.Helper()
	return newObjectFixture(t).
		object(1, testObject{name: "house", child: 2}).
		object(2, testObject{name: "first", parent: 1, sibling: 3}).
		object(3, testObject{name: "middle", parent: 1, sibling: 4}).
		object(4, testObject{name: "last", parent: 1}).
		object(5, testObject{name: "orphan"})
}

// TestAttributeBitOrder covers the rule of S 12.3.1 that is easiest to get
// backwards: the 32 attribute flags are "stored topmost bit first: e.g.,
// attribute 0 is stored in bit 7 of the first byte, attribute 31 is stored in
// bit 0 of the fourth".
func TestAttributeBitOrder(t *testing.T) {
	tests := []struct {
		attribute uint16
		byteIndex uint32
		want      uint8
	}{
		{attribute: 0, byteIndex: 0, want: 0x80},
		{attribute: 1, byteIndex: 0, want: 0x40},
		{attribute: 7, byteIndex: 0, want: 0x01},
		{attribute: 8, byteIndex: 1, want: 0x80},
		{attribute: 23, byteIndex: 2, want: 0x01},
		{attribute: 24, byteIndex: 3, want: 0x80},
		{attribute: 31, byteIndex: 3, want: 0x01},
	}

	for _, tt := range tests {
		m := newObjectFixture(t).object(1, testObject{name: "thing"}).memory()
		if err := m.setObjectAttribute(1, tt.attribute, true); err != nil {
			t.Fatalf("setObjectAttribute(1, %d) error = %v", tt.attribute, err)
		}
		for i := uint32(0); i < 4; i++ {
			got, err := m.readByte(fixtureFirstObject + objectAttributeOffset + i)
			if err != nil {
				t.Fatalf("readByte() error = %v", err)
			}
			want := uint8(0)
			if i == tt.byteIndex {
				want = tt.want
			}
			if got != want {
				t.Errorf("attribute %d: byte %d = 0x%02x, want 0x%02x", tt.attribute, i, got, want)
			}
		}
	}
}

// TestAttributesRoundTrip checks every Version 3 attribute independently:
// setting one must not disturb any other, and clearing it must restore the
// object exactly (S 12.3.1).
func TestAttributesRoundTrip(t *testing.T) {
	m := newObjectFixture(t).object(1, testObject{name: "thing"}).memory()

	for a := uint16(0); a < objectAttributeCountV3; a++ {
		if err := m.setObjectAttribute(1, a, true); err != nil {
			t.Fatalf("setObjectAttribute(1, %d) error = %v", a, err)
		}
		for b := uint16(0); b < objectAttributeCountV3; b++ {
			set, err := m.objectAttribute(1, b)
			if err != nil {
				t.Fatalf("objectAttribute(1, %d) error = %v", b, err)
			}
			if set != (a == b) {
				t.Fatalf("with attribute %d set, attribute %d = %t", a, b, set)
			}
		}
		if err := m.setObjectAttribute(1, a, false); err != nil {
			t.Fatalf("clearing attribute %d error = %v", a, err)
		}
	}
}

// TestAttributeOutOfRange covers S 12.3.1: Version 3 objects have exactly 32
// attributes. A story asking for a higher one - as 'Sherlock' does, per the
// remarks to S 12 - is reported rather than allowed to write over the object's
// parent field.
func TestAttributeOutOfRange(t *testing.T) {
	m := newObjectFixture(t).object(1, testObject{name: "thing"}).memory()

	if _, err := m.objectAttribute(1, objectAttributeCountV3); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("objectAttribute(1, 32) error = %v, want one wrapping ErrExecutionFault", err)
	}
	if err := m.setObjectAttribute(1, 48, true); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("setObjectAttribute(1, 48) error = %v, want one wrapping ErrExecutionFault", err)
	}
	parent, _, _ := relatives(t, m, 1)
	if parent != 0 {
		t.Errorf("parent = %d after the refused writes, want 0", parent)
	}
}

// TestObjectZeroIsNothing covers S 12.3: object number 0 is used to mean
// "nothing", "though there is formally no such object". Every accessor refuses
// it instead of indexing the table one entry below the first.
func TestObjectZeroIsNothing(t *testing.T) {
	m := familyFixture(t).memory()

	checks := map[string]error{
		"objectParent":  errFrom(func() error { _, err := m.objectParent(0); return err }),
		"objectSibling": errFrom(func() error { _, err := m.objectSibling(0); return err }),
		"objectChild":   errFrom(func() error { _, err := m.objectChild(0); return err }),
		"objectAttr":    errFrom(func() error { _, err := m.objectAttribute(0, 0); return err }),
		"setObjectAttr": m.setObjectAttribute(0, 0, true),
		"removeObject":  m.removeObject(0),
		"insertInto0":   m.insertObject(2, 0),
		"insert0":       m.insertObject(0, 1),
		"shortName":     errFrom(func() error { _, err := m.objectShortName(0); return err }),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrExecutionFault) {
			t.Errorf("%s on object 0: error = %v, want one wrapping ErrExecutionFault", name, err)
		}
	}

	// Nothing above may have disturbed the tree.
	if got := childrenOf(t, m, 1); !equalObjects(got, []uint16{2, 3, 4}) {
		t.Errorf("children of object 1 = %v, want [2 3 4]", got)
	}
}

// errFrom runs f and returns its error, so that a table of checks can mix
// value-returning and error-returning calls.
func errFrom(f func() error) error { return f() }

// TestObjectNumberOutOfRange covers S 12.3.1: "in Versions 1 to 3, there are
// at most 255 objects". A number past the end of the table, or one whose entry
// would fall outside dynamic memory, is an error and never an unchecked index.
func TestObjectNumberOutOfRange(t *testing.T) {
	m := newObjectFixture(t).object(1, testObject{name: "thing"}).memory()

	if _, err := m.objectParent(256); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("objectParent(256) error = %v, want one wrapping ErrExecutionFault", err)
	}
	// Object 255 is inside the Version 3 range but its entry, at 0x025e +
	// 254*9 = 0x0810, is past the base of static memory in this fixture, so it
	// is refused as well: S 12.1 holds the object table in dynamic memory.
	if _, err := m.objectParent(255); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("objectParent(255) error = %v, want one wrapping ErrExecutionFault", err)
	}
}

// TestObjectRelativeMustFitInAByte covers S 12.3.1: "parent, sibling and child
// must all hold valid object numbers", and in Version 3 each field is a single
// byte. Storing a larger number would truncate it and quietly name a different
// object, so it is refused.
func TestObjectRelativeMustFitInAByte(t *testing.T) {
	m := familyFixture(t).memory()

	if err := m.setObjectRelative(1, objectParentOffset, maxObjectsV3+1); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("setObjectRelative(1, parent, 256) error = %v, want one wrapping ErrExecutionFault", err)
	}
	if parent, _, _ := relatives(t, m, 1); parent != objectNothing {
		t.Errorf("parent of object 1 = %d, want 0: the refused write must change nothing", parent)
	}
}

// TestRemoveObject covers remove_obj (S 15) at every position in a chain of
// children. Relinking the chain is where an object model most often goes
// silently wrong, so each case checks the whole remaining chain and not just
// the object removed.
func TestRemoveObject(t *testing.T) {
	tests := []struct {
		name   string
		remove uint16
		want   []uint16
	}{
		{name: "first child", remove: 2, want: []uint16{3, 4}},
		{name: "middle child", remove: 3, want: []uint16{2, 4}},
		{name: "last child", remove: 4, want: []uint16{2, 3}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := familyFixture(t).memory()
			if err := m.removeObject(tt.remove); err != nil {
				t.Fatalf("removeObject(%d) error = %v", tt.remove, err)
			}
			if got := childrenOf(t, m, 1); !equalObjects(got, tt.want) {
				t.Errorf("children of object 1 = %v, want %v", got, tt.want)
			}
			// S 15, remove_obj: the object "no longer has any parent". Its
			// sibling goes too, because S 12.5(a) says an object with a sibling
			// also has a parent.
			parent, sibling, _ := relatives(t, m, tt.remove)
			if parent != objectNothing || sibling != objectNothing {
				t.Errorf("object %d: parent = %d, sibling = %d, want 0 and 0", tt.remove, parent, sibling)
			}
		})
	}

	t.Run("only child", func(t *testing.T) {
		m := newObjectFixture(t).
			object(1, testObject{name: "house", child: 2}).
			object(2, testObject{name: "only", parent: 1}).
			memory()
		if err := m.removeObject(2); err != nil {
			t.Fatalf("removeObject(2) error = %v", err)
		}
		if got := childrenOf(t, m, 1); len(got) != 0 {
			t.Errorf("children of object 1 = %v, want none", got)
		}
	})

	t.Run("object with no parent", func(t *testing.T) {
		m := familyFixture(t).memory()
		if err := m.removeObject(5); err != nil {
			t.Fatalf("removeObject(5) error = %v", err)
		}
		if parent, _, _ := relatives(t, m, 5); parent != objectNothing {
			t.Errorf("parent of object 5 = %d, want 0", parent)
		}
	})

	t.Run("keeps its own children", func(t *testing.T) {
		// S 15, remove_obj: "(Its children remain in its possession.)"
		m := newObjectFixture(t).
			object(1, testObject{name: "house", child: 2}).
			object(2, testObject{name: "box", parent: 1, child: 3}).
			object(3, testObject{name: "coin", parent: 2}).
			memory()
		if err := m.removeObject(2); err != nil {
			t.Fatalf("removeObject(2) error = %v", err)
		}
		if got := childrenOf(t, m, 2); !equalObjects(got, []uint16{3}) {
			t.Errorf("children of object 2 = %v, want [3]", got)
		}
		if parent, _, _ := relatives(t, m, 3); parent != 2 {
			t.Errorf("parent of object 3 = %d, want 2", parent)
		}
	})
}

// TestRemoveObjectFromIllFoundedTree covers S 12.5(b): an object is the parent
// of exactly those objects in the sibling list of its child. A story that has
// broken that gets a contextual error, and the walk of the chain is bounded so
// that a cycle cannot hang the host.
func TestRemoveObjectFromIllFoundedTree(t *testing.T) {
	t.Run("not in the parent's chain", func(t *testing.T) {
		m := newObjectFixture(t).
			object(1, testObject{name: "house", child: 3}).
			object(2, testObject{name: "stray", parent: 1}).
			object(3, testObject{name: "child", parent: 1}).
			memory()
		if err := m.removeObject(2); !errors.Is(err, ErrExecutionFault) {
			t.Errorf("removeObject(2) error = %v, want one wrapping ErrExecutionFault", err)
		}
	})

	t.Run("cyclic chain", func(t *testing.T) {
		// Objects 3 and 4 are each other's siblings, so the walk never reaches
		// object 2 and must stop of its own accord.
		m := newObjectFixture(t).
			object(1, testObject{name: "house", child: 3}).
			object(2, testObject{name: "stray", parent: 1}).
			object(3, testObject{name: "a", parent: 1, sibling: 4}).
			object(4, testObject{name: "b", parent: 1, sibling: 3}).
			memory()
		if err := m.removeObject(2); !errors.Is(err, ErrExecutionFault) {
			t.Errorf("removeObject(2) error = %v, want one wrapping ErrExecutionFault", err)
		}
	})
}

// TestInsertObject covers insert_obj (S 15): the object becomes the first
// child of the destination, the destination's old child becomes its sibling,
// and the object is unlinked from wherever it was before.
func TestInsertObject(t *testing.T) {
	t.Run("into an object with children", func(t *testing.T) {
		m := familyFixture(t).memory()
		if err := m.insertObject(5, 1); err != nil {
			t.Fatalf("insertObject(5, 1) error = %v", err)
		}
		if got := childrenOf(t, m, 1); !equalObjects(got, []uint16{5, 2, 3, 4}) {
			t.Errorf("children of object 1 = %v, want [5 2 3 4]", got)
		}
		if parent, sibling, _ := relatives(t, m, 5); parent != 1 || sibling != 2 {
			t.Errorf("object 5: parent = %d, sibling = %d, want 1 and 2", parent, sibling)
		}
	})

	t.Run("into an empty object", func(t *testing.T) {
		m := familyFixture(t).memory()
		if err := m.insertObject(3, 5); err != nil {
			t.Fatalf("insertObject(3, 5) error = %v", err)
		}
		if got := childrenOf(t, m, 5); !equalObjects(got, []uint16{3}) {
			t.Errorf("children of object 5 = %v, want [3]", got)
		}
		if got := childrenOf(t, m, 1); !equalObjects(got, []uint16{2, 4}) {
			t.Errorf("children of object 1 = %v, want [2 4]", got)
		}
		if parent, sibling, _ := relatives(t, m, 3); parent != 5 || sibling != objectNothing {
			t.Errorf("object 3: parent = %d, sibling = %d, want 5 and 0", parent, sibling)
		}
	})

	t.Run("moves children with it", func(t *testing.T) {
		// S 15, insert_obj: "All children of O move with it."
		m := newObjectFixture(t).
			object(1, testObject{name: "house", child: 2}).
			object(2, testObject{name: "box", parent: 1, child: 3}).
			object(3, testObject{name: "coin", parent: 2}).
			object(4, testObject{name: "shed"}).
			memory()
		if err := m.insertObject(2, 4); err != nil {
			t.Fatalf("insertObject(2, 4) error = %v", err)
		}
		if got := childrenOf(t, m, 2); !equalObjects(got, []uint16{3}) {
			t.Errorf("children of object 2 = %v, want [3]", got)
		}
	})

	t.Run("already the first child", func(t *testing.T) {
		m := familyFixture(t).memory()
		if err := m.insertObject(2, 1); err != nil {
			t.Fatalf("insertObject(2, 1) error = %v", err)
		}
		if got := childrenOf(t, m, 1); !equalObjects(got, []uint16{2, 3, 4}) {
			t.Errorf("children of object 1 = %v, want [2 3 4]", got)
		}
	})

	t.Run("into itself", func(t *testing.T) {
		// S 12.5(c) gives every object a level one greater than its parent's,
		// which an object that is its own parent cannot have.
		m := familyFixture(t).memory()
		if err := m.insertObject(2, 2); !errors.Is(err, ErrExecutionFault) {
			t.Errorf("insertObject(2, 2) error = %v, want one wrapping ErrExecutionFault", err)
		}
		if got := childrenOf(t, m, 1); !equalObjects(got, []uint16{2, 3, 4}) {
			t.Errorf("children of object 1 = %v, want [2 3 4]: the refused move must change nothing", got)
		}
	})
}

// TestObjectTreeOpcodes covers get_parent, get_sibling and get_child (S 15).
// get_sibling and get_child both store a result and branch, and the branch is
// taken when the result "exists, i.e. is not 0"; get_parent stores only, since
// S 15 notes it "has no 'branch if exists' clause".
func TestObjectTreeOpcodes(t *testing.T) {
	tests := []struct {
		name       string
		number     uint8
		object     uint8
		want       uint16
		wantBranch bool
		branches   bool
	}{
		{name: "get_sibling of a middle child", number: 0x01, object: 2, want: 3, wantBranch: true, branches: true},
		{name: "get_sibling of the last child", number: 0x01, object: 4, want: 0, wantBranch: false, branches: true},
		{name: "get_child of a parent", number: 0x02, object: 1, want: 2, wantBranch: true, branches: true},
		{name: "get_child of a leaf", number: 0x02, object: 4, want: 0, wantBranch: false, branches: true},
		{name: "get_parent of a child", number: 0x03, object: 3, want: 1},
		{name: "get_parent of an orphan", number: 0x03, object: 5, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := append(encodeShort(tt.number, smallOp(tt.object)), globalFirst)
			if tt.branches {
				code = append(code, branch2(true, 20)...)
			}
			m := familyFixture(t).code(code...).machine()

			branched := execBranch(t, m, code)
			got, err := m.readGlobal(globalFirst)
			if err != nil {
				t.Fatalf("readGlobal() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("result = %d, want %d", got, tt.want)
			}
			if tt.branches && branched != tt.wantBranch {
				t.Errorf("branched = %t, want %t", branched, tt.wantBranch)
			}
		})
	}
}

// TestJinOpcode covers jin (S 15): jump if object a is a direct child of b,
// that is if the parent of a is b. Being a grandchild is not enough.
func TestJinOpcode(t *testing.T) {
	fixture := func(t *testing.T) *objectFixture {
		return newObjectFixture(t).
			object(1, testObject{name: "house", child: 2}).
			object(2, testObject{name: "box", parent: 1, child: 3}).
			object(3, testObject{name: "coin", parent: 2})
	}

	tests := []struct {
		name string
		a, b uint8
		want bool
	}{
		{name: "direct child", a: 2, b: 1, want: true},
		{name: "grandchild", a: 3, b: 1, want: false},
		{name: "the wrong way round", a: 1, b: 2, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := join(encodeVar(count2OP, 0x06, smallOp(tt.a), smallOp(tt.b)), branch2(true, 20))
			m := fixture(t).code(code...).machine()
			if got := execBranch(t, m, code); got != tt.want {
				t.Errorf("jin %d %d branched = %t, want %t", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestAttributeOpcodes covers test_attr, set_attr and clear_attr (S 15).
func TestAttributeOpcodes(t *testing.T) {
	const (
		numberTestAttr  = 0x0a
		numberSetAttr   = 0x0b
		numberClearAttr = 0x0c
	)

	fixture := func(t *testing.T) *objectFixture {
		return newObjectFixture(t).object(1, testObject{name: "lamp", attributes: []uint16{5}})
	}

	t.Run("test_attr", func(t *testing.T) {
		for _, tt := range []struct {
			attribute uint8
			want      bool
		}{{attribute: 5, want: true}, {attribute: 6, want: false}} {
			code := join(encodeVar(count2OP, numberTestAttr, smallOp(1), smallOp(tt.attribute)), branch2(true, 20))
			m := fixture(t).code(code...).machine()
			if got := execBranch(t, m, code); got != tt.want {
				t.Errorf("test_attr 1 %d branched = %t, want %t", tt.attribute, got, tt.want)
			}
		}
	})

	t.Run("set_attr and clear_attr", func(t *testing.T) {
		code := join(
			encodeVar(count2OP, numberSetAttr, smallOp(1), smallOp(31)),
			encodeVar(count2OP, numberClearAttr, smallOp(1), smallOp(5)),
		)
		m := fixture(t).code(code...).machine()
		mustStep(t, m)
		mustStep(t, m)

		set, err := m.mem.objectAttribute(1, 31)
		if err != nil {
			t.Fatalf("objectAttribute(1, 31) error = %v", err)
		}
		if !set {
			t.Errorf("attribute 31 = false after set_attr, want true")
		}
		if set, err = m.mem.objectAttribute(1, 5); err != nil {
			t.Fatalf("objectAttribute(1, 5) error = %v", err)
		}
		if set {
			t.Errorf("attribute 5 = true after clear_attr, want false")
		}
	})
}

// TestInsertAndRemoveOpcodes covers insert_obj and remove_obj as instructions,
// so that the dispatch as well as the tree helpers is exercised (S 15).
func TestInsertAndRemoveOpcodes(t *testing.T) {
	code := join(
		encodeVar(count2OP, 0x0e, smallOp(5), smallOp(1)), // insert_obj 5 1
		encodeShort(0x09, smallOp(3)),                     // remove_obj 3
	)
	m := familyFixture(t).code(code...).machine()
	mustStep(t, m)
	mustStep(t, m)

	if got := childrenOf(t, m.mem, 1); !equalObjects(got, []uint16{5, 2, 4}) {
		t.Errorf("children of object 1 = %v, want [5 2 4]", got)
	}
}

// TestPrintObj covers print_obj (S 15): it prints "the short name of object
// (the Z-encoded string in the object header, not a property)".
func TestPrintObj(t *testing.T) {
	t.Run("prints the short name", func(t *testing.T) {
		code := encodeShort(0x0a, smallOp(3))
		m := familyFixture(t).code(code...).machine()
		mustStep(t, m)
		if got := string(m.out.screen); got != "middle" {
			t.Errorf("output = %q, want %q", got, "middle")
		}
	})

	t.Run("an empty short name prints nothing", func(t *testing.T) {
		// S 12.4: a text-length of zero is a property table with no name.
		code := encodeShort(0x0a, smallOp(1))
		m := newObjectFixture(t).
			object(1, testObject{name: "placeholder"}).
			at(fixtureProperties, 0).
			code(code...).
			machine()
		mustStep(t, m)
		if got := string(m.out.screen); got != "" {
			t.Errorf("output = %q, want empty", got)
		}
	})

	t.Run("an invalid object number halts", func(t *testing.T) {
		// S 15, print_obj: "If the object number is invalid, the interpreter
		// should halt with a suitable error message."
		code := encodeShort(0x0a, smallOp(0))
		m := familyFixture(t).code(code...).machine()
		assertExecutionError(t, stepErr(t, m), fixtureCodeBase, ErrExecutionFault)
	})
}

// TestObjectShortNameUnterminated covers S 12.4 together with S 3.2: the
// text-length byte says how many words the short name occupies, and the last of
// those words must be the one with the end-of-string bit set. A name whose
// declared words never end is malformed rather than a licence to read on.
func TestObjectShortNameUnterminated(t *testing.T) {
	m := newObjectFixture(t).
		object(1, testObject{name: "thing"}).
		// One word of text with the end bit clear.
		at(fixtureProperties, 1, 0x00, 0x00, 0x00).
		memory()

	if _, err := m.objectShortName(1); !errors.Is(err, ErrInvalidText) {
		t.Errorf("objectShortName(1) error = %v, want one wrapping ErrInvalidText", err)
	}
}

// TestStatusLineName covers S 8.2.2: the short name of the object whose number
// is in the first global belongs on the left of the status line. S 8.2.2.1 asks
// interpreters to protect themselves when the story leaves an invalid number
// there, so a bad number leaves the name empty instead of ending the turn.
func TestStatusLineName(t *testing.T) {
	t.Run("names the object in the first global", func(t *testing.T) {
		m := familyFixture(t).machine()
		if err := m.writeGlobal(globalFirst, 3); err != nil {
			t.Fatalf("writeGlobal() error = %v", err)
		}
		if err := m.updateStatusLine(); err != nil {
			t.Fatalf("updateStatusLine() error = %v", err)
		}
		if m.status.Name != "middle" {
			t.Errorf("Name = %q, want %q", m.status.Name, "middle")
		}
		if m.status.Object != 3 {
			t.Errorf("Object = %d, want 3", m.status.Object)
		}
	})

	t.Run("an invalid object leaves the name empty", func(t *testing.T) {
		m := familyFixture(t).machine()
		if err := m.writeGlobal(globalFirst, 0); err != nil {
			t.Fatalf("writeGlobal() error = %v", err)
		}
		if err := m.updateStatusLine(); err != nil {
			t.Fatalf("updateStatusLine() error = %v, want nil: S 8.2.2.1 asks for protection, not a fault", err)
		}
		if m.status.Name != "" {
			t.Errorf("Name = %q, want empty", m.status.Name)
		}
	})
}

// loadZork1 loads the bundled Zork I story, skipping the test when the fixture
// is not present.
func loadZork1(t *testing.T) *Story {
	t.Helper()
	const path = "testdata/stories/zork1-r119-880429.z3"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("story fixture unavailable: %v", err)
	}
	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory(%s) error = %v, want nil", path, err)
	}
	return story
}

// TestZork1ObjectTree checks the object model against a real Version 3 story.
//
// The expectations were read out of the story image itself, by walking the
// tables exactly as S 12 describes and independently of the code under test:
// the object table is at the address in the header word at $0a ($03e6), its 31
// words of property defaults end at $0424, and object n has its 9-byte entry at
// $0424 + 9(n-1). The short names come from the property table each entry
// points at, decoded through the abbreviations table. The relationships below
// are the ones a player of Zork I can confirm: the mailbox stands outside the
// white house at West of House and the leaflet is inside it, the sword and the
// lantern begin in the living room, and both are children of it in the tree.
func TestZork1ObjectTree(t *testing.T) {
	m := newMemory(loadZork1(t))

	t.Run("short names", func(t *testing.T) {
		names := map[uint16]string{
			1:   "forest",
			64:  "West of House",
			75:  "Living Room",
			76:  "leaflet",
			146: "brass lantern",
			227: "sword",
			230: "small mailbox",
		}
		for number, want := range names {
			got, err := m.objectShortName(number)
			if err != nil {
				t.Fatalf("objectShortName(%d) error = %v", number, err)
			}
			if got != want {
				t.Errorf("objectShortName(%d) = %q, want %q", number, got, want)
			}
		}
	})

	t.Run("relationships", func(t *testing.T) {
		// The mailbox is at West of House and holds the leaflet.
		if parent, _, child := relatives(t, m, 230); parent != 64 || child != 76 {
			t.Errorf("small mailbox: parent = %d, child = %d, want 64 and 76", parent, child)
		}
		if parent, _, _ := relatives(t, m, 76); parent != 230 {
			t.Errorf("leaflet: parent = %d, want 230", parent)
		}
		// The sword heads the living room's chain of contents and the lantern
		// is in the same chain.
		if _, _, child := relatives(t, m, 75); child != 227 {
			t.Errorf("Living Room: child = %d, want 227", child)
		}
		contents := childrenOf(t, m, 75)
		found := false
		for _, number := range contents {
			if number == 146 {
				found = true
			}
		}
		if !found {
			t.Errorf("brass lantern is not among the contents of the Living Room: %v", contents)
		}
	})

	t.Run("moving an object relinks the chain", func(t *testing.T) {
		// Taking the leaflet moves it from the mailbox to the player's hands,
		// which is what insert_obj does; putting it back restores the tree.
		before := childrenOf(t, m, 230)
		if err := m.insertObject(76, 64); err != nil {
			t.Fatalf("insertObject(76, 64) error = %v", err)
		}
		if got := childrenOf(t, m, 230); len(got) != 0 {
			t.Errorf("contents of the mailbox = %v, want none", got)
		}
		if err := m.insertObject(76, 230); err != nil {
			t.Fatalf("insertObject(76, 230) error = %v", err)
		}
		if got := childrenOf(t, m, 230); !equalObjects(got, before) {
			t.Errorf("contents of the mailbox = %v, want %v", got, before)
		}
	})
}
