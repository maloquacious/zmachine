package zmachine

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
)

// The machine's random number generator (S 2.4).
//
// S 2.4 requires two states rather than one algorithm: a "random" state, whose
// sequence the story cannot predict, and a "predictable" state, which a given
// seed always reproduces. It says nothing about which numbers either state
// produces, so two conforming interpreters given the same seed will disagree.
//
// That freedom is why this is an interface. The engine's own generator is a
// PCG from math/rand/v2, and it is what every ordinary Machine uses. Comparing
// this engine's behaviour against another interpreter's, though, means drawing
// the numbers that interpreter would draw, which no general-purpose generator
// does by accident; see frotzRandom.

// randomSource is a generator a Machine can own. A Machine never shares one,
// and no generator is ever held in a package-level variable.
type randomSource interface {
	// draw returns a value between 1 and n inclusive. n is always positive:
	// S 15 gives the other cases their own meaning, and random handles them
	// before reaching here.
	draw(n uint16) uint16

	// seedWith puts the generator into the predictable state of S 2.4.2. The
	// same seed must always produce the same sequence.
	seedWith(seed uint64)

	// reseed puts the generator into the unpredictable state of S 2.4.
	reseed() error

	// marshalState returns the generator's state, and unmarshalState restores
	// one, so that a generator can cross a request boundary (spec S 23).
	// unmarshalState rejects states it does not recognise with an error
	// wrapping ErrInvalidState, because saved states are untrusted.
	marshalState() ([]byte, error)
	unmarshalState(data []byte) error

	// kind identifies the generator in a saved state, so that restoring picks
	// the generator the state was written by rather than misreading it.
	kind() uint8
}

// The generator kinds recorded in a saved state.
const (
	randomKindPCG   uint8 = 1
	randomKindFrotz uint8 = 2
)

// newRandomSource builds a generator of the given kind in its zero state. The
// caller seeds it.
func newRandomSource(kind uint8) (randomSource, error) {
	switch kind {
	case randomKindPCG:
		return &pcgRandom{}, nil
	case randomKindFrotz:
		return &frotzRandom{}, nil
	default:
		return nil, fmt.Errorf("zmachine: unknown random generator kind %d: %w", kind, ErrInvalidState)
	}
}

// entropy fills b from the host's random source.
//
// crypto/rand reads that source directly. The engine never calls the
// package-global generators of math/rand or math/rand/v2, so no process-global
// state is touched or shared between machines.
func entropy(b []byte) error {
	if _, err := crand.Read(b); err != nil {
		return fmt.Errorf("zmachine: seeding the random number generator: %w", err)
	}
	return nil
}

// pcgRandom is the engine's own generator: the PCG of math/rand/v2.
type pcgRandom struct {
	pcg *rand.PCG
	rng *rand.Rand
}

func (p *pcgRandom) kind() uint8 { return randomKindPCG }

func (p *pcgRandom) use(pcg *rand.PCG) {
	p.pcg = pcg
	p.rng = rand.New(pcg)
}

func (p *pcgRandom) seedWith(seed uint64) {
	// PCG takes a 128-bit seed. The second word is a fixed constant so that
	// the whole state is a function of the seed alone, which is what S 2.4.2
	// requires, and PCG's mixing means a very low seed - S 2.4.2 mentions 10 -
	// is still a usable starting point.
	p.use(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
}

func (p *pcgRandom) reseed() error {
	var b [16]byte
	if err := entropy(b[:]); err != nil {
		return err
	}
	p.use(rand.NewPCG(binary.BigEndian.Uint64(b[0:8]), binary.BigEndian.Uint64(b[8:16])))
	return nil
}

func (p *pcgRandom) marshalState() ([]byte, error) {
	state, err := p.pcg.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("zmachine: reading the random number generator state: %w", err)
	}
	return state, nil
}

