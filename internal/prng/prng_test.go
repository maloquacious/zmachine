package prng

import (
	"errors"
	"testing"
)

// Tests for the random generators (section 2.4).
//
// The Frotz-compatible generator is tested against values worked out from the
// C in Frotz's src/common/random.c rather than from this package's own code,
// so that a failure here means this generator is wrong rather than merely
// different from what it used to be. The differential tests then check the
// whole engine against dfrotz; these say which of the two is at fault when
// that comparison breaks.

// TestFrotzMatchesReferenceSequence pins the generating half of Frotz's
// z_random.
//
// Frotz computes:
//
//	A = 0x015a4e35L * A + 1;
//	result = (A >> 16) & 0x7fff;
//	store(result % range + 1);
//
// Starting from A = 20000, the first five values of result are 12061, 12517,
// 15131, 29025 and 29131, which were worked out from those three lines rather
// than by running this package.
func TestFrotzMatchesReferenceSequence(t *testing.T) {
	reference := []uint16{12061, 12517, 15131, 29025, 29131}

	t.Run("raw results", func(t *testing.T) {
		f := &Frotz{}
		f.SeedWith(20000)
		// Drawing over a range larger than any result recovers the result
		// itself, because result % range + 1 is result + 1 when range exceeds
		// result.
		for i, want := range reference {
			if got := f.Draw(0x7fff + 1); got != want+1 {
				t.Errorf("draw %d = %d, want %d", i, got-1, want)
			}
		}
	})

	t.Run("scaled to a range", func(t *testing.T) {
		f := &Frotz{}
		f.SeedWith(20000)
		for i, result := range reference {
			want := result%6 + 1
			if got := f.Draw(6); got != want {
				t.Errorf("draw(6) %d = %d, want %d", i, got, want)
			}
		}
	})
}

// TestFrotzSeeding covers Frotz's seed_random, whose three-way split is
// the reason a differential run has to choose its seed with care.
func TestFrotzSeeding(t *testing.T) {
	t.Run("a seed below 1000 counts instead of generating", func(t *testing.T) {
		// Frotz reads the counter and then advances it, wrapping when it
		// reaches the interval, so the results cycle 0, 1, 2 and the drawn
		// values are those plus one.
		f := &Frotz{}
		f.SeedWith(3)
		want := []uint16{1, 2, 3, 1, 2, 3, 1}
		for i, w := range want {
			if got := f.Draw(6); got != w {
				t.Errorf("draw %d = %d, want %d", i, got, w)
			}
		}
	})

	t.Run("a seed of 1000 or more starts the generator", func(t *testing.T) {
		f := &Frotz{}
		f.SeedWith(1000)
		if f.interval != 0 {
			t.Errorf("interval = %d, want 0: the generator should be running", f.interval)
		}
		if f.a != 1000 {
			t.Errorf("A = %d, want 1000", f.a)
		}
	})

	t.Run("the same seed repeats the sequence", func(t *testing.T) {
		first, second := &Frotz{}, &Frotz{}
		first.SeedWith(4242)
		second.SeedWith(4242)
		for i := 0; i < 50; i++ {
			if a, b := first.Draw(1000), second.Draw(1000); a != b {
				t.Fatalf("draw %d = %d and %d; the same seed must repeat (S 2.4.2)", i, a, b)
			}
		}
	})
}

// TestSourcesStayInRange covers S 2.4.1 for both generators: a draw is
// between 1 and n inclusive.
func TestSourcesStayInRange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source Source
	}{
		{"pcg", &PCG{}},
		{"frotz", &Frotz{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.source.SeedWith(12345)
			for _, n := range []uint16{1, 2, 6, 100, 1000, 0x7fff} {
				for i := 0; i < 500; i++ {
					got := tc.source.Draw(n)
					if got < 1 || got > n {
						t.Fatalf("draw(%d) = %d, want 1 to %d", n, got, n)
					}
				}
			}
		})
	}
}

