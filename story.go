// Package zmachine implements a headless Z-machine Version 3 execution engine.
//
// It is built for a host that advances a story one command at a time: given a
// validated story, an optional saved state and at most one line of input, it
// executes until the next input boundary, clean termination, cancellation, a
// limit or a fault, then returns the captured output and a resumable state. It
// never blocks for input, never prints, and touches nothing outside the process
// - no filesystem, no terminal, no environment, no process-global state.
//
// # Types
//
// A Story holds validated, immutable story data and is safe for concurrent use.
// Each Machine built from a Story owns its own mutable execution state, so many
// independent sessions may share one loaded story. A Machine is not safe for
// concurrent use, and is not meant to be kept: creating one is cheap.
//
// # The request lifecycle
//
// Loading a story is the expensive step and is done once. Everything after that
// is per request:
//
//	machine, err := zmachine.New(story)
//	if err != nil {
//		return err
//	}
//	if len(saved) != 0 {
//		if err := machine.Restore(saved); err != nil {
//			return err
//		}
//	}
//	result, err := machine.Run(ctx, command)
//	if err != nil {
//		return err
//	}
//	store(result.State)
//	send(result.Output)
//
// A story that has not begun is started with Start rather than Run, since a new
// game usually prints its banner before asking for anything. Either call
// returns a Result whose State resumes execution exactly where it stopped, so
// the Machine may be dropped as soon as the Result is in hand. Doing that on
// every turn is observably the same as keeping one Machine alive throughout.
//
// # Untrusted input
//
// Stories and saved states are both treated as hostile binary input. Every
// address, length and count derived from either is checked before it is used to
// index, allocate or slice, and malformed input is reported as an error - never
// a panic. Errors wrap a sentinel (ErrInvalidStory, ErrInvalidState,
// ErrExecutionFault and the rest) so a host can classify a failure with
// errors.Is, and carry the program counter, opcode and address in a typed error
// so it can be diagnosed.
//
// # Version 3 only
//
// The Z-machine semantics implemented here follow the Z-Machine Standards
// Document 1.1; section references in comments refer to that document. Only
// Version 3 is implemented, and LoadStory rejects every other version.
package zmachine

import (
	"encoding/binary"
)

// Header field offsets (S 11.1). Only the fields a Version 3 interpreter must
// deal with are named here.
const (
	hdrVersion       = 0x00 // version number
	hdrFlags1        = 0x01 // flags 1
	hdrRelease       = 0x02 // release number (word)
	hdrHighBase      = 0x04 // base of high memory (byte address, word)
	hdrInitialPC     = 0x06 // initial value of the program counter (byte address, word)
	hdrDictionary    = 0x08 // location of the dictionary (byte address, word)
	hdrObjectTable   = 0x0a // location of the object table (byte address, word)
	hdrGlobals       = 0x0c // location of the global variables table (byte address, word)
	hdrStaticBase    = 0x0e // base of static memory (byte address, word)
	hdrFlags2        = 0x10 // flags 2 (word)
	hdrSerial        = 0x12 // serial code (6 ASCII bytes)
	hdrAbbreviations = 0x18 // location of the abbreviations table (byte address, word)
	hdrFileLength    = 0x1a // length of file, scaled (word)
	hdrChecksum      = 0x1c // checksum of file (word)
)

const (
	// headerSize is the size of the header, and therefore the minimum size of
	// dynamic memory (S 1.1, S 1.1.1.1).
	headerSize = 64

	// versionV3 is the only story version this engine accepts.
	versionV3 = 3

	// maxStoryLength is the maximum permitted length of a Version 1-3 story
	// file: 128K (S 1.1.4).
	maxStoryLength = 128 * 1024

	// fileLengthScaleV3 is the constant the header file length is divided by in
	// Versions 1 to 3 (S 11.1.6).
	fileLengthScaleV3 = 2

	// packedScaleV3 is the multiplier that turns a packed address into a byte
	// address in Versions 1 to 3 (S 1.2.3).
	packedScaleV3 = 2

	// wordAddressScale is the multiplier that turns a word address into a byte
	// address (S 1.2.2).
	wordAddressScale = 2

	// checksumBase is the first byte included in the story checksum (S 15, verify).
	checksumBase = 0x40

	// propertyDefaultsSizeV3 is the size in bytes of the property defaults
	// table that begins the object table: 31 words in Versions 1 to 3 (S 12.2).
	propertyDefaultsSizeV3 = 31 * 2

	// globalsTableSize is the size in bytes of the global variables table:
	// 240 words, for variables $10 to $ff (S 6.2).
	globalsTableSize = 240 * 2

	// abbreviationsTableSizeV3 is the size in bytes of the abbreviations table:
	// 96 word addresses, three blocks of 32 (S 3.3).
	abbreviationsTableSizeV3 = 96 * 2

	// dictionaryEntryLengthMinV3 is the smallest legal dictionary entry length
	// in Versions 1 to 3: four bytes of encoded text plus zero data bytes
	// (S 13.2, S 13.3).
	dictionaryEntryLengthMinV3 = 4

	// addressSpaceLimit is one past the highest byte address a Version 3 story
	// can name. A story is at most 128K (S 1.1.4), which also covers the
	// largest expanded packed address, 2 * $ffff = $1fffe (S 1.2.3).
	addressSpaceLimit = maxStoryLength
)

