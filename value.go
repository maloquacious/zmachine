package zmachine

// Conversions between the Z-machine's 16-bit words and Go's integer types.
//
// The Z-machine holds numbers "in signed 2's complement 16-bit form" (S 2.2),
// but the same word is unsigned when it is an address, an object number or a
// bitmap. Which reading applies is a property of the instruction, not of the
// word, so every change of reading goes through one of these helpers rather
// than through a bare conversion that might silently pick up the host's int
// width or overflow behaviour.

// signed reinterprets a word as a signed 16-bit integer (S 2.2).
func signed(v uint16) int16 { return int16(v) }

// unsigned reinterprets a signed 16-bit integer as a word (S 2.2).
func unsigned(v int16) uint16 { return uint16(v) }

// signExtend reads the low bits of v as a two's complement number of that
// width and returns it as a signed 16-bit integer. bits must be between 1
// and 16.
//
// It is needed because not every signed quantity in a story occupies a whole
// word: a two-byte branch offset, for instance, is a signed 14-bit number
// (S 4.7), whose sign bit is bit 13 rather than bit 15.
func signExtend(v uint16, bits uint) int16 {
	shift := 16 - bits
	// Shifting the sign bit up to bit 15 discards the bits above the field;
	// the arithmetic shift back down copies the sign bit into them.
	return int16(v<<shift) >> shift
}
