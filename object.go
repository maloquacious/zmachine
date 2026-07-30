package zmachine

import "fmt"

// The Version 3 object table (S 12).
//
// The table begins with the property defaults (S 12.2) and is followed by one
// entry per object. In Versions 1 to 3 there are at most 255 objects and each
// entry is nine bytes (S 12.3.1):
//
//	--the 32 attribute flags--  parent  sibling  child  properties
//	----32 bits in 4 bytes----  -------3 bytes--------  --2 bytes--
//
// Objects are numbered from 1 upward and object number 0 means "nothing",
// though "there is formally no such object" (S 12.3). Every accessor here
// therefore refuses object 0 rather than indexing the table with it; 0 is only
// ever a value read out of a parent, sibling or child field.
//
// The tree operations are the delicate part. Removing an object means
// relinking the sibling chain that its parent's child begins, and getting that
// wrong silently corrupts the whole tree rather than failing where the mistake
// was made. S 12.5 makes keeping the tree well-founded the game's
// responsibility and does not require the interpreter to check, but every walk
// of a chain here is bounded, so a tree that is already corrupt produces a
// contextual error instead of a hang.

const (
	// maxObjectsV3 is the largest object number Version 3 can name: "in
	// Versions 1 to 3, there are at most 255 objects" (S 12.3.1).
	maxObjectsV3 = 255

	// objectEntrySizeV3 is the size of one object entry (S 12.3.1).
	objectEntrySizeV3 = 9

	// Offsets of the fields within an object entry (S 12.3.1).
	objectAttributeOffset  = 0
	objectParentOffset     = 4
	objectSiblingOffset    = 5
	objectChildOffset      = 6
	objectPropertiesOffset = 7

	// objectAttributeCountV3 is the number of attribute flags each object
	// carries: "the 32 attribute flags" of S 12.3.1.
	objectAttributeCountV3 = 32

	// attributeBitsPerByte is how many attributes one byte of the attribute
	// field holds.
	attributeBitsPerByte = 8

	// objectNothing is the object number meaning "nothing" (S 12.3).
	objectNothing = 0

	// objectChainLimit bounds a walk along a sibling chain. There are at most
	// 255 objects and a well-founded tree names each of them at most once in
	// any one chain (S 12.5), so a longer walk means the chain has a cycle.
	objectChainLimit = maxObjectsV3
)

// objectAddress returns the byte address of the entry for an object (S 12.3.1).
//
// The whole entry must lie in dynamic memory: S 12.1 holds the object table
// there, and set_attr, insert_obj and remove_obj all write to it.
func (m *memory) objectAddress(number uint16) (uint32, error) {
	if number == objectNothing {
		return 0, fmt.Errorf("zmachine: object 0 means \"nothing\" and has no entry in the object table (S 12.3): %w",
			ErrExecutionFault)
	}
	if number > maxObjectsV3 {
		return 0, fmt.Errorf("zmachine: object %d: Version 3 has at most %d objects (S 12.3.1): %w",
			number, maxObjectsV3, ErrExecutionFault)
	}
	addr := m.story.objectTable + propertyDefaultsSizeV3 + uint32(number-1)*objectEntrySizeV3
	if !m.writable(addr, objectEntrySizeV3) {
		return 0, fmt.Errorf("zmachine: object %d: its %d-byte entry at 0x%04x is not in dynamic memory (0x%04x), where S 12.1 holds the object table: %w",
			number, objectEntrySizeV3, addr, m.dynamicSize(), ErrExecutionFault)
	}
	return addr, nil
}

// attributeAddress returns the address of the byte holding an attribute and the
// mask selecting it.
//
// S 12.3.1: the flags are "stored topmost bit first: e.g., attribute 0 is
// stored in bit 7 of the first byte, attribute 31 is stored in bit 0 of the
// fourth". Reading the bits the other way round is the classic way to get an
// object model subtly and completely wrong.
func (m *memory) attributeAddress(number, attribute uint16) (uint32, uint8, error) {
	entry, err := m.objectAddress(number)
	if err != nil {
		return 0, 0, err
	}
	if attribute >= objectAttributeCountV3 {
		return 0, 0, fmt.Errorf("zmachine: object %d: attribute %d: Version 3 objects have attributes 0 to %d (S 12.3.1): %w",
			number, attribute, objectAttributeCountV3-1, ErrExecutionFault)
	}
	addr := entry + objectAttributeOffset + uint32(attribute/attributeBitsPerByte)
	mask := uint8(0x80) >> (attribute % attributeBitsPerByte)
	return addr, mask, nil
}

