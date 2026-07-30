package zmachine

import (
	"errors"
	"testing"
)

// Tests for the machine's random generators (S 2.4).
//
// The Frotz-compatible generator is tested against values worked out from the
// C in Frotz's src/common/random.c rather than from this package's own code,
// so that a failure here means this generator is wrong rather than merely
// different from what it used to be. The differential tests then check the
// whole engine against dfrotz; these say which of the two is at fault when
// that comparison breaks.

// TestFrotzRandomMatchesReferenceSequence pins the generating half of Frotz's
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
func TestFrotzRandomMatchesReferenceSequence(t *testing.T) {
	reference := []uint16{12061, 12517, 15131, 29025, 29131}

	t.Run("raw results", func(t *testing.T) {
		f := &frotzRandom{}
		f.seedWith(20000)
		// Drawing over a range larger than any result recovers the result
		// itself, because result % range + 1 is result + 1 when range exceeds
		// result.
		for i, want := range reference {
			if got := f.draw(0x7fff + 1); got != want+1 {
				t.Errorf("draw %d = %d, want %d", i, got-1, want)
			}
		}
	})

	t.Run("scaled to a range", func(t *testing.T) {
		f := &frotzRandom{}
		f.seedWith(20000)
		for i, result := range reference {
			want := result%6 + 1
			if got := f.draw(6); got != want {
				t.Errorf("draw(6) %d = %d, want %d", i, got, want)
			}
		}
	})
}

// TestFrotzRandomSeeding covers Frotz's seed_random, whose three-way split is
// the reason a differential run has to choose its seed with care.
func TestFrotzRandomSeeding(t *testing.T) {
	t.Run("a seed below 1000 counts instead of generating", func(t *testing.T) {
		// Frotz reads the counter and then advances it, wrapping when it
		// reaches the interval, so the results cycle 0, 1, 2 and the drawn
		// values are those plus one.
		f := &frotzRandom{}
		f.seedWith(3)
		want := []uint16{1, 2, 3, 1, 2, 3, 1}
		for i, w := range want {
			if got := f.draw(6); got != w {
				t.Errorf("draw %d = %d, want %d", i, got, w)
			}
		}
	})

	t.Run("a seed of 1000 or more starts the generator", func(t *testing.T) {
		f := &frotzRandom{}
		f.seedWith(1000)
		if f.interval != 0 {
			t.Errorf("interval = %d, want 0: the generator should be running", f.interval)
		}
		if f.a != 1000 {
			t.Errorf("A = %d, want 1000", f.a)
		}
	})

	t.Run("the same seed repeats the sequence", func(t *testing.T) {
		first, second := &frotzRandom{}, &frotzRandom{}
		first.seedWith(4242)
		second.seedWith(4242)
		for i := 0; i < 50; i++ {
			if a, b := first.draw(1000), second.draw(1000); a != b {
				t.Fatalf("draw %d = %d and %d; the same seed must repeat (S 2.4.2)", i, a, b)
			}
		}
	})
}

// TestRandomSourcesStayInRange covers S 2.4.1 for both generators: a draw is
// between 1 and n inclusive.
func TestRandomSourcesStayInRange(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source randomSource
	}{
		{"pcg", &pcgRandom{}},
		{"frotz", &frotzRandom{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.source.seedWith(12345)
			for _, n := range []uint16{1, 2, 6, 100, 1000, 0x7fff} {
				for i := 0; i < 500; i++ {
					got := tc.source.draw(n)
					if got < 1 || got > n {
						t.Fatalf("draw(%d) = %d, want 1 to %d", n, got, n)
					}
				}
			}
		})
	}
}

// TestRandomStateRoundTrips covers the halves of spec S 23 each generator is
// responsible for: a generator that crossed a request boundary carries on with
// the sequence it was in the middle of.
func TestRandomStateRoundTrips(t *testing.T) {
	for _, tc := range []struct {
		name string
		make func() randomSource
	}{
		{"pcg", func() randomSource { return &pcgRandom{} }},
		{"frotz", func() randomSource { return &frotzRandom{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := tc.make()
			source.seedWith(999999)
			for i := 0; i < 17; i++ {
				source.draw(1000)
			}

			state, err := source.marshalState()
			if err != nil {
				t.Fatalf("marshalState() error = %v", err)
			}
			want := make([]uint16, 10)
			for i := range want {
				want[i] = source.draw(1000)
			}

			restored := tc.make()
			if err := restored.unmarshalState(state); err != nil {
				t.Fatalf("unmarshalState() error = %v", err)
			}
			for i := range want {
				if got := restored.draw(1000); got != want[i] {
					t.Fatalf("draw %d after restoring = %d, want %d", i, got, want[i])
				}
			}
		})
	}
}

// TestRandomStateRejectsGarbage covers spec S 26: a saved state is untrusted,
// so a generator state that means nothing is refused rather than accepted as
// some other sequence.
func TestRandomStateRejectsGarbage(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source randomSource
		state  []byte
	}{
		{"pcg", &pcgRandom{}, []byte("not a pcg state")},
		{"frotz too short", &frotzRandom{}, []byte{1, 2, 3}},
		{"frotz too long", &frotzRandom{}, make([]byte, frotzStateSize+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.source.unmarshalState(tc.state); !errors.Is(err, ErrInvalidState) {
				t.Errorf("unmarshalState(garbage) error = %v, want one wrapping ErrInvalidState", err)
			}
		})
	}
}

// TestNewRandomSourceRejectsUnknownKind covers a saved state naming a generator
// this engine does not have.
func TestNewRandomSourceRejectsUnknownKind(t *testing.T) {
	if _, err := newRandomSource(0); !errors.Is(err, ErrInvalidState) {
		t.Errorf("newRandomSource(0) error = %v, want one wrapping ErrInvalidState", err)
	}
	if _, err := newRandomSource(99); !errors.Is(err, ErrInvalidState) {
		t.Errorf("newRandomSource(99) error = %v, want one wrapping ErrInvalidState", err)
	}
}

// TestWithFrotzRandomSeedSelectsTheFrotzGenerator checks the option reaches the
// machine, and that the machine's own draws are the reference sequence.
func TestWithFrotzRandomSeedSelectsTheFrotzGenerator(t *testing.T) {
	story := newTestStory(t)
	m, err := New(story, WithFrotzRandomSeed(20000))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if m.random.kind() != randomKindFrotz {
		t.Fatalf("generator kind = %d, want %d", m.random.kind(), randomKindFrotz)
	}
	// The same five reference results, scaled to a range of six.
	for i, result := range []uint16{12061, 12517, 15131, 29025, 29131} {
		if got := m.randomInRange(6); got != result%6+1 {
			t.Errorf("randomInRange(6) %d = %d, want %d", i, got, result%6+1)
		}
	}
}