func (p *pcgRandom) unmarshalState(data []byte) error {
	pcg := &rand.PCG{}
	if err := pcg.UnmarshalBinary(data); err != nil {
		return fmt.Errorf("zmachine: restoring the random number generator state: %w: %w", err, ErrInvalidState)
	}
	p.use(pcg)
	return nil
}

func (p *pcgRandom) draw(n uint16) uint16 {
	return uint16(p.rng.IntN(int(n))) + 1
}

// frotzRandom reproduces the generator in Frotz's src/common/random.c, so that
// this engine can be compared against dfrotz turn for turn.
//
// It exists for differential testing (spec S 33) and for nothing else. It is
// not a better generator than the default and should not be used to run a real
// session; the reason to have it is that a text comparison against another
// interpreter is worthless the moment the two draw different numbers, and
// S 2.4 guarantees they will.
//
// Frotz draws like this:
//
//	A = 0x015a4e35L * A + 1;
//	result = (A >> 16) & 0x7fff;
//	store(result % range + 1);
//
// A is a C long, so it wraps at whatever width the host uses, but the result
// is unaffected: the low bits of a linear congruential generator depend only
// on the low bits of its previous state, so bits 16 to 30 of A evolve the same
// way whether A is held in 32 bits, 64 bits, or the 31 kept here.
type frotzRandom struct {
	// a is Frotz's A, held to 31 bits.
	a uint32

	// interval and counter are Frotz's predictable mode, which its
	// seed_random enters for a seed below 1000. In that mode it counts rather
	// than generating; see seedWith.
	interval uint16
	counter  uint16
}

// The constants of Frotz's generator.
const (
	frotzMultiplier = 0x015a4e35
	frotzIncrement  = 1
	frotzStateMask  = 0x7fffffff

	// frotzIntervalSeed is the seed below which Frotz's seed_random counts
	// instead of generating.
	frotzIntervalSeed = 1000
)

func (f *frotzRandom) kind() uint8 { return randomKindFrotz }

// seedWith reproduces Frotz's seed_random.
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
func (f *frotzRandom) seedWith(seed uint64) {
	switch {
	case seed == 0:
		// Frotz asks the interface for a seed here. Failing to get entropy is
		// not something seed_random can report, and neither can this; a
		// generator that cannot be seeded unpredictably falls back to Frotz's
		// own starting state.
		if err := f.reseed(); err != nil {
			f.a = 1
			f.interval = 0
		}
	case seed < frotzIntervalSeed:
		f.counter = 0
		f.interval = uint16(seed)
	default:
		f.a = uint32(seed) & frotzStateMask
		f.interval = 0
	}
}

func (f *frotzRandom) reseed() error {
	var b [4]byte
	if err := entropy(b[:]); err != nil {
		return err
	}
	f.a = binary.BigEndian.Uint32(b[:]) & frotzStateMask
	f.interval = 0
	f.counter = 0
	return nil
}

// frotzStateSize is the marshalled size of a frotzRandom: A, then the interval
// and counter of its predictable mode.
const frotzStateSize = 8

func (f *frotzRandom) marshalState() ([]byte, error) {
	b := make([]byte, frotzStateSize)
	binary.BigEndian.PutUint32(b[0:4], f.a)
	binary.BigEndian.PutUint16(b[4:6], f.interval)
	binary.BigEndian.PutUint16(b[6:8], f.counter)
	return b, nil
}

func (f *frotzRandom) unmarshalState(data []byte) error {
	if len(data) != frotzStateSize {
		return fmt.Errorf("zmachine: random generator state is %d bytes, want %d: %w",
			len(data), frotzStateSize, ErrInvalidState)
	}
	f.a = binary.BigEndian.Uint32(data[0:4]) & frotzStateMask
	f.interval = binary.BigEndian.Uint16(data[4:6])
	f.counter = binary.BigEndian.Uint16(data[6:8])
	return nil
}

// draw reproduces the generating half of Frotz's z_random.
func (f *frotzRandom) draw(n uint16) uint16 {
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
