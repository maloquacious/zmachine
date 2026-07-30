package prng

import (
	"encoding/binary"
	"fmt"
	"math/rand/v2"
)

// PCG is the engine's own generator: the PCG of math/rand/v2. It is what every
// ordinary machine uses.
type PCG struct {
	pcg *rand.PCG
	rng *rand.Rand
}

// Kind implements Source.
func (p *PCG) Kind() uint8 { return KindPCG }

func (p *PCG) use(pcg *rand.PCG) {
	p.pcg = pcg
	p.rng = rand.New(pcg)
}

// SeedWith implements Source.
func (p *PCG) SeedWith(seed uint64) {
	// PCG takes a 128-bit seed. The second word is a fixed constant so that
	// the whole state is a function of the seed alone, which is what section
	// 2.4.2 requires, and PCG's mixing means a very low seed - section 2.4.2
	// mentions 10 - is still a usable starting point.
	p.use(rand.NewPCG(seed, 0x9e3779b97f4a7c15))
}

// Reseed implements Source.
func (p *PCG) Reseed() error {
	var b [16]byte
	if err := entropy(b[:]); err != nil {
		return err
	}
	p.use(rand.NewPCG(binary.BigEndian.Uint64(b[0:8]), binary.BigEndian.Uint64(b[8:16])))
	return nil
}

// MarshalState implements Source.
func (p *PCG) MarshalState() ([]byte, error) {
	state, err := p.pcg.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("prng: reading the random number generator state: %w", err)
	}
	return state, nil
}

// UnmarshalState implements Source.
func (p *PCG) UnmarshalState(data []byte) error {
	pcg := &rand.PCG{}
	if err := pcg.UnmarshalBinary(data); err != nil {
		return fmt.Errorf("prng: restoring the random number generator state: %w: %w", err, ErrInvalidState)
	}
	p.use(pcg)
	return nil
}

// Draw implements Source.
func (p *PCG) Draw(n uint16) uint16 {
	return uint16(p.rng.IntN(int(n))) + 1
}