// objectAttribute reports whether an object has an attribute (S 15, test_attr).
func (m *memory) objectAttribute(number, attribute uint16) (bool, error) {
	addr, mask, err := m.attributeAddress(number, attribute)
	if err != nil {
		return false, err
	}
	value, err := m.readByte(addr)
	if err != nil {
		return false, err
	}
	return value&mask != 0, nil
}

// setObjectAttribute turns an attribute on or off (S 15, set_attr, clear_attr).
func (m *memory) setObjectAttribute(number, attribute uint16, on bool) error {
	addr, mask, err := m.attributeAddress(number, attribute)
	if err != nil {
		return err
	}
	value, err := m.readByte(addr)
	if err != nil {
		return err
	}
	if on {
		value |= mask
	} else {
		value &^= mask
	}
	return m.writeByte(addr, value)
}

// objectRelative returns the object number held in one of the parent, sibling
// and child fields (S 12.3.1). They are single bytes in Version 3, so the
// result is always a valid object number or 0.
func (m *memory) objectRelative(number uint16, offset uint32) (uint16, error) {
	entry, err := m.objectAddress(number)
	if err != nil {
		return 0, err
	}
	value, err := m.readByte(entry + offset)
	if err != nil {
		return 0, err
	}
	return uint16(value), nil
}

// setObjectRelative stores an object number in one of those fields.
func (m *memory) setObjectRelative(number uint16, offset uint32, value uint16) error {
	entry, err := m.objectAddress(number)
	if err != nil {
		return err
	}
	if value > maxObjectsV3 {
		// S 12.3.1: "parent, sibling and child must all hold valid object
		// numbers", and in Version 3 the field is one byte wide, so a larger
		// number could not be stored without silently truncating it.
		return fmt.Errorf("zmachine: object %d: cannot hold object %d as a relative: Version 3 object fields are one byte (S 12.3.1): %w",
			number, value, ErrExecutionFault)
	}
	return m.writeByte(entry+offset, uint8(value))
}

// objectParent returns an object's parent, or 0 if it has none (S 15,
// get_parent).
func (m *memory) objectParent(number uint16) (uint16, error) {
	return m.objectRelative(number, objectParentOffset)
}

// objectSibling returns the next object in the tree, or 0 if there is none
// (S 15, get_sibling).
func (m *memory) objectSibling(number uint16) (uint16, error) {
	return m.objectRelative(number, objectSiblingOffset)
}

// objectChild returns the first object contained in an object, or 0 if it
// contains none (S 15, get_child).
func (m *memory) objectChild(number uint16) (uint16, error) {
	return m.objectRelative(number, objectChildOffset)
}

func (m *memory) setObjectParent(number, value uint16) error {
	return m.setObjectRelative(number, objectParentOffset, value)
}

func (m *memory) setObjectSibling(number, value uint16) error {
	return m.setObjectRelative(number, objectSiblingOffset, value)
}

func (m *memory) setObjectChild(number, value uint16) error {
	return m.setObjectRelative(number, objectChildOffset, value)
}

// removeObject detaches an object from its parent, so that it no longer has
// one. Its own children stay with it (S 15, remove_obj).
//
// The object is unlinked from the sibling chain its parent's child begins,
// which is the only place the parent records it (S 12.5(b)). Its sibling field
// is cleared as well: an object with a sibling must also have a parent
// (S 12.5(a)), so leaving the old sibling behind would leave the tree
// ill-founded.
func (m *memory) removeObject(number uint16) error {
	parent, err := m.objectParent(number)
	if err != nil {
		return err
	}
	sibling, err := m.objectSibling(number)
	if err != nil {
		return err
	}

	if parent != objectNothing {
		if err := m.unlinkFromParent(parent, number, sibling); err != nil {
			return err
		}
	}
	if err := m.setObjectParent(number, objectNothing); err != nil {
		return err
	}
	return m.setObjectSibling(number, objectNothing)
}

