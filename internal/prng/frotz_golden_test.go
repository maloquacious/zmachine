package prng

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

// frotzSeed1234Digest is the SHA-256 of the first 1,000 results Frotz's
// generator produces from a seed of 1234, each written in base 10 and followed
// by a newline.
//
// The constant does not come from this package. It was taken from the
// executable: Homebrew's Frotz 2.55 (archive SHA-256
// e3d6912ff94bca724759fe59aa222c1ee23aa4d35ffa15a0b40f72f97887d871), run as
// `dfrotz -q -m -s 1234` against a synthetic Version 3 story whose only content
// is `random 32767` and `print_num` a thousand times over. The printed values
// were decremented to undo the opcode's `result % range + 1` scaling and hashed
// one per line. The sister implementation at ../z3 derived it, and its
// docs/prng-history.md records the story image, the commands, and the
// three-way agreement between the executable, the recurrence in
// src/common/random.c evaluated independently, and its own Go code.
//
// Provenance is the whole point of the constant. A digest generated from the
// implementation it checks would only assert that the code still does what it
// did, which is not the property this generator needs to hold.
const frotzSeed1234Digest = "b7b37f6f04fb177b86ef1370097d9def416e893e979f28c52eb9eb7e7c67a88d"

// TestFrotzMatchesDfrotzGoldenSequence pins the sequence itself.
//
// The other tests in this package check the parts of Frotz's z_random -- the
// recurrence over five values, the 1000 boundary in seed_random, predictable
// mode's cycle, the modulo scaling, the state round trip -- and a change that
// altered the sequence while preserving all of them would pass every one. This
// test is what would fail.
//
// It matters because Frotz is not here to be a good generator. It exists so the
// differential tests can compare this engine against dfrotz turn for turn, and
// section 2.4 leaves the numbers entirely to the interpreter: two conforming
// implementations given one seed are expected to disagree. The moment our
// sequence drifts from dfrotz's, every transcript comparison silently stops
// testing what it claims to, and the failures surface somewhere else entirely.
func TestFrotzMatchesDfrotzGoldenSequence(t *testing.T) {
	f := &Frotz{}
	f.SeedWith(1234)

	var values strings.Builder
	for range 1000 {
		// The oracle ran `random 32767`, so recovering what it drew means
		// undoing that opcode's scaling the same way the digest's derivation
		// did. TestFrotzGoldenNormalizationIsLossless is what earns the
		// subtraction.
		fmt.Fprintln(&values, f.Draw(32767)-1)
	}

	got := fmt.Sprintf("%x", sha256.Sum256([]byte(values.String())))
	if got != frotzSeed1234Digest {
		t.Errorf("digest of 1,000 results from seed 1234 = %s, want %s\n"+
			"this generator no longer draws what dfrotz draws; the differential tests are comparing engines that disagree by construction",
			got, frotzSeed1234Digest)
	}
}

// TestFrotzGoldenNormalizationIsLossless checks the assumption the digest rests
// on.
//
// `random 32767` stores result % 32767 + 1, which is result + 1 for every
// result except 32767 itself, where it collides with a result of 0. Subtracting
// one recovers the sequence only while that value stays away, so the golden
// test's normalization is an assumption about this particular sample rather
// than an identity. Drawing over 32768 is lossless for a 15-bit result and can
// say whether the assumption holds.
func TestFrotzGoldenNormalizationIsLossless(t *testing.T) {
	scaled, exact := &Frotz{}, &Frotz{}
	scaled.SeedWith(1234)
	exact.SeedWith(1234)

	for i := range 1000 {
		normalized := scaled.Draw(32767) - 1
		if want := exact.Draw(0x7fff+1) - 1; normalized != want {
			t.Fatalf("result %d normalizes to %d but is %d: a result of 32767 collides with 0 under the golden test's subtraction", i, normalized, want)
		}
	}
}
