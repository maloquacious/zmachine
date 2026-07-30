package zmachine

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// A long scripted Zork I route, replayed under several random seeds (spec S 23,
// S 31, S 32).
//
// TestZork1AcrossRequestBoundaries in state_test.go proves the engine agrees
// with a conventional interpreter about what a player sees, turn by turn, under
// one seed. It cannot say much about seeds it does not use, and 46 turns leave
// the story shortly after the Troll Room.
//
// This route goes considerably further - the attic, the dome, the rope, the
// Torch Room, the Temple, the Egyptian Room and the Altar - and runs under
// several seeds. The seeds genuinely diverge: the troll fight takes a different
// number of blows under each, and the thief steals the coffin under some and
// not others. That divergence is the point. Whatever the random sequence does,
// destroying and recreating the Machine at every input boundary must not change
// it, which is what spec S 23 promises and what this file checks.

// fightTroll is a sentinel standing in for however many blows the troll takes
// under the seed in play. The troll blocks every exit from its room (S 32 asks
// for combat, and this is where Zork I does it), so the route cannot continue
// until it is dead, and the number of turns that takes is exactly the sort of
// thing the random sequence decides.
const fightTroll = "\x00fight troll"

// fightTrollLimit bounds the fight so that a seed which somehow never resolves
// it fails the test rather than running forever.
const fightTrollLimit = 25

// zork1LongRoute is the route itself.
//
// The turns before the trap door closes are the same under every seed, because
// nothing above ground consults the random number generator, so those turns
// name text and rooms. From the Troll Room on, the thief roams and the fight
// runs long or short, so later turns asserted here name only what no seed can
// change: the rooms the route walks through, which depend on the map rather
// than on chance.
var zork1LongRoute = []routeTurn{
	// The mailbox, the leaflet and the way in through the window.
	{command: "open mailbox", contains: []string{"Opening the small mailbox reveals a leaflet."}, room: "West of House"},
	{command: "take leaflet", contains: []string{"Taken."}, room: "West of House"},
	{command: "read leaflet", contains: []string{"WELCOME TO ZORK!"}, room: "West of House"},
	{command: "drop leaflet", contains: []string{"Dropped."}, room: "West of House"},
	{command: "north", contains: []string{"North of House"}, room: "North of House"},
	{command: "east", contains: []string{"Behind House"}, room: "Behind House"},
	{command: "open window", contains: []string{"With great effort, you open the window"}, room: "Behind House"},
	{command: "west", contains: []string{"Kitchen"}, room: "Kitchen", score: 10},

	// The living room: the lamp, the sword, the trophy case and the rug.
	{command: "west", contains: []string{"Living Room", "trophy case"}, room: "Living Room", score: 10},
	{command: "take lamp", contains: []string{"Taken."}, room: "Living Room", score: 10},
	{command: "take sword", contains: []string{"Taken."}, room: "Living Room", score: 10},
	{command: "open trophy case", contains: []string{"Opened."}, room: "Living Room", score: 10},
	{command: "move rug", contains: []string{"the rug is moved to one side", "closed trap door"}, room: "Living Room", score: 10},
	{command: "open trap door", contains: []string{"rickety staircase descending into darkness"}, room: "Living Room", score: 10},

	// The attic is dark, so the lamp has to be lit to go up: light is a
	// property of the room and of what the player carries, not of the map.
	{command: "east", contains: []string{"Kitchen"}, room: "Kitchen", score: 10},
	{command: "turn on lamp", contains: []string{"The brass lantern is now on."}, room: "Kitchen", score: 10},
	{command: "up", contains: []string{"Attic", "A large coil of rope", "nasty-looking knife"}, room: "Attic", score: 10},
	{command: "take rope", contains: []string{"Taken."}, room: "Attic", score: 10},
	{command: "take knife", contains: []string{"Taken."}, room: "Attic", score: 10},
	{command: "down", contains: []string{"Kitchen"}, room: "Kitchen", score: 10},
	{command: "west", contains: []string{"Living Room"}, room: "Living Room", score: 10},

	// Underground. The trap door bars itself behind the player, and from here
	// on the thief is abroad, so nothing below names text that chance can
	// touch.
	{command: "down", contains: []string{"The trap door crashes shut", "Cellar"}, room: "Cellar", score: 35},
	{command: "north", contains: []string{"The Troll Room"}, room: "The Troll Room", score: 35},
	{command: fightTroll},

	// Everything from here on asserts nothing about what the story prints.
	//
	// Once the fight has happened the seed has had a lasting effect: the player
	// may be wounded, which lowers how much can be carried, and the thief may
	// have taken any of the items being carried. Both change what these turns
	// print, and neither is a defect. Pinning text here would be asserting that
	// one seed's luck is the only correct outcome.
	//
	// What still has to hold is the whole point of the file: whatever this
	// stretch does, it must do it identically whether the Machine survives
	// between turns or is rebuilt for each one.

	// A step into the maze and back out, which the shorter script never
	// reaches.
	{command: "west"},
	{command: "east"},

	// East to the dome, and down the rope into the Torch Room.
	{command: "east"},
	{command: "east"},
	{command: "southeast"},
	{command: "east"},
	{command: "tie rope to railing"},
	{command: "down"},
	{command: "take torch"},

	// The torch is a light source in its own right, so the lamp can go out
	// without the room going dark.
	{command: "turn off lamp"},
	{command: "down"},

	// The Temple, the Egyptian Room and the coffin. Whether the coffin can be
	// lifted depends on what the player is still carrying and on whether the
	// troll drew blood, so this may or may not succeed.
	{command: "east"},
	{command: "take coffin"},
	{command: "drop knife"},
	{command: "drop sword"},
	{command: "take coffin"},
	{command: "west"},
	{command: "south"},
	{command: "look"},
	{command: "inventory"},
	{command: "score"},

	// Praying at the altar moves the player to the forest.
	{command: "pray"},
	{command: "diagnose"},

	// Opcodes the route would otherwise never reach: verify computes the story
	// checksum, and the in-story save and restore report failure because this
	// engine leaves persistence to the host (spec S 24). Those two are worth
	// asserting, because no seed can change them.
	{command: "verify"},
	{command: "save", contains: []string{"Failed."}},
	{command: "restore", contains: []string{"Failed."}},
	{command: "wait"},
	{command: "score"},
	{command: "look"},
}

