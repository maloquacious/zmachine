package zmachine

import (
	"errors"
	"fmt"
)

// Sentinel errors classifying the major failure modes of the engine.
//
// Every error returned by this package wraps exactly one of these, so callers
// can classify failures with errors.Is without depending on message text.
var (
	// ErrInvalidStory reports a story file that is not a usable Version 3 story.
	ErrInvalidStory = errors.New("invalid Z3 story")

	// ErrInvalidState reports a saved state that cannot be restored.
	ErrInvalidState = errors.New("invalid saved state")

	// ErrInvalidOpcode reports an instruction that is not defined in Version 3.
	ErrInvalidOpcode = errors.New("invalid opcode")

	// ErrMemoryAccess reports a read or write that the Version 3 memory model
	// does not permit.
	ErrMemoryAccess = errors.New("invalid memory access")

	// ErrExecutionLimit reports that execution stopped because a host-imposed
	// limit was reached.
	ErrExecutionLimit = errors.New("execution limit exceeded")

	// ErrInvalidText reports encoded text that does not obey the Version 3
	// rules for Z-strings. It is distinct from ErrInvalidStory because strings
	// may be built in dynamic memory while the story runs, so a malformed
	// string is not necessarily a defect in the story file.
	ErrInvalidText = errors.New("invalid Z-string")
)

// Region names one of the three regions of the Z-machine memory map
// (Z-machine Standards Document 1.1, section 1.1).
type Region uint8

const (
	// RegionUnknown means the address does not lie inside the story image.
	RegionUnknown Region = iota
	// RegionDynamic is readable and writable memory, below the base of static memory.
	RegionDynamic
	// RegionStatic is readable but not writable memory.
	RegionStatic
	// RegionHigh holds routines and strings. Its bottom may overlap the top of
	// static memory, so an address reported as high memory may also be readable
	// as static memory.
	RegionHigh
)

// String returns the region name used in error messages.
func (r Region) String() string {
	switch r {
	case RegionDynamic:
		return "dynamic"
	case RegionStatic:
		return "static"
	case RegionHigh:
		return "high"
	default:
		return "unmapped"
	}
}

// MemoryOp distinguishes the kinds of memory access an error can describe.
type MemoryOp uint8

const (
	// MemoryRead is a load from story memory.
	MemoryRead MemoryOp = iota
	// MemoryWrite is a store into story memory.
	MemoryWrite
)

// String returns the operation name used in error messages.
func (o MemoryOp) String() string {
	if o == MemoryWrite {
		return "write"
	}
	return "read"
}

// StoryError describes why a story file was rejected. It always wraps
// ErrInvalidStory unless a caller constructs it otherwise.
type StoryError struct {
	// Field names the header field or table at fault, for example
	// "base of static memory". It is empty when no single field is responsible.
	Field string
	// Value is the offending value. It is only meaningful when Field is set.
	Value uint32
	// Detail explains what is wrong with the value.
	Detail string
	// Err is the sentinel this error is classified as.
	Err error
}

// Error implements error.
func (e *StoryError) Error() string {
	if e.Field == "" {
		return "zmachine: invalid story: " + e.Detail
	}
	return fmt.Sprintf("zmachine: invalid story: %s 0x%04x: %s", e.Field, e.Value, e.Detail)
}

// Unwrap returns the sentinel classifying this error.
func (e *StoryError) Unwrap() error { return e.Err }

// MemoryError describes a refused memory access. It always wraps
// ErrMemoryAccess unless a caller constructs it otherwise.
type MemoryError struct {
	// Op is the kind of access attempted.
	Op MemoryOp
	// Width is the number of bytes the access would have touched.
	Width int
	// Addr is the byte address of the first byte of the access.
	Addr uint32
	// Region is the region containing Addr, or RegionUnknown when Addr lies
	// outside the story image.
	Region Region
	// Detail explains why the access was refused.
	Detail string
	// Err is the sentinel this error is classified as.
	Err error
}

// Error implements error.
func (e *MemoryError) Error() string {
	if e.Region == RegionUnknown {
		return fmt.Sprintf("zmachine: %s of %d byte(s) at 0x%04x: %s",
			e.Op, e.Width, e.Addr, e.Detail)
	}
	return fmt.Sprintf("zmachine: %s of %d byte(s) at 0x%04x in %s memory: %s",
		e.Op, e.Width, e.Addr, e.Region, e.Detail)
}

// Unwrap returns the sentinel classifying this error.
func (e *MemoryError) Unwrap() error { return e.Err }

// DecodeError describes an instruction that could not be decoded. It wraps the
// sentinel that classifies the failure: ErrInvalidOpcode for an instruction
// Version 3 does not define, and the refused memory access - and so
// ErrMemoryAccess - for one that runs off the end of the story.
type DecodeError struct {
	// Addr is the byte address of the instruction, which is the program counter
	// it was decoded from.
	Addr uint32
	// Opcode is the first byte of the instruction. It is zero when the failure
	// was reading that byte itself.
	Opcode uint8
	// Detail explains what is wrong with the instruction.
	Detail string
	// Err is the error this one is classified as.
	Err error
}

// Error implements error.
func (e *DecodeError) Error() string {
	return fmt.Sprintf("zmachine: instruction at 0x%04x: opcode byte 0x%02x: %s", e.Addr, e.Opcode, e.Detail)
}

// Unwrap returns the error classifying this one.
func (e *DecodeError) Unwrap() error { return e.Err }

// TextError describes encoded text that could not be decoded. It always wraps
// ErrInvalidText unless a caller constructs it otherwise.
type TextError struct {
	// Addr is the byte address the string was read from, or zero when the text
	// did not come from story memory.
	Addr uint32
	// Detail explains what is wrong with the text.
	Detail string
	// Err is the sentinel this error is classified as.
	Err error
}

// Error implements error.
func (e *TextError) Error() string {
	return fmt.Sprintf("zmachine: Z-string at 0x%04x: %s", e.Addr, e.Detail)
}

// Unwrap returns the sentinel classifying this error.
func (e *TextError) Unwrap() error { return e.Err }

// storyErrorf builds a StoryError classified as ErrInvalidStory.
func storyErrorf(field string, value uint32, format string, args ...any) *StoryError {
	return &StoryError{
		Field:  field,
		Value:  value,
		Detail: fmt.Sprintf(format, args...),
		Err:    ErrInvalidStory,
	}
}

// textErrorf builds a TextError classified as ErrInvalidText.
func textErrorf(addr uint32, format string, args ...any) *TextError {
	return &TextError{
		Addr:   addr,
		Detail: fmt.Sprintf(format, args...),
		Err:    ErrInvalidText,
	}
}