// Story is a validated, immutable Version 3 story file.
//
// A Story is safe for concurrent use by any number of Machines. It never
// changes after LoadStory returns.
type Story struct {
	// image is the story image, truncated to the length declared in the header.
	// LoadStory copies the caller's bytes, so nothing outside this package can
	// mutate it. It must never be written to.
	image []byte

	version  uint8
	flags1   uint8
	flags2   uint16
	release  uint16
	serial   [6]byte
	checksum uint16 // checksum recorded in the header
	computed uint16 // checksum computed from the image (S 15, verify)

	length        uint32 // effective image length, equal to len(image)
	staticBase    uint32 // base of static memory; also the size of dynamic memory
	highBase      uint32 // base of high memory
	initialPC     uint32 // byte address of the first instruction
	dictionary    uint32 // byte address of the dictionary
	objectTable   uint32 // byte address of the object table
	globals       uint32 // byte address of the global variables table
	abbreviations uint32 // byte address of the abbreviations table, or 0 if absent
}

// LoadStory validates data as a Version 3 story file and returns an immutable
// Story. The returned Story owns a private copy of the story image, so the
// caller may reuse or modify data afterwards.
//
// Every header address and table extent is checked before use. Malformed input
// is reported as an error wrapping ErrInvalidStory; it never panics.
func LoadStory(data []byte) (*Story, error) {
	if len(data) < headerSize {
		return nil, storyErrorf("", 0, "story is %d bytes; the %d-byte header is truncated", len(data), headerSize)
	}
	// S 1.1.4: a Version 1-3 story file may not exceed 128K. Rejecting larger
	// input here also bounds every address computed below.
	if len(data) > maxStoryLength {
		return nil, storyErrorf("", 0, "story is %d bytes; Version 3 stories may not exceed %d bytes", len(data), maxStoryLength)
	}

	word := func(offset uint32) uint32 {
		return uint32(binary.BigEndian.Uint16(data[offset : offset+2]))
	}

	s := &Story{
		version: data[hdrVersion],
		flags1:  data[hdrFlags1],
		flags2:  uint16(word(hdrFlags2)),
		release: uint16(word(hdrRelease)),
	}
	copy(s.serial[:], data[hdrSerial:hdrSerial+6])

	if s.version != versionV3 {
		return nil, storyErrorf("version number", uint32(s.version), "only Version %d stories are supported", versionV3)
	}

	// S 11.1.6: the header holds the file length divided by 2 in Versions 1 to
	// 3. Some early Version 3 files leave it (and the checksum) zero, in which
	// case the whole input is the image.
	declared := word(hdrFileLength)
	s.checksum = uint16(word(hdrChecksum))
	if declared == 0 {
		s.length = uint32(len(data))
	} else {
		s.length = declared * fileLengthScaleV3
		if s.length > uint32(len(data)) {
			return nil, storyErrorf("length of file", s.length, "story is only %d bytes", len(data))
		}
	}
	// S 15 (verify): the file may legally be longer than the declared length,
	// but the trailing bytes are padding and are not part of the story.
	s.image = make([]byte, s.length)
	copy(s.image, data[:s.length])

	// S 1.1: dynamic memory runs from 0 to the base of static memory and must
	// contain at least the 64-byte header; static memory follows it.
	s.staticBase = word(hdrStaticBase)
	if s.staticBase < headerSize {
		return nil, storyErrorf("base of static memory", s.staticBase, "dynamic memory must contain at least the %d-byte header", headerSize)
	}
	if s.staticBase > s.length {
		return nil, storyErrorf("base of static memory", s.staticBase, "beyond the end of the story (0x%04x)", s.length)
	}

	// S 1.1: high memory may overlap the top of static memory but never
	// dynamic memory, so it begins at or above the base of static memory.
	s.highBase = word(hdrHighBase)
	if s.highBase < s.staticBase {
		return nil, storyErrorf("base of high memory", s.highBase, "below the base of static memory (0x%04x)", s.staticBase)
	}
	if s.highBase > s.length {
		return nil, storyErrorf("base of high memory", s.highBase, "beyond the end of the story (0x%04x)", s.length)
	}

	// S 11.1: in Version 3 the word at $06 is the byte address of the first
	// instruction, not a packed address.
	s.initialPC = word(hdrInitialPC)
	if s.initialPC < headerSize {
		return nil, storyErrorf("initial program counter", s.initialPC, "inside the header")
	}
	if s.initialPC >= s.length {
		return nil, storyErrorf("initial program counter", s.initialPC, "beyond the end of the story (0x%04x)", s.length)
	}

	// S 12.1: the object table is held in dynamic memory. Its property
	// defaults table occupies the first 62 bytes in Version 3 (S 12.2); object
	// entries follow and must be writable.
	s.objectTable = word(hdrObjectTable)
	if s.objectTable < headerSize {
		return nil, storyErrorf("object table", s.objectTable, "inside the header")
	}
	if s.objectTable+propertyDefaultsSizeV3 > s.staticBase {
		return nil, storyErrorf("object table", s.objectTable, "property defaults table (%d bytes) does not fit in dynamic memory (0x%04x)", propertyDefaultsSizeV3, s.staticBase)
	}

	// S 6.2: the 240-word global variables table is held in dynamic memory.
	s.globals = word(hdrGlobals)
	if s.globals < headerSize {
		return nil, storyErrorf("global variables table", s.globals, "inside the header")
	}
	if s.globals+globalsTableSize > s.staticBase {
		return nil, storyErrorf("global variables table", s.globals, "%d bytes do not fit in dynamic memory (0x%04x)", globalsTableSize, s.staticBase)
	}

	// S 3.3: the abbreviations table holds 96 word addresses in Version 3. A
	// story that uses no abbreviations may leave the header entry zero; any
	// other value must address a complete table inside the story.
	s.abbreviations = word(hdrAbbreviations)
	if s.abbreviations != 0 {
		if s.abbreviations < headerSize {
			return nil, storyErrorf("abbreviations table", s.abbreviations, "inside the header")
		}
		if s.abbreviations+abbreviationsTableSizeV3 > s.length {
			return nil, storyErrorf("abbreviations table", s.abbreviations, "%d bytes extend beyond the end of the story (0x%04x)", abbreviationsTableSizeV3, s.length)
		}
	}

	// S 13.1, S 13.2: the dictionary header gives the word separators, the
	// entry length and the number of entries. Validate the whole table now so
	// that lookups later cannot run off the end of the story.
	s.dictionary = word(hdrDictionary)
	if err := s.validateDictionary(); err != nil {
		return nil, err
	}

	// S 15 (verify): the checksum is the sum of the bytes from $0040 to the
	// declared end of the file, modulo $10000. A mismatch is not fatal - early
	// Version 3 stories have no checksum at all, and the verify instruction
	// reports the result to the story - so it is recorded rather than enforced.
	s.computed = computeChecksum(s.image)

	return s, nil
}

