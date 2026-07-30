package zmachine

import "fmt"

// Object property tables (S 12.2, S 12.4).
//
// Each object points at its own property table, which may lie anywhere in
// dynamic memory. The table begins with the object's short name (S 12.4) and
// the properties follow it, "listed in descending numerical order. (This order
// is essential and is not a matter of convention.)"
//
// In Versions 1 to 3 each property is a size byte followed by its data
// (S 12.4.1):
//
//	size byte   the actual property data
//	--1 byte--  ---between 1 and 8 bytes---
//
// where the size byte is "32 times the number of data bytes minus one, plus the
// property number". A size byte of 0 ends the list, and no other multiple of 32
// is legal, because that would name property 0.
//
// Every walk of a property list is bounded. There are 31 property numbers and a
// well-formed list is in strictly descending order, so it can hold no more than
// 31 entries; a list that runs longer is corrupt and is reported rather than
// followed.

const (
	// propertyNumberMask selects the property number from a size byte, and
	// propertySizeShift the data length minus one (S 12.4.1).
	propertyNumberMask = 0x1f
	propertySizeShift  = 5

	// propertyNumberMaxV3 is the largest property number Version 3 defines. The
	// number occupies the bottom five bits of the size byte and 0 terminates
	// the list, so the usable numbers are 1 to 31 (S 12.4.1). It is also the
	// greatest number of properties one object can have, and so the bound on
	// walking a property list.
	propertyNumberMaxV3 = 31

	// propertyWordMax is the greatest length get_prop and put_prop accept. Both
	// are defined only for properties of one or two bytes (S 15).
	propertyWordMax = 2
)

// propertyEntry is one property in an object's property table (S 12.4.1).
type propertyEntry struct {
	// number is the property number, between 1 and 31.
	number uint8
	// length is the number of data bytes, between 1 and 8.
	length uint8
	// data is the byte address of the first data byte, which is what
	// get_prop_addr reports (S 15).
	data uint32
	// next is the byte address of the following size byte.
	next uint32
}

// propertyTableAddress returns the byte address of an object's property table
// (S 12.3.1).
func (m *memory) propertyTableAddress(number uint16) (uint32, error) {
	entry, err := m.objectAddress(number)
	if err != nil {
		return 0, err
	}
	addr, err := m.readWord(entry + objectPropertiesOffset)
	if err != nil {
		return 0, err
	}
	return uint32(addr), nil
}

// firstPropertyAddress returns the address of the first size byte of an
// object's property list, which follows the short name in the table header
// (S 12.4).
func (m *memory) firstPropertyAddress(number uint16) (uint32, error) {
	table, err := m.propertyTableAddress(number)
	if err != nil {
		return 0, err
	}
	words, err := m.readByte(table)
	if err != nil {
		return 0, err
	}
	// A table address is at most $ffff and the name at most 255 words, so this
	// stays well inside the Version 3 address space.
	return table + 1 + uint32(words)*zstringWordSize, nil
}

// readPropertyEntry reads the property whose size byte is at addr. It reports
// false when the byte is the zero that terminates the list (S 12.4.1).
func (m *memory) readPropertyEntry(addr uint32) (propertyEntry, bool, error) {
	size, err := m.readByte(addr)
	if err != nil {
		return propertyEntry{}, false, err
	}
	if size == 0 {
		return propertyEntry{}, false, nil
	}

	entry := propertyEntry{
		number: size & propertyNumberMask,
		length: size>>propertySizeShift + 1,
		data:   addr + 1,
	}
	if entry.number == 0 {
		// S 12.4.1: "It is otherwise illegal for a size byte to be a multiple
		// of 32", because the bottom five bits would name property 0.
		return propertyEntry{}, false, fmt.Errorf("zmachine: property size byte 0x%02x at 0x%04x names property 0, which S 12.4.1 forbids: %w",
			size, addr, ErrExecutionFault)
	}
	entry.next = entry.data + uint32(entry.length)
	if !m.readable(entry.data, uint32(entry.length)) {
		return propertyEntry{}, false, fmt.Errorf("zmachine: property %d at 0x%04x: its %d data byte(s) run past the end of the story (0x%04x): %w",
			entry.number, addr, entry.length, m.size(), ErrMemoryAccess)
	}
	return entry, true, nil
}

// forEachProperty walks an object's property list in the order it is stored,
// calling visit for each entry until visit returns false or the list ends.
func (m *memory) forEachProperty(number uint16, visit func(propertyEntry) bool) error {
	addr, err := m.firstPropertyAddress(number)
	if err != nil {
		return err
	}
	first := addr

	for count := 0; count < propertyNumberMaxV3; count++ {
		entry, ok, err := m.readPropertyEntry(addr)
		if err != nil {
			return err
		}
		if !ok || !visit(entry) {
			return nil
		}
		addr = entry.next
	}
	// S 12.4.1 numbers properties 1 to 31 and S 12.4 lists them in descending
	// order, so no well-formed list holds more than 31 of them. A longer one
	// has lost its terminating zero byte, and following it would walk over the
	// rest of dynamic memory.
	return fmt.Errorf("zmachine: object %d: its property list at 0x%04x holds more than the %d properties Version 3 defines and has no terminating zero byte (S 12.4.1): %w",
		number, first, propertyNumberMaxV3, ErrExecutionFault)
}

