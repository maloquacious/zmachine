package zmachine_test

// The tutorial in docs/tutorial.md, run.
//
// A tutorial promises that a reader who follows it will see what it says they
// will see, and it is the one document whose reader cannot tell a stale example
// from their own mistake. So the tutorial's program is written out here as the
// reader would write it, and the text it prints is compared against the blocks
// the tutorial shows.
//
// The test is deliberately literal. It builds its output with the same prints
// in the same order rather than sharing helpers with the rest of the package,
// because what is under test is the reader's experience of a specific listing,
// not the engine underneath it. When this fails, the fix is usually to the
// tutorial.

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/maloquacious/zmachine"
)

// tutorialStoryPath is the story the tutorial loads. The tutorial names this
// path in full, and a reader who has cloned the repository has this file.
const tutorialStoryPath = "testdata/stories/zork1-r119-880429.z3"

// tutorialStep3Tail is the block docs/tutorial.md shows as "the last few lines"
// after the step 3 program has run.
const tutorialStep3Tail = `>open mailbox
Opening the small mailbox reveals a leaflet.

>
[ West of House   Score: 0   Moves: 1 ]
`

// tutorialStep4Output is the whole of what docs/tutorial.md promises the
// finished program prints, and is shown twice there: once at the top, as the
// goal, and once at the end, as the result.
const tutorialStep4Output = `loaded release 119, serial 880429, 86838 bytes
ZORK I: The Great Underground Empire
Infocom interactive fiction - a fantasy story
Copyright (c) 1981, 1982, 1983, 1984, 1985, 1986 Infocom, Inc. All rights reserved.
ZORK is a registered trademark of Infocom, Inc.
Release 119 / Serial number 880429

West of House
You are standing in an open field west of a white house, with a boarded front door.
There is a small mailbox here.

>open mailbox
Opening the small mailbox reveals a leaflet.

>take leaflet
Taken.

>read leaflet
"WELCOME TO ZORK!

ZORK is a game of adventure, danger, and low cunning. In it you will explore some of the most amazing territory ever seen by mortals. No computer should be without one!"

>
[ West of House   Score: 0   Moves: 3   486 bytes of state ]
`

func TestTutorial(t *testing.T) {
	t.Run("steps 1 to 3", func(t *testing.T) {
		story := tutorialStory(t)
		var out strings.Builder

		fmt.Fprintf(&out, "loaded release %d, serial %s, %d bytes\n",
			story.Release(), story.Serial(), story.Size())

		machine, err := zmachine.New(story)
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}

		opening, err := machine.Start(context.Background())
		if err != nil {
			t.Fatalf("Start() error = %v, want nil", err)
		}

		fmt.Fprint(&out, opening.Output)

		first, err := machine.Run(context.Background(), "open mailbox")
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}

		fmt.Fprintln(&out, "open mailbox")
		fmt.Fprint(&out, first.Output)
		fmt.Fprintf(&out, "\n[ %s   Score: %d   Moves: %d ]\n",
			first.StatusLine.Name, first.StatusLine.Score, first.StatusLine.Turns)

		if got := out.String(); !strings.HasSuffix(got, tutorialStep3Tail) {
			t.Errorf("step 3 output does not end with the block the tutorial shows\ngot:\n%s\nwant suffix:\n%s",
				got, tutorialStep3Tail)
		}
	})

	t.Run("step 4", func(t *testing.T) {
		story := tutorialStory(t)
		var out strings.Builder

		fmt.Fprintf(&out, "loaded release %d, serial %s, %d bytes\n",
			story.Release(), story.Serial(), story.Size())

		machine, err := zmachine.New(story)
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}

		opening, err := machine.Start(context.Background())
		if err != nil {
			t.Fatalf("Start() error = %v, want nil", err)
		}

		fmt.Fprint(&out, opening.Output)

		saved := opening.State

		var result zmachine.Result

		for _, command := range []string{"open mailbox", "take leaflet", "read leaflet"} {
			fmt.Fprintln(&out, command)

			result, err = tutorialPlayOneTurn(story, saved, command)
			if err != nil {
				t.Fatalf("turn %q error = %v, want nil", command, err)
			}

			fmt.Fprint(&out, result.Output)
			saved = result.State
		}

		fmt.Fprintf(&out, "\n[ %s   Score: %d   Moves: %d   %d bytes of state ]\n",
			result.StatusLine.Name, result.StatusLine.Score, result.StatusLine.Turns, len(saved))

		if got := out.String(); got != tutorialStep4Output {
			t.Errorf("finished program printed:\n%s\ntutorial promises:\n%s", got, tutorialStep4Output)
		}
	})
}

// tutorialPlayOneTurn is the tutorial's own playOneTurn, copied as it is
// listed: a new Machine, the state from last time, one command, and the Machine
// thrown away.
func tutorialPlayOneTurn(story *zmachine.Story, saved []byte, command string) (zmachine.Result, error) {
	machine, err := zmachine.New(story)
	if err != nil {
		return zmachine.Result{}, err
	}

	if err := machine.Restore(saved); err != nil {
		return zmachine.Result{}, err
	}

	return machine.Run(context.Background(), command)
}

// tutorialStory loads the story the tutorial uses, or skips. The story files
// are test fixtures rather than dependencies, so their absence is not a
// failure.
func tutorialStory(t *testing.T) *zmachine.Story {
	t.Helper()

	data, err := os.ReadFile(tutorialStoryPath)
	if err != nil {
		t.Skipf("story fixture unavailable: %v", err)
	}

	story, err := zmachine.LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory(%s) error = %v, want nil", tutorialStoryPath, err)
	}

	return story
}