// validateDictionary checks that the dictionary header and every entry lie
// inside the story image (S 13.2, S 13.3).
func (s *Story) validateDictionary() error {
	addr := s.dictionary
	if addr < headerSize {
		return storyErrorf("dictionary", addr, "inside the header")
	}
	// The header is: a separator count byte, that many separator bytes, an
	// entry length byte and a two-byte entry count.
	if addr >= s.length {
		return storyErrorf("dictionary", addr, "beyond the end of the story (0x%04x)", s.length)
	}
	separators := uint32(s.image[addr])
	headerEnd := addr + 1 + separators + 1 + 2
	if headerEnd > s.length {
		return storyErrorf("dictionary", addr, "header with %d word separators extends beyond the end of the story (0x%04x)", separators, s.length)
	}
	entryLength := uint32(s.image[addr+1+separators])
	if entryLength < dictionaryEntryLengthMinV3 {
		return storyErrorf("dictionary", addr, "entry length %d is less than the Version 3 minimum of %d", entryLength, dictionaryEntryLengthMinV3)
	}
	// A negative count marks an unsorted user dictionary (S 15, tokenise); the
	// dictionary named in the header is the sorted main dictionary.
	count := int16(binary.BigEndian.Uint16(s.image[addr+1+separators+1 : addr+1+separators+3]))
	if count < 0 {
		return storyErrorf("dictionary", addr, "entry count %d is negative", count)
	}
	// entryLength <= 255 and count <= 32767, so this cannot overflow uint32.
	if headerEnd+uint32(count)*entryLength > s.length {
		return storyErrorf("dictionary", addr, "%d entries of %d bytes extend beyond the end of the story (0x%04x)", count, entryLength, s.length)
	}
	return nil
}

// computeChecksum sums the bytes of the story image from $0040 onwards, modulo
// $10000 (S 15, verify).
func computeChecksum(image []byte) uint16 {
	if len(image) <= checksumBase {
		return 0
	}
	var sum uint16
	for _, b := range image[checksumBase:] {
		sum += uint16(b)
	}
	return sum
}

// Version reports the Z-machine version of the story. It is always 3.
func (s *Story) Version() uint8 { return s.version }

// Release reports the release number recorded in the header.
func (s *Story) Release() uint16 { return s.release }

// Serial reports the six-character serial code recorded in the header,
// conventionally the compilation date as YYMMDD.
func (s *Story) Serial() string { return string(s.serial[:]) }

// Checksum reports the checksum recorded in the header. It is zero in early
// Version 3 stories that carry no checksum.
func (s *Story) Checksum() uint16 { return s.checksum }

// Size reports the length in bytes of the story image, which is the file length
// declared in the header when the story declares one.
func (s *Story) Size() int { return len(s.image) }