// routeTurn is one turn of the route. Empty fields assert nothing, which is how
// turns whose outcome the random sequence may touch are written.
type routeTurn struct {
	command string
	// contains is text the turn must produce under every seed.
	contains []string
	// room is the object name the status line must report, when the seed
	// cannot affect where the player is standing.
	room string
	// score is the score the status line must report; it is only meaningful
	// before the thief is abroad, so later turns leave it zero and are not
	// checked.
	score int16
}

// longRouteSeeds are the seeds the route runs under. They are fixed so that a
// failure can be reproduced exactly; no test here draws on production entropy
// (CLAUDE.md, spec S 21).
var longRouteSeeds = []uint64{1, 42, 1987, 20240, 31337}

// longRouteReplaySeed is deliberately not one of longRouteSeeds.
//
// Every Machine created during the replay is seeded with it, so if the replay
// still produces the original random sequence, that sequence can only have come
// out of the restored state rather than out of the Machine's own seed. Without
// this the comparison would pass even if the state carried no generator state
// at all.
const longRouteReplaySeed uint64 = 777000777

// TestZork1LongRouteAcrossSeeds walks the long route under several seeds, once
// on a Machine kept alive for the whole game and once on a fresh Machine per
// turn, and requires the two to agree exactly.
//
// This is spec S 23's promise stated as a test: the host may destroy the
// Machine at any input boundary and carry on later from the bytes it was
// handed, with behaviour indistinguishable from never having stopped.
func TestZork1LongRouteAcrossSeeds(t *testing.T) {
	story := loadZork1(t)

	for _, seed := range longRouteSeeds {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			commands, continuous := playLongRouteContinuous(t, story, seed)
			if len(commands) < len(zork1LongRoute) {
				t.Fatalf("route issued %d commands, want at least %d", len(commands), len(zork1LongRoute))
			}

			perRequest := replayLongRoutePerRequest(t, story, seed, commands)

			// One extra result on each side for the opening, which no command
			// produced.
			if len(continuous) != len(perRequest) {
				t.Fatalf("continuous produced %d results, per-request %d", len(continuous), len(perRequest))
			}

			for i := range continuous {
				what := "the opening"
				if i > 0 {
					what = fmt.Sprintf("turn %d (%q)", i, commands[i-1])
				}
				if continuous[i].Output != perRequest[i].Output {
					t.Fatalf("%s diverged.\n--- retained Machine ---\n%s\n--- Machine per turn ---\n%s",
						what, continuous[i].Output, perRequest[i].Output)
				}
				if continuous[i].Status != perRequest[i].Status {
					t.Errorf("%s Status = %v retained, %v per turn", what, continuous[i].Status, perRequest[i].Status)
				}
				if continuous[i].StatusLine != perRequest[i].StatusLine {
					t.Errorf("%s StatusLine = %+v retained, %+v per turn",
						what, continuous[i].StatusLine, perRequest[i].StatusLine)
				}
			}

			// Praying at the altar ends the route in the forest, and so does
			// dying, so the destination holds however the seed treated the
			// player on the way.
			//
			// The score is only checked against a floor. Reaching the cellar
			// and the passage east of the troll is worth 40 points that cannot
			// be lost, while everything above that depends on whether the
			// coffin could be lifted and whether the thief kept it.
			last := continuous[len(continuous)-1]
			if last.StatusLine.Name != "Forest" {
				t.Errorf("final room = %q, want %q", last.StatusLine.Name, "Forest")
			}
			if last.StatusLine.Score < 40 {
				t.Errorf("final score = %d, want at least 40", last.StatusLine.Score)
			}
		})
	}
}

