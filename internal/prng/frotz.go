package prng

import (
	"encoding/binary"
	"fmt"
)

// Frotz reproduces the generator in Frotz's src/common/random.c, so that this
// engine can be compared against dfrotz turn for turn.
//
// It exists for differential testing and for nothing else. It is not a better
// generator than PCG and should not be used to run a real session; the reason
// to have it is that a text comparison against another interpreter is worthless
// the moment the two draw different numbers, and section 2.4 guarantees they
// will.
//
// Frotz draws like this:
//
//	A = 0x015a4e35L * A + 1;
//	result = (A >> 16) & 0x7fff;
//	store(result % range + 1);
//
// A is a C long, so it wraps at whatever width the host uses, but the result is
// unaffected: the low bits of a linear congruential generator depend only on
// the low bits of its previous state, so bits 16 to 30 of A evolve the same way
// whether A is held in 32 bits, 64 bits, or the 31 kept here.
type Frotz struct {
	// a is Frotz's A, held to 31 bits.
	a uint32

	// interval and counter are Frotz's predictable mode, which its
	// seed_random enters for a seed below 1000. In that mode it counts rather
	// than generating; see SeedWith.
	interval uint16
	counter  uint16
}

// The constants of Frotz's generator.
const (
	frotzMultiplier = 0x015a4e35
	frotzIncrement  = 1
	frotzStateMask  = 0x7fffffff

	// FrotzIntervalSeed is the seed below which Frotz's seed_random counts
	// instead of generating.
	FrotzIntervalSeed = 1000

	// FrotzStateSize is the marshalled size of a Frotz: A, then the interval
	// and counter of its predictable mode.
	FrotzStateSize = 8
)

// Kind implements Source.
func (f *Frotz) Kind() uint8 { return KindFrotz }

// SeedWith reproduces Frotz's seed_random.
//
// Frotz splits the seed three ways, and the split is why a differential test
// must choose its seeds with care:
//
//	value == 0     take a seed from the host's entropy
//	value < 1000   count from 0 to value-1 forever, generating nothing
//	otherwise      begin the generator at A = value
//
// So dfrotz -s 42 does not run the generator at all, while dfrotz -s 1042
// does. A comparison against dfrotz wants a seed of at least 1000.
func (f *Frotz) SeedWith(seed uint64) {
	switch {
	case seed == 0:
		// Frotz asks the interface for a seed here. Failing to get entropy is
		// not something seed_random can report, and neither can this; a
		// generator that cannot be seeded unpredictably falls back to Frotz's
		// own starting state.
		if err := f.Reseed(); err != nil {
			f.a = 1
			f.interval = 0
		}
	case seed < FrotzIntervalSeed:
		f.counter = 0
		f.interval = uint16(seed)
	default:
		f.a = uint32(seed) & frotzStateMask
		f.interval = 0
	}
}

// Reseed implements Source.
func (f *Frotz) Reseed() error {
	var b [4]byte
	if err := entropy(b[:]); err != nil {
		return err
	}
	f.a = binary.BigEndian.Uint32(b[:]) & frotzStateMask
	f.interval = 0
	f.counter = 0
	return nil
}

// MarshalState implements Source.
func (f *Frotz) MarshalState() ([]byte, error) {
	b := make([]byte, FrotzStateSize)
	binary.BigEndian.PutUint32(b[0:4], f.a)
	binary.BigEndian.PutUint16(b[4:6], f.interval)
	binary.BigEndian.PutUint16(b[6:8], f.counter)
	return b, nil
}

// UnmarshalState implements Source.
func (f *Frotz) UnmarshalState(data []byte) error {
	if len(data) != FrotzStateSize {
		return fmt.Errorf("prng: random generator state is %d bytes, want %d: %w",
			len(data), FrotzStateSize, ErrInvalidState)
	}
	f.a = binary.BigEndian.Uint32(data[0:4]) & frotzStateMask
	f.interval = binary.BigEndian.Uint16(data[4:6])
	f.counter = binary.BigEndian.Uint16(data[6:8])
	return nil
}

// Draw reproduces the generating half of Frotz's z_random.
func (f *Frotz) Draw(n uint16) uint16 {
	var result uint32
	if f.interval != 0 {
		// Frotz reads the counter and then advances it, so the first value it
		// yields is 0 and the cycle is 0 to interval-1.
		result = uint32(f.counter)
		f.counter++
		if f.counter == f.interval {
			f.counter = 0
		}
	} else {
		f.a = (f.a*frotzMultiplier + frotzIncrement) & frotzStateMask
		result = (f.a >> 16) & 0x7fff
	}
	return uint16(result%uint32(n)) + 1
}
