// Package prng provides the random number generators a Z-machine may own.
//
// Section 2.4 of the Z-Machine Standards Document requires two states rather
// than one algorithm: a "random" state, whose sequence the story cannot
// predict, and a "predictable" state, which a given seed always reproduces. It
// says nothing about which numbers either state produces, so two conforming
// interpreters given the same seed will disagree.
//
// That freedom is why a Source is an interface. PCG is the engine's own
// generator and is what every ordinary machine uses. Comparing the engine's
// behaviour against another interpreter's, though, means drawing the numbers
// that interpreter would draw, which no general-purpose generator does by
// accident; see Frotz.
//
// A Source is owned by one machine. Nothing here is held in a package-level
// variable, and no generator is shared between machines.
package prng

import (
	crand "crypto/rand"
	"errors"
	"fmt"
)

// ErrInvalidState classifies a generator state that cannot be read. Saved
// states are untrusted, so a state that means nothing is refused rather than
// accepted as some other sequence.
//
// The engine wraps this in its own ErrInvalidState, so a caller classifying a
// failed restore finds it there.
var ErrInvalidState = errors.New("invalid random generator state")

// Source is a random number generator a machine can own.
type Source interface {
	// Draw returns a value between 1 and n inclusive. n is always positive:
	// section 15 gives the other cases their own meaning, and the random
	// opcode handles them before reaching here.
	Draw(n uint16) uint16

	// SeedWith puts the generator into the predictable state of section 2.4.2.
	// The same seed must always produce the same sequence.
	SeedWith(seed uint64)

	// Reseed puts the generator into the unpredictable state of section 2.4.
	Reseed() error

	// MarshalState returns the generator's state, and UnmarshalState restores
	// one, so that a generator can cross a request boundary. UnmarshalState
	// rejects states it does not recognise with an error wrapping
	// ErrInvalidState.
	MarshalState() ([]byte, error)
	UnmarshalState(data []byte) error

	// Kind identifies the generator in a saved state, so that restoring picks
	// the generator the state was written by rather than misreading it.
	Kind() uint8
}

// The generator kinds recorded in a saved state.
const (
	KindPCG   uint8 = 1
	KindFrotz uint8 = 2
)

// New builds a generator of the given kind in its zero state. The caller seeds
// it.
func New(kind uint8) (Source, error) {
	switch kind {
	case KindPCG:
		return &PCG{}, nil
	case KindFrotz:
		return &Frotz{}, nil
	default:
		return nil, fmt.Errorf("prng: unknown random generator kind %d: %w", kind, ErrInvalidState)
	}
}

// entropy fills b from the host's random source.
//
// crypto/rand reads that source directly. This package never calls the
// package-global generators of math/rand or math/rand/v2, so no process-global
// state is touched or shared between machines.
func entropy(b []byte) error {
	if _, err := crand.Read(b); err != nil {
		return fmt.Errorf("prng: seeding the random number generator: %w", err)
	}
	return nil
}