// TestStateRoundTrips covers the halves of spec S 23 each generator is
// responsible for: a generator that crossed a request boundary carries on with
// the sequence it was in the middle of.
func TestStateRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() Source
	}{
		{"pcg", func() Source { return &PCG{} }},
		{"frotz", func() Source { return &Frotz{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.make()
			source.SeedWith(999999)
			for i := 0; i < 17; i++ {
				source.Draw(1000)
			}

			state, err := source.MarshalState()
			if err != nil {
				t.Fatalf("marshalState() error = %v", err)
			}
			want := make([]uint16, 10)
			for i := range want {
				want[i] = source.Draw(1000)
			}

			restored := tc.make()
			if err := restored.UnmarshalState(state); err != nil {
				t.Fatalf("unmarshalState() error = %v", err)
			}
			for i := range want {
				if got := restored.Draw(1000); got != want[i] {
					t.Fatalf("draw %d after restoring = %d, want %d", i, got, want[i])
				}
			}
		})
	}
}

// TestStateRejectsGarbage covers spec S 26: a saved state is untrusted,
// so a generator state that means nothing is refused rather than accepted as
// some other sequence.
func TestStateRejectsGarbage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source Source
		state  []byte
	}{
		{"pcg", &PCG{}, []byte("not a pcg state")},
		{"frotz too short", &Frotz{}, []byte{1, 2, 3}},
		{"frotz too long", &Frotz{}, make([]byte, FrotzStateSize+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.source.UnmarshalState(tc.state); !errors.Is(err, ErrInvalidState) {
				t.Errorf("unmarshalState(garbage) error = %v, want one wrapping ErrInvalidState", err)
			}
		})
	}
}

// TestNewRejectsUnknownKind covers a saved state naming a generator
// this engine does not have.
func TestNewRejectsUnknownKind(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidState) {
		t.Errorf("New(0) error = %v, want one wrapping ErrInvalidState", err)
	}
	if _, err := New(99); !errors.Is(err, ErrInvalidState) {
		t.Errorf("New(99) error = %v, want one wrapping ErrInvalidState", err)
	}
}

// TestReseedProducesAWorkingGenerator covers the unpredictable state of
// section 2.4, which is what a machine starts in when the host supplies no
// seed.
func TestReseedProducesAWorkingGenerator(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source Source
	}{
		{"pcg", &PCG{}},
		{"frotz", &Frotz{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.source.Reseed(); err != nil {
				t.Fatalf("Reseed() error = %v, want nil", err)
			}
			// The sequence is unpredictable by construction, so the only thing
			// worth asserting is that it is a usable one.
			seen := make(map[uint16]bool)
			for i := 0; i < 500; i++ {
				got := tc.source.Draw(100)
				if got < 1 || got > 100 {
					t.Fatalf("Draw(100) = %d, want 1 to 100", got)
				}
				seen[got] = true
			}
			if len(seen) < 20 {
				t.Errorf("500 draws produced only %d distinct values; the generator looks stuck", len(seen))
			}
		})
	}
}

// TestFrotzSeedZeroTakesEntropy covers the first branch of Frotz's
// seed_random, which asks the interface for a seed rather than using the value.
func TestFrotzSeedZeroTakesEntropy(t *testing.T) {
	f := &Frotz{}
	f.SeedWith(0)
	if f.interval != 0 {
		t.Errorf("interval = %d, want 0: a seed of 0 runs the generator", f.interval)
	}
	// Two generators seeded this way should almost never agree; a repeat would
	// mean the seed was not taken from entropy at all.
	g := &Frotz{}
	g.SeedWith(0)
	same := 0
	for i := 0; i < 20; i++ {
		if f.Draw(10000) == g.Draw(10000) {
			same++
		}
	}
	if same == 20 {
		t.Error("two generators seeded from entropy produced the same 20 draws")
	}
}

// TestNewBuildsEachKind checks the constructor against the kinds a saved state
// may name.
func TestNewBuildsEachKind(t *testing.T) {
	for _, tc := range []struct {
		kind uint8
		want uint8
	}{
		{KindPCG, KindPCG},
		{KindFrotz, KindFrotz},
	} {
		source, err := New(tc.kind)
		if err != nil {
			t.Fatalf("New(%d) error = %v, want nil", tc.kind, err)
		}
		if source.Kind() != tc.want {
			t.Errorf("New(%d).Kind() = %d, want %d", tc.kind, source.Kind(), tc.want)
		}
	}
}