// findProperty returns an object's property of the given number, reporting
// whether the object provides it at all.
func (m *memory) findProperty(number, property uint16) (propertyEntry, bool, error) {
	var found propertyEntry
	var ok bool
	err := m.forEachProperty(number, func(entry propertyEntry) bool {
		if uint16(entry.number) == property {
			found, ok = entry, true
			return false
		}
		return true
	})
	if err != nil {
		return propertyEntry{}, false, err
	}
	return found, ok, nil
}

// propertyDefault returns the default value of a property, which is what
// reading a property an object does not provide gives (S 12.2).
func (m *memory) propertyDefault(property uint16) (uint16, error) {
	if property == 0 || property > propertyNumberMaxV3 {
		return 0, fmt.Errorf("zmachine: property %d: Version 3 numbers properties 1 to %d (S 12.4.1): %w",
			property, propertyNumberMaxV3, ErrExecutionFault)
	}
	// S 12.2: the defaults table begins the object table and holds 31 words in
	// Versions 1 to 3; the n-th entry is the default for property n. LoadStory
	// has already placed the whole table inside dynamic memory.
	return m.readWord(m.story.objectTable + uint32(property-1)*wordAddressScale)
}

// propertyValue reads a property from an object, "resulting in the default
// value if it had no such declared property" (S 15, get_prop).
//
// A property of one byte reads as that byte and one of two bytes as a word.
// S 15 leaves the result unspecified for anything longer, so this reports the
// story's mistake rather than inventing a value.
func (m *memory) propertyValue(number, property uint16) (uint16, error) {
	entry, ok, err := m.findProperty(number, property)
	if err != nil {
		return 0, err
	}
	if !ok {
		return m.propertyDefault(property)
	}

	switch entry.length {
	case 1:
		value, err := m.readByte(entry.data)
		return uint16(value), err
	case propertyWordMax:
		return m.readWord(entry.data)
	default:
		return 0, fmt.Errorf("zmachine: object %d: property %d is %d bytes long, and S 15 defines get_prop only for properties of 1 or 2 bytes: %w",
			number, property, entry.length, ErrExecutionFault)
	}
}

// propertyAddress returns the byte address of an object's property data, or 0
// when the object does not provide the property (S 15, get_prop_addr).
func (m *memory) propertyAddress(number, property uint16) (uint16, error) {
	entry, ok, err := m.findProperty(number, property)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	if entry.data > 0xffff {
		// The result is a word, so an address the story could not name back is
		// worse than useless: it would silently become a different address.
		return 0, fmt.Errorf("zmachine: object %d: the data of property %d lies at 0x%04x, which does not fit in the word get_prop_addr returns: %w",
			number, property, entry.data, ErrExecutionFault)
	}
	return uint16(entry.data), nil
}

// propertyLength returns the number of data bytes of the property whose data
// begins at addr (S 15, get_prop_len). The size byte is the byte before the
// data.
func (m *memory) propertyLength(addr uint16) (uint16, error) {
	if addr == 0 {
		// S 15: "@get_prop_len 0 must return 0. This is required by some
		// Infocom games and files generated by old versions of Inform."
		return 0, nil
	}
	size, err := m.readByte(uint32(addr) - 1)
	if err != nil {
		return 0, err
	}
	if size == 0 {
		// A zero size byte terminates a property list (S 12.4.1), so there is
		// no property here to measure.
		return 0, fmt.Errorf("zmachine: get_prop_len: the byte before 0x%04x is zero, so no property begins there (S 12.4.1): %w",
			addr, ErrExecutionFault)
	}
	return uint16(size>>propertySizeShift) + 1, nil
}

// nextPropertyNumber returns the number of the property after the given one in
// an object's list, or 0 at the end of the list. Asked for property 0 it
// returns the first property the object provides (S 15, get_next_prop).
func (m *memory) nextPropertyNumber(number, property uint16) (uint16, error) {
	// Asking for the property after property 0 is the request for the first
	// property, so the walk starts already looking for the next entry.
	seen := property == 0
	var next uint16

	err := m.forEachProperty(number, func(entry propertyEntry) bool {
		if seen {
			next = uint16(entry.number)
			return false
		}
		if uint16(entry.number) == property {
			seen = true
		}
		return true
	})
	if err != nil {
		return 0, err
	}
	if !seen {
		// S 15: "It is illegal to try to find the next property of a property
		// which does not exist, and an interpreter should halt with an error
		// message (if it can efficiently check this condition)." Walking the
		// list has just checked it.
		return 0, fmt.Errorf("zmachine: object %d does not provide property %d, so it has no next property (S 15, get_next_prop): %w",
			number, property, ErrExecutionFault)
	}
	return next, nil
}

// putProperty writes a value to a property of an object (S 15, put_prop).
//
// A property of one byte takes only the least significant byte of the value,
// so storing -1 into it leaves 255.
func (m *memory) putProperty(number, property, value uint16) error {
	entry, ok, err := m.findProperty(number, property)
	if err != nil {
		return err
	}
	if !ok {
		// S 15: "If the property does not exist for that object, the
		// interpreter should halt with a suitable error message."
		return fmt.Errorf("zmachine: object %d does not provide property %d (S 15, put_prop): %w",
			number, property, ErrExecutionFault)
	}

	switch entry.length {
	case 1:
		return m.writeByte(entry.data, uint8(value))
	case propertyWordMax:
		return m.writeWord(entry.data, value)
	default:
		return fmt.Errorf("zmachine: object %d: property %d is %d bytes long, and S 15 leaves put_prop undefined for properties of more than %d bytes: %w",
			number, property, entry.length, propertyWordMax, ErrExecutionFault)
	}
}
