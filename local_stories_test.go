package zmachine

import (
	"context"
	"errors"
	"os"
	"testing"
)

// Tests over the story files in testdata/local, which are a second edition of
// the three games in testdata/stories rather than another copy of them.
//
// They are commercial files with no redistribution licence, so they are not in
// the repository and are fetched by testdata/local/fetch.sh. Every test here
// skips itself when they are absent: a developer who has not run the script,
// and any environment that cannot reach the Internet Archive, must still see a
// passing go test ./....

const (
	localZork1 = "testdata/local/zork1-r88-840726.z3"
	localZork2 = "testdata/local/zork2-r48-840904.z3"
	localZork3 = "testdata/local/zork3-r17-840727.z3"
)

// requireLocalEditions skips the calling test unless every local story file is
// present.
//
// It is called before the subtests rather than inside them on purpose. A parent
// whose subtests all skip is reported as PASS, so skipping per-case would make
// an absent fixture look like a test that ran and succeeded - the same way a
// missing dfrotz once did, which is why ZMACHINE_REQUIRE_DFROTZ exists. Skipping
// the parent makes the absence visible in the output instead.
func requireLocalEditions(t *testing.T) {
	t.Helper()
	for _, path := range []string{localZork1, localZork2, localZork3} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("local story fixtures unavailable; run testdata/local/fetch.sh: %v", err)
		}
	}
}

// TestLocalEditionsAreVersion3 pins what the Lost Treasures disc actually
// contains.
//
// The first editions of Zork I and Zork II were compiled as Z-machine Version 1
// and Version 2, which LoadStory refuses by design; Appendix F of Standard 1.1
// catalogues which releases those were. This edition shipped the 1984
// re-releases instead, and that is the reason the engine can run the files the
// client owns at all. If a disc image ever produced something else, the
// compatibility policy would need revisiting, so the fact is asserted rather
// than assumed.
func TestLocalEditionsAreVersion3(t *testing.T) {
	requireLocalEditions(t)

	for _, tc := range []struct {
		path     string
		release  uint16
		serial   string
		checksum uint16
	}{
		{localZork1, 88, "840726", 0xa129},
		{localZork2, 48, "840904", 0xd899},
		{localZork3, 17, "840727", 0x2e7a},
	} {
		t.Run(tc.path, func(t *testing.T) {
			story := loadStoryFile(t, tc.path)
			if got := story.Version(); got != 3 {
				t.Errorf("Version() = %d, want 3", got)
			}
			if got := story.Release(); got != tc.release {
				t.Errorf("Release() = %d, want %d", got, tc.release)
			}
			if got := story.Serial(); got != tc.serial {
				t.Errorf("Serial() = %q, want %q", got, tc.serial)
			}
			if got := story.Checksum(); got != tc.checksum {
				t.Errorf("Checksum() = 0x%04x, want 0x%04x", got, tc.checksum)
			}
		})
	}
}

// TestLocalEditionsRunAcrossARequestBoundary checks that a second edition is
// playable the way the first is, rather than merely loadable.
//
// The files are padded to the disc's block size, well beyond the length their
// headers declare, so this also covers LoadStory reading the declared length
// and ignoring the trailing bytes.
func TestLocalEditionsRunAcrossARequestBoundary(t *testing.T) {
	requireLocalEditions(t)

	for _, path := range []string{localZork1, localZork2, localZork3} {
		t.Run(path, func(t *testing.T) {
			story := loadStoryFile(t, path)

			first, err := New(story, WithRandomSeed(1234))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			started, err := first.Start(context.Background())
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if started.Output == "" {
				t.Error("Start() produced no output; a story prints its banner first")
			}
			if started.State == nil {
				t.Fatal("Start() returned no state at an input boundary")
			}

			// The machine is discarded between turns, which is the contract the
			// whole package exists to keep (spec S 23).
			second, err := New(story, WithRandomSeed(1234))
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := second.Restore(started.State); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if _, err := second.Run(context.Background(), "look"); err != nil {
				t.Fatalf("Run() error = %v", err)
			}
		})
	}
}

// TestStateDoesNotCrossEditions covers the rule the host-facing compatibility
// policy rests on: a saved state belongs to one story file, not to one game.
//
// Quetzal identifies the story by release number, serial code and checksum in
// its IFhd chunk, and Restore refuses a state whose identity does not match the
// story the machine was built from (spec S 9). Zork I release 119 and release
// 88 are both Version 3 and both play, which is exactly why the rule matters:
// nothing about the two files makes the mismatch obvious to a host that tracks
// sessions by game rather than by file.
func TestStateDoesNotCrossEditions(t *testing.T) {
	requireLocalEditions(t)

	other := loadStoryFile(t, localZork1)  // release 88
	bundled := loadStoryFile(t, zork1Path) // release 119

	source, err := New(bundled, WithRandomSeed(1234))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := source.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	target, err := New(other, WithRandomSeed(1234))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	err = target.Restore(result.State)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore(state from release %d into release %d) error = %v, want one wrapping ErrInvalidState",
			bundled.Release(), other.Release(), err)
	}
}
