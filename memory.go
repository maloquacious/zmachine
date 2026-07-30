package zmachine

import "fmt"

// memory is one Machine's view of story memory (S 1.1).
//
// Dynamic memory is private to the Machine: it is copied from the story image
// when the view is created, so machines sharing a Story are fully isolated.
// Static and high memory are read-only, so the view borrows them from the
// Story rather than copying them.
//
// Addresses are byte addresses held in uint32 rather than the host's int, so
// that address arithmetic (including packed-address expansion, which can reach
// 0x1fffe) is explicit and cannot silently depend on the host word size.
type memory struct {
	story   *Story
	dynamic []byte // private copy of addresses [0, staticBase)
	static  []byte // shared view of addresses [staticBase, len(image)); never written
}

// newMemory builds a fresh memory view of story, with dynamic memory reset to
// the story's initial contents.
func newMemory(story *Story) *memory {
	base := story.staticBase
	dynamic := make([]byte, base)
	copy(dynamic, story.image[:base])
	shared := story.image[base:]
	return &memory{
		story:   story,
		dynamic: dynamic,
		// Cap the shared slice at its length so that no future append can
		// reallocate or write through it.
		static: shared[:len(shared):len(shared)],
	}
}

// size returns the number of addressable bytes: the whole story image.
func (m *memory) size() uint32 {
	return uint32(len(m.dynamic)) + uint32(len(m.static))
}

// dynamicSize returns the size of dynamic memory, which is also the base of
// static memory.
func (m *memory) dynamicSize() uint32 {
	return uint32(len(m.dynamic))
}

// regionOf reports which region contains addr, or RegionUnknown if addr is not
// part of the story image. Because the bottom of high memory may overlap the
// top of static memory (S 1.1), an address reported as high memory is still
// readable.
func (m *memory) regionOf(addr uint32) Region {
	switch {
	case addr >= m.size():
		return RegionUnknown
	case addr < m.dynamicSize():
		return RegionDynamic
	case addr < m.story.highBase:
		return RegionStatic
	default:
		return RegionHigh
	}
}

// readable reports whether the width bytes starting at addr are inside the
// story image. It is written to be safe against overflow of addr+width.
func (m *memory) readable(addr, width uint32) bool {
	size := m.size()
	return size >= width && addr <= size-width
}

// writable reports whether the width bytes starting at addr are inside dynamic
// memory (S 1.1.2: it is illegal for a game to write to static memory).
func (m *memory) writable(addr, width uint32) bool {
	size := m.dynamicSize()
	return size >= width && addr <= size-width
}

// readByte returns the byte at addr (S 1.1.1, S 1.1.2).
func (m *memory) readByte(addr uint32) (uint8, error) {
	if !m.readable(addr, 1) {
		return 0, m.accessError(MemoryRead, 1, addr, "beyond the end of the story (0x%04x)", m.size())
	}
	if addr < m.dynamicSize() {
		return m.dynamic[addr], nil
	}
	return m.static[addr-m.dynamicSize()], nil
}

// readWord returns the word stored at addr, most significant byte first
// (S 2.1). The word may straddle the boundary between dynamic and static
// memory, so its bytes are fetched individually.
func (m *memory) readWord(addr uint32) (uint16, error) {
	if !m.readable(addr, 2) {
		return 0, m.accessError(MemoryRead, 2, addr, "beyond the end of the story (0x%04x)", m.size())
	}
	high, err := m.readByte(addr)
	if err != nil {
		return 0, err
	}
	low, err := m.readByte(addr + 1)
	if err != nil {
		return 0, err
	}
	return uint16(high)<<8 | uint16(low), nil
}

// writeByte stores value at addr, which must lie in dynamic memory.
//
// Writes to the header are permitted here because the interpreter itself must
// set several header fields (S 11.1). Restrictions on which fields a story may
// alter belong to the instructions that write memory, not to this layer.
func (m *memory) writeByte(addr uint32, value uint8) error {
	if err := m.checkWrite(1, addr); err != nil {
		return err
	}
	m.dynamic[addr] = value
	return nil
}

// writeWord stores value at addr, most significant byte first. Both bytes must
// lie in dynamic memory.
func (m *memory) writeWord(addr uint32, value uint16) error {
	if err := m.checkWrite(2, addr); err != nil {
		return err
	}
	m.dynamic[addr] = uint8(value >> 8)
	m.dynamic[addr+1] = uint8(value)
	return nil
}

// checkWrite reports why a store of width bytes at addr is not permitted.
func (m *memory) checkWrite(width, addr uint32) error {
	if m.writable(addr, width) {
		return nil
	}
	if !m.readable(addr, width) {
		return m.accessError(MemoryWrite, width, addr, "beyond the end of the story (0x%04x)", m.size())
	}
	return m.accessError(MemoryWrite, width, addr, "outside dynamic memory (0x%04x)", m.dynamicSize())
}

// accessError builds a MemoryError classified as ErrMemoryAccess.
func (m *memory) accessError(op MemoryOp, width, addr uint32, format string, args ...any) *MemoryError {
	return &MemoryError{
		Op:     op,
		Width:  int(width),
		Addr:   addr,
		Region: m.regionOf(addr),
		Detail: fmt.Sprintf(format, args...),
		Err:    ErrMemoryAccess,
	}
}

// unpackAddress expands a packed address into the byte address of the routine
// or string it names. In Versions 1 to 3 the byte address is 2P (S 1.2.3).
func unpackAddress(packed uint16) uint32 {
	return uint32(packed) * packedScaleV3
}

// expandWordAddress expands a word address, which names an even byte address
// as that address divided by two (S 1.2.2). Word addresses are used only by
// the abbreviations table.
func expandWordAddress(word uint16) uint32 {
	return uint32(word) * wordAddressScale
}

// offsetAddress adds a byte offset to a byte address, reporting an error if
// the result leaves the Version 3 address space. Callers use it instead of
// bare arithmetic on story-derived values so that overflow cannot turn into an
// out-of-range index.
func offsetAddress(base uint32, offset int32) (uint32, error) {
	result := int64(base) + int64(offset)
	if result < 0 || result >= addressSpaceLimit {
		return 0, fmt.Errorf("zmachine: byte address 0x%04x offset by %d leaves the address space: %w",
			base, offset, ErrMemoryAccess)
	}
	return uint32(result), nil
}