// playLongRouteContinuous plays the route on one Machine, returning the exact
// commands it issued and every result it produced, the opening first.
//
// The commands are returned rather than taken from zork1LongRoute because the
// troll fight is as long as the seed makes it, and the replay has to issue the
// identical sequence for the comparison to mean anything.
func playLongRouteContinuous(t *testing.T, story *Story, seed uint64) ([]string, []Result) {
	t.Helper()

	m, err := New(story, WithRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	ctx := context.Background()

	opening, err := m.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	requireResumable(t, opening, "the opening")

	commands := make([]string, 0, len(zork1LongRoute)+fightTrollLimit)
	results := []Result{opening}

	for _, turn := range zork1LongRoute {
		if turn.command == fightTroll {
			issued, fought := fightTheTroll(t, m, ctx)
			commands = append(commands, issued...)
			results = append(results, fought...)
			continue
		}

		result, err := m.Run(ctx, turn.command)
		if err != nil {
			t.Fatalf("Run(%q) error = %v, want nil", turn.command, err)
		}
		requireResumable(t, result, fmt.Sprintf("turn %q", turn.command))

		for _, want := range turn.contains {
			if !strings.Contains(result.Output, want) {
				t.Errorf("Run(%q) output = %q, want it to contain %q", turn.command, result.Output, want)
			}
		}
		if turn.room != "" && result.StatusLine.Name != turn.room {
			t.Errorf("after %q the status line reports room %q, want %q",
				turn.command, result.StatusLine.Name, turn.room)
		}
		if turn.score != 0 && result.StatusLine.Score != turn.score {
			t.Errorf("after %q the status line reports score %d, want %d",
				turn.command, result.StatusLine.Score, turn.score)
		}

		commands = append(commands, turn.command)
		results = append(results, result)
	}

	return commands, results
}

// fightTheTroll strikes until the troll is gone, and reports the commands it
// issued along with their results.
func fightTheTroll(t *testing.T, m *Machine, ctx context.Context) ([]string, []Result) {
	t.Helper()

	const blow = "kill troll with sword"
	var commands []string
	var results []Result

	for i := 0; i < fightTrollLimit; i++ {
		result, err := m.Run(ctx, blow)
		if err != nil {
			t.Fatalf("Run(%q) error = %v, want nil", blow, err)
		}
		requireResumable(t, result, "a blow struck at the troll")
		commands = append(commands, blow)
		results = append(results, result)

		// The troll's body vanishes when it dies, so the room stops reporting
		// one. Striking at a troll that is no longer there is how the story
		// says the fight is over.
		if strings.Contains(result.Output, "You can't see any troll here!") {
			return commands, results
		}
	}

	t.Fatalf("the troll survived %d blows, which no seed should allow", fightTrollLimit)
	return nil, nil
}

// replayLongRoutePerRequest issues the same commands again, but on a Machine
// that is created for one turn and dropped, exactly as a server handling one
// request at a time would.
func replayLongRoutePerRequest(t *testing.T, story *Story, seed uint64, commands []string) []Result {
	t.Helper()
	ctx := context.Background()

	// Only the opening needs the original seed, because it is the turn that
	// establishes the random sequence. Every Machine after it is seeded
	// differently on purpose; see longRouteReplaySeed.
	starter, err := New(story, WithRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	opening, err := starter.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	requireResumable(t, opening, "the opening")

	results := []Result{opening}
	state := opening.State

	for _, command := range commands {
		result := runTurn(t, story, state, command, WithRandomSeed(longRouteReplaySeed))
		requireResumable(t, result, fmt.Sprintf("turn %q", command))
		results = append(results, result)
		state = result.State
	}

	return results
}

// requireResumable checks the two things every turn of a request-oriented
// session must be true of: the story stopped somewhere the host can resume
// from, and it handed back the bytes to resume with (spec S 8, S 23).
func requireResumable(t *testing.T, result Result, what string) {
	t.Helper()
	if result.Status != WaitingForInput {
		t.Fatalf("%s: Status = %v, want %v", what, result.Status, WaitingForInput)
	}
	if len(result.State) == 0 {
		t.Fatalf("%s: Result carries no State, so the session cannot be resumed", what)
	}
}