// unlinkFromParent removes number from parent's chain of children, putting
// successor in its place. successor is the object's own sibling, which is
// whatever followed it in the chain.
func (m *memory) unlinkFromParent(parent, number, successor uint16) error {
	first, err := m.objectChild(parent)
	if err != nil {
		return err
	}
	// The object may be the first child, in which case the parent itself points
	// at it and there is no chain to walk.
	if first == number {
		return m.setObjectChild(parent, successor)
	}

	current := first
	for steps := 0; current != objectNothing; steps++ {
		if steps >= objectChainLimit {
			return fmt.Errorf("zmachine: object %d: the child list of its parent %d is more than %d objects long, so the object tree is not well-founded (S 12.5): %w",
				number, parent, objectChainLimit, ErrExecutionFault)
		}
		next, err := m.objectSibling(current)
		if err != nil {
			return err
		}
		if next == number {
			return m.setObjectSibling(current, successor)
		}
		current = next
	}
	// S 12.5(b): an object is the parent of exactly those objects in the
	// sibling list of its child. Reaching here means the story has already
	// broken that, and continuing would leave the tree in a worse state.
	return fmt.Errorf("zmachine: object %d claims parent %d but is not in that object's child list, so the object tree is not well-founded (S 12.5): %w",
		number, parent, ErrExecutionFault)
}

// insertObject moves an object to become the first child of a destination
// object, taking its own children with it (S 15, insert_obj).
//
// The object may start anywhere in the tree, including with no parent at all.
func (m *memory) insertObject(number, destination uint16) error {
	// Both entries are checked before anything is written, so a bad object
	// number cannot leave the tree half-modified.
	if _, err := m.objectAddress(number); err != nil {
		return err
	}
	if _, err := m.objectAddress(destination); err != nil {
		return err
	}
	if number == destination {
		// S 12.5(c) gives every object a level one greater than its parent's,
		// which an object that is its own parent cannot have. Allowing it would
		// build a cycle that later walks would have to unpick.
		return fmt.Errorf("zmachine: object %d cannot be inserted into itself, which would leave the object tree not well-founded (S 12.5): %w",
			number, ErrExecutionFault)
	}

	if err := m.removeObject(number); err != nil {
		return err
	}
	// S 15, insert_obj: after the operation the child of the destination is the
	// object, and the sibling of the object is whatever was previously that
	// child.
	previous, err := m.objectChild(destination)
	if err != nil {
		return err
	}
	if err := m.setObjectSibling(number, previous); err != nil {
		return err
	}
	if err := m.setObjectChild(destination, number); err != nil {
		return err
	}
	return m.setObjectParent(number, destination)
}

// objectShortName returns the short name held in the header of an object's
// property table (S 12.4). It is what print_obj prints and what belongs on the
// left of the status line (S 8.2.2).
func (m *memory) objectShortName(number uint16) (string, error) {
	table, err := m.propertyTableAddress(number)
	if err != nil {
		return "", err
	}
	// S 12.4: the header is a text-length byte giving the number of 2-byte
	// words of text, followed by that text.
	words, err := m.readByte(table)
	if err != nil {
		return "", err
	}
	if words == 0 {
		// A length of zero is a property table with no short name at all, which
		// is legal and prints nothing.
		return "", nil
	}

	length := uint32(words) * zstringWordSize
	text := table + 1
	if !m.readable(text, length) {
		return "", fmt.Errorf("zmachine: object %d: its short name of %d byte(s) at 0x%04x runs past the end of the story (0x%04x): %w",
			number, length, text, m.size(), ErrMemoryAccess)
	}
	// The name is copied out and decoded from the declared number of words, so
	// that the text-length byte of S 12.4 bounds the decode rather than the
	// end-of-string bit alone. A name whose words do not contain that bit is
	// malformed and is reported as such.
	encoded := make([]byte, length)
	for i := range encoded {
		b, err := m.readByte(text + uint32(i))
		if err != nil {
			return "", err
		}
		encoded[i] = b
	}
	chars, _, err := unpackZChars(encoded)
	if err != nil {
		return "", fmt.Errorf("zmachine: object %d: short name at 0x%04x: %w", number, text, err)
	}
	return decodeZText(chars, text, func(index uint16) (string, error) {
		return abbreviationText(m, index)
	})
}
