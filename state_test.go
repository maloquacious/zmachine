package zmachine

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/maloquacious/quetzal"
)

// Tests for the Quetzal state adapter and for the request-oriented execution
// model it exists to support (spec S 22, S 23, S 31).
//
// The central architectural promise of this package is that a host may destroy
// a Machine at an input boundary, create another one later, restore the state
// it was handed, and carry on, with behaviour indistinguishable from having
// kept the first Machine alive. Everything below either proves that promise or
// proves that state a host did not get from this package cannot hurt it.

// runTurn is the whole request lifecycle of spec S 9: create a Machine, restore
// the state from the previous turn, run one command, and let the Machine go.
//
// It exists so that no test can accidentally keep a Machine alive between
// turns: the one it creates is unreachable by the time it returns.
func runTurn(t *testing.T, story *Story, state []byte, command string, opts ...Option) Result {
	t.Helper()
	m, err := New(story, opts...)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := m.Restore(state); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}
	result, err := m.Run(context.Background(), command)
	if err != nil {
		t.Fatalf("Run(%q) error = %v, want nil", command, err)
	}
	return result
}

// zork1Script is a scripted Zork I playthrough covering the operations spec
// S 32 asks for: movement, examining, taking and dropping, containers,
// inventory, darkness, combat, random behaviour and scoring.
//
// Each turn names text the story must produce. The expectations are what a
// player of Zork I sees, so a failure here means the engine and a conventional
// interpreter disagree about the story rather than about this test.
var zork1Script = []struct {
	command string
	// contains is text that must appear in the turn's output.
	contains []string
	// score, when non-negative, is the score the status line must report.
	score int
	// room, when non-empty, is the object name the status line must report.
	room string
}{
	// The mailbox and the leaflet: opening a container, taking, reading and
	// dropping.
	{"open mailbox", []string{"Opening the small mailbox reveals a leaflet."}, 0, "West of House"},
	{"take leaflet", []string{"Taken."}, 0, "West of House"},
	{"read leaflet", []string{"WELCOME TO ZORK!"}, 0, "West of House"},
	{"drop leaflet", []string{"Dropped."}, 0, "West of House"},

	// Movement round the house and in through the window.
	{"north", []string{"North of House", "north side of a white house"}, 0, "North of House"},
	{"east", []string{"Behind House", "small window which is slightly ajar"}, 0, "Behind House"},
	{"open window", []string{"With great effort, you open the window"}, 0, "Behind House"},
	{"west", []string{"Kitchen", "A bottle is sitting on the table.", "elongated brown sack"}, 10, "Kitchen"},

	// Containers: the bottle holds water, the sack is shut.
	{"take bottle", []string{"Taken."}, 10, "Kitchen"},
	{"inventory", []string{"You are carrying:", "A glass bottle", "A quantity of water"}, 10, "Kitchen"},
	{"put bottle in sack", []string{"The brown sack isn't open."}, 10, "Kitchen"},
	{"look in sack", []string{"The brown sack is closed."}, 10, "Kitchen"},

	// The living room, the rug and the trap door.
	{"west", []string{"Living Room", "trophy case", "elvish sword of great antiquity"}, 10, "Living Room"},
	{"take lamp", []string{"Taken."}, 10, "Living Room"},
	{"take sword", []string{"Taken."}, 10, "Living Room"},
	{"open trophy case", []string{"Opened."}, 10, "Living Room"},
	{"move rug", []string{"the rug is moved to one side", "closed trap door"}, 10, "Living Room"},
	{"open trap door", []string{"rickety staircase descending into darkness"}, 10, "Living Room"},
	{"score", []string{"Your score is 10 (total of 350 points)", "rank of Beginner"}, 10, "Living Room"},

	// Darkness: the lamp is what makes the cellar visible, and a grue is what
	// waits when it is off.
	{"turn on lamp", []string{"The brass lantern is now on."}, 10, "Living Room"},
	{"down", []string{"The trap door crashes shut", "Cellar", "dark and damp cellar"}, 35, "Cellar"},
	{"look", []string{"Cellar", "narrow passageway leading north"}, 35, "Cellar"},
	{"turn off lamp", []string{"The brass lantern is now off.", "It is now pitch black."}, 35, "Cellar"},
	{"look", []string{"It is pitch black. You are likely to be eaten by a grue."}, 35, "Cellar"},
	{"turn on lamp", []string{"The brass lantern is now on.", "Cellar"}, 35, "Cellar"},

	// Combat, which is where Zork I draws random numbers. The outcomes below
	// belong to the seed the tests use, so they also pin the random sequence
	// across every request boundary the script crosses.
	{"north", []string{"The Troll Room", "nasty-looking troll", "Your sword has begun to glow very brightly."}, 35, "The Troll Room"},
	{"kill troll with sword", []string{"Your sword misses the troll by an inch."}, 35, "The Troll Room"},
	{"kill troll with sword", []string{"A furious exchange, and the troll is knocked out!"}, 35, "The Troll Room"},
	{"kill troll with sword", []string{"The unconscious troll cannot defend himself: He dies.", "the carcass has disappeared"}, 35, "The Troll Room"},
	{"kill troll with sword", []string{"You can't see any troll here!"}, 35, "The Troll Room"},
	{"look", []string{"The Troll Room", "There is a bloody axe here."}, 35, "The Troll Room"},
	{"inventory", []string{"A sword", "A brass lantern (providing light)", "A glass bottle"}, 35, "The Troll Room"},
	{"score", []string{"Your score is 35 (total of 350 points)", "rank of Amateur Adventurer"}, 35, "The Troll Room"},

	// Deeper in, and back again.
	{"east", []string{"East-West Passage", "narrow east-west passageway"}, 40, "East-West Passage"},
	{"east", []string{"Round Room", "circular stone room"}, 40, "Round Room"},
	{"up", []string{"You can't go that way."}, 40, "Round Room"},
	{"look", []string{"Round Room"}, 40, "Round Room"},
	{"down", []string{"You can't go that way."}, 40, "Round Room"},
	{"west", []string{"East-West Passage"}, 40, "East-West Passage"},
	{"west", []string{"The Troll Room", "There is a bloody axe here."}, 40, "The Troll Room"},
	{"south", []string{"Cellar"}, 40, "Cellar"},
	{"up", []string{"The trap door is closed."}, 40, "Cellar"},

	// The thief, who moves on his own and so depends on the random sequence
	// having survived every boundary so far.
	{"put sword in trophy case", []string{"You can't see any trophy case here!", "Someone carrying a large bag", "flips your sword out of your hands"}, 40, "Cellar"},
	{"score", []string{"Your score is 40 (total of 350 points)"}, 40, "Cellar"},
	{"inventory", []string{"A brass lantern (providing light)", "The thief just left"}, 40, "Cellar"},
	{"look", []string{"Cellar", "dark and damp cellar"}, 40, "Cellar"},
}

// zork1Seed is the random seed every Zork I test here uses. The expectations in
// zork1Script - the blows struck in the troll fight and the thief's arrival -
// are the ones this seed produces, so tests never depend on production entropy.
const zork1Seed = 1987

// TestZork1AcrossRequestBoundaries is the central architectural test of this
// package (spec S 23, S 31, S 43).
//
// Zork I is started once, and every turn after that runs on a Machine that did
// not exist a moment earlier and does not exist a moment later: it is created,
// handed the previous turn's state, given one command, and dropped. If any part
// of the "state of play" of S 6.1 - dynamic memory, the program counter, the
// call chain, the evaluation stack - failed to cross a boundary, the story
// would diverge from what a player sees, and the expectations below say what a
// player sees.
func TestZork1AcrossRequestBoundaries(t *testing.T) {
	story := loadZork1(t)

	first, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	opening, err := first.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if !strings.Contains(opening.Output, "ZORK I: The Great Underground Empire") {
		t.Fatalf("opening output = %q, want the Zork I banner", opening.Output)
	}
	if !strings.Contains(opening.Output, "West of House") {
		t.Fatalf("opening output = %q, want the first room", opening.Output)
	}
	if opening.Status != WaitingForInput {
		t.Fatalf("opening Status = %v, want %v", opening.Status, WaitingForInput)
	}
	if len(opening.State) == 0 {
		t.Fatal("opening Result has no State: spec S 23 requires a snapshot at every input boundary")
	}
	state := opening.State
	// The Machine that produced the opening is not used again.
	first = nil
	_ = first

	for i, turn := range zork1Script {
		result := runTurn(t, story, state, turn.command, WithRandomSeed(zork1Seed))

		for _, want := range turn.contains {
			if !strings.Contains(result.Output, want) {
				t.Errorf("turn %d %q: output does not contain %q\ngot: %q",
					i+1, turn.command, want, result.Output)
			}
		}
		if turn.score >= 0 && int(result.StatusLine.Score) != turn.score {
			t.Errorf("turn %d %q: score = %d, want %d", i+1, turn.command, result.StatusLine.Score, turn.score)
		}
		if turn.room != "" && result.StatusLine.Name != turn.room {
			t.Errorf("turn %d %q: status line room = %q, want %q", i+1, turn.command, result.StatusLine.Name, turn.room)
		}
		if result.Status != WaitingForInput {
			t.Fatalf("turn %d %q: Status = %v, want %v", i+1, turn.command, result.Status, WaitingForInput)
		}
		if len(result.State) == 0 {
			t.Fatalf("turn %d %q: Result has no State", i+1, turn.command)
		}
		state = result.State
	}
}

// TestRequestBoundariesMatchContinuousExecution is the property spec S 23
// actually promises: creating, restoring, running and destroying a Machine
// every turn is observably the same as never letting go of the first one.
//
// It is stated as a differential test between the two lifecycles rather than
// against fixed text, so it holds the whole observable result to account -
// story output, the upper window and the status line - without any expectation
// that could drift out of date. Both runs use the same seed, so the random
// sequence is part of what must agree (S 2.4).
func TestRequestBoundariesMatchContinuousExecution(t *testing.T) {
	story := loadZork1(t)
	commands := make([]string, 0, len(zork1Script))
	for _, turn := range zork1Script {
		commands = append(commands, turn.command)
	}

	// One Machine, kept alive for the whole game.
	retained, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	continuous := make([]Result, 0, len(commands)+1)
	opening, err := retained.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	continuous = append(continuous, opening)
	for _, command := range commands {
		result, err := retained.Run(context.Background(), command)
		if err != nil {
			t.Fatalf("continuous Run(%q) error = %v, want nil", command, err)
		}
		continuous = append(continuous, result)
	}

	// A new Machine for every turn, each one restoring what the last returned.
	starter, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	restarted, err := starter.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	perRequest := []Result{restarted}
	state := restarted.State
	for _, command := range commands {
		result := runTurn(t, story, state, command, WithRandomSeed(zork1Seed))
		perRequest = append(perRequest, result)
		state = result.State
	}

	for i := range continuous {
		what := "the opening"
		if i > 0 {
			what = "turn " + commands[i-1]
		}
		if continuous[i].Output != perRequest[i].Output {
			t.Errorf("%s: output differs\ncontinuous:  %q\nper-request: %q",
				what, continuous[i].Output, perRequest[i].Output)
		}
		if continuous[i].UpperWindow != perRequest[i].UpperWindow {
			t.Errorf("%s: upper window differs\ncontinuous:  %q\nper-request: %q",
				what, continuous[i].UpperWindow, perRequest[i].UpperWindow)
		}
		if continuous[i].StatusLine != perRequest[i].StatusLine {
			t.Errorf("%s: status line differs\ncontinuous:  %+v\nper-request: %+v",
				what, continuous[i].StatusLine, perRequest[i].StatusLine)
		}
		if continuous[i].Status != perRequest[i].Status {
			t.Errorf("%s: status = %v continuous, %v per-request",
				what, continuous[i].Status, perRequest[i].Status)
		}
	}
}

// TestRestoredMachinesAreIsolated checks that two players of the same story,
// each restoring their own state, cannot see each other (spec S 28, S 44).
//
// The two games are driven to different places and then run interleaved, one
// turn each, so that if a restore wrote anything shared - dynamic memory
// belonging to the Story rather than to the Machine, say - the second player's
// turn would move the first player's game.
func TestRestoredMachinesAreIsolated(t *testing.T) {
	story := loadZork1(t)

	opening, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	start, err := opening.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	// One player goes north, the other goes south, so their games are in
	// different rooms holding different objects.
	north := runTurn(t, story, start.State, "north", WithRandomSeed(zork1Seed))
	south := runTurn(t, story, start.State, "south", WithRandomSeed(zork1Seed))
	if north.StatusLine.Name != "North of House" {
		t.Fatalf("first player is in %q, want North of House", north.StatusLine.Name)
	}
	if south.StatusLine.Name != "South of House" {
		t.Fatalf("second player is in %q, want South of House", south.StatusLine.Name)
	}

	// Each player now plays a game of their own. What each of them sees when
	// the two games are interleaved must be exactly what they see when their
	// game is the only one being played, so the reference runs come first.
	firstCommands := []string{"north", "east", "open window", "west", "take bottle", "look"}
	secondCommands := []string{"east", "north", "north", "south", "west", "inventory"}

	solo := func(state []byte, commands []string) []Result {
		out := make([]Result, 0, len(commands))
		for _, command := range commands {
			result := runTurn(t, story, state, command, WithRandomSeed(zork1Seed))
			out = append(out, result)
			state = result.State
		}
		return out
	}
	firstAlone := solo(north.State, firstCommands)
	secondAlone := solo(south.State, secondCommands)

	// Now the same two games, one turn each, alternating. If a restore touched
	// anything the Story owns rather than the Machine, one player's turn would
	// show up in the other's.
	firstState, secondState := north.State, south.State
	for i := range firstCommands {
		a := runTurn(t, story, firstState, firstCommands[i], WithRandomSeed(zork1Seed))
		b := runTurn(t, story, secondState, secondCommands[i], WithRandomSeed(zork1Seed))
		firstState, secondState = a.State, b.State

		if a.Output != firstAlone[i].Output || a.StatusLine != firstAlone[i].StatusLine {
			t.Errorf("interleaved turn %d %q: the first player's game changed\n got: %q %+v\nwant: %q %+v",
				i+1, firstCommands[i], a.Output, a.StatusLine, firstAlone[i].Output, firstAlone[i].StatusLine)
		}
		if b.Output != secondAlone[i].Output || b.StatusLine != secondAlone[i].StatusLine {
			t.Errorf("interleaved turn %d %q: the second player's game changed\n got: %q %+v\nwant: %q %+v",
				i+1, secondCommands[i], b.Output, b.StatusLine, secondAlone[i].Output, secondAlone[i].StatusLine)
		}
		if a.StatusLine.Name == b.StatusLine.Name {
			t.Errorf("interleaved turn %d: both players are in %q; the two games have converged",
				i+1, a.StatusLine.Name)
		}
	}
}

// TestSnapshotProgramCounterIsTheReadInstruction records the convention this
// engine saves at, which is documented at the top of state.go.
//
// Quetzal describes IFhd's program counter relative to a save instruction. This
// engine snapshots at the input boundary of spec S 4 instead, and records the
// address of the sread instruction that asked for the line, so that resuming
// re-decodes and re-executes it with the host's input available. The test
// therefore asserts two things at once: that the recorded address is the
// machine's program counter, and that an sread really stands there.
func TestSnapshotProgramCounterIsTheReadInstruction(t *testing.T) {
	story := loadZork1(t)
	m, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	save := decodeState(t, story, result.State)
	if save.Header.PC != m.pc {
		t.Errorf("saved PC = 0x%04x, want the machine's program counter 0x%04x", save.Header.PC, m.pc)
	}
	inst, err := decodeInstruction(m.mem, save.Header.PC)
	if err != nil {
		t.Fatalf("decoding the instruction at the saved PC: %v", err)
	}
	if inst.op != opSRead {
		t.Errorf("instruction at the saved PC is %s, want sread", inst.op)
	}

	// The identity recorded must be the story's own, so that another
	// interpreter can match the save to the same file.
	if save.Header.Release != story.release {
		t.Errorf("saved release = %d, want %d", save.Header.Release, story.release)
	}
	if got := string(save.Header.Serial[:]); got != story.Serial() {
		t.Errorf("saved serial = %q, want %q", got, story.Serial())
	}
}

// TestSnapshotFrameMapping covers the translation of the machine's call chain
// into the frames a Quetzal file records (spec S 22).
//
// The frames are built by hand so that every field that can be got wrong is
// different from every other: two routines with different numbers of locals,
// different argument counts, different result variables, and evaluation-stack
// entries in each of the three frames including the initial environment of
// S 5.5. Quetzal requires that initial environment to be written as the dummy
// frame, and Frame.IsDummy is what says whether it was.
func TestSnapshotFrameMapping(t *testing.T) {
	story := loadZork1(t)
	m, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}

	// S 6.3.1: each frame owns the stack entries pushed since it was called, so
	// the watermarks below give the initial environment two words, the first
	// routine one, and the second three.
	m.stack = []uint16{0x1111, 0x2222, 0x3333, 0x4444, 0x5555, 0x6666}
	m.frames = []frame{
		{returnPC: story.initialPC, stackBase: 0},
		{returnPC: 0x4321, stackBase: 2, numLocals: 2, argCount: 1, store: 0x10, hasStore: true,
			locals: [maxLocalsV3]uint16{0xaaaa, 0xbbbb}},
		{returnPC: 0x8765, stackBase: 3, numLocals: 3, argCount: 3, store: 0x00, hasStore: true,
			locals: [maxLocalsV3]uint16{0x0001, 0x0002, 0x0003}},
	}
	m.pc = 0x5000

	state, err := m.snapshot()
	if err != nil {
		t.Fatalf("snapshot() error = %v, want nil", err)
	}
	save := decodeState(t, story, state)

	if len(save.Frames) != 3 {
		t.Fatalf("saved %d frames, want 3", len(save.Frames))
	}

	// S 5.5: execution begins at an address rather than in a routine, so words
	// can sit on the stack with nothing on the call chain. Quetzal keeps them
	// in the dummy frame, which every non-Version-6 save must begin with.
	dummy := save.Frames[0]
	if !dummy.IsDummy() {
		t.Errorf("frame 0 = %+v, want the dummy frame that holds top-level stack state", dummy)
	}
	if got := dummy.Evaluation; len(got) != 2 || got[0] != 0x1111 || got[1] != 0x2222 {
		t.Errorf("dummy frame evaluation stack = %v, want [4369 8738]", got)
	}

	tests := []struct {
		index      int
		returnPC   uint32
		locals     []uint16
		arguments  uint8
		result     byte
		evaluation []uint16
	}{
		// S 6.4.4.1: the mask 0gfedcba has one bit per supplied argument, so a
		// call with one argument is 0b0000001 and one with three is 0b0000111.
		{1, 0x4321, []uint16{0xaaaa, 0xbbbb}, 0b0000001, 0x10, []uint16{0x3333}},
		{2, 0x8765, []uint16{0x0001, 0x0002, 0x0003}, 0b0000111, 0x00, []uint16{0x4444, 0x5555, 0x6666}},
	}
	for _, tt := range tests {
		got := save.Frames[tt.index]
		if got.ReturnPC != tt.returnPC {
			t.Errorf("frame %d ReturnPC = 0x%04x, want 0x%04x", tt.index, got.ReturnPC, tt.returnPC)
		}
		if got.DiscardResult {
			t.Errorf("frame %d DiscardResult = true, but every Version 3 call stores its result (S 6.4.5)", tt.index)
		}
		if got.ResultVariable != tt.result {
			t.Errorf("frame %d ResultVariable = 0x%02x, want 0x%02x", tt.index, got.ResultVariable, tt.result)
		}
		if got.Arguments != tt.arguments {
			t.Errorf("frame %d Arguments = %#b, want %#b", tt.index, got.Arguments, tt.arguments)
		}
		if !equalWords(got.Locals, tt.locals) {
			t.Errorf("frame %d Locals = %v, want %v", tt.index, got.Locals, tt.locals)
		}
		if !equalWords(got.Evaluation, tt.evaluation) {
			t.Errorf("frame %d Evaluation = %v, want %v", tt.index, got.Evaluation, tt.evaluation)
		}
	}

	// Restoring must give back exactly the machine that was saved.
	restored, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := restored.Restore(state); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}
	if restored.pc != m.pc {
		t.Errorf("restored pc = 0x%04x, want 0x%04x", restored.pc, m.pc)
	}
	if !equalWords(restored.stack, m.stack) {
		t.Errorf("restored stack = %v, want %v", restored.stack, m.stack)
	}
	if len(restored.frames) != len(m.frames) {
		t.Fatalf("restored %d frames, want %d", len(restored.frames), len(m.frames))
	}
	for i := range m.frames {
		want, got := m.frames[i], restored.frames[i]
		if got.returnPC != want.returnPC || got.numLocals != want.numLocals ||
			got.argCount != want.argCount || got.stackBase != want.stackBase ||
			got.store != want.store || got.hasStore != want.hasStore ||
			got.locals != want.locals {
			t.Errorf("restored frame %d = %+v, want %+v", i, got, want)
		}
	}
}

// TestRandomStateSurvivesRestore covers the custom chunk that carries the
// generator state, and the rule of S 2.4 that a story which seeded the
// generator keeps the sequence it asked for.
//
// A snapshot is taken part way through a sequence and the numbers that follow
// it are compared with the numbers a restored machine produces. If the chunk
// were missing or misread, the restored machine would reseed and the two runs
// would diverge immediately.
func TestRandomStateSurvivesRestore(t *testing.T) {
	story := loadZork1(t)

	m, err := New(story, WithRandomSeed(0xfeed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	// Draw a few numbers first, so the snapshot is taken from a generator that
	// is no longer in its initial state; a restore that reseeded from the same
	// host seed would otherwise look correct.
	for i := 0; i < 7; i++ {
		m.randomInRange(1000)
	}

	state, err := m.snapshot()
	if err != nil {
		t.Fatalf("snapshot() error = %v, want nil", err)
	}
	want := make([]uint16, 16)
	for i := range want {
		want[i] = m.randomInRange(1000)
	}

	// The restored machine is given a different host seed on purpose: the saved
	// state must win, or the sequence is not the story's any more.
	restored, err := New(story, WithRandomSeed(0xbeef))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := restored.Restore(state); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}
	got := make([]uint16, len(want))
	for i := range got {
		got[i] = restored.randomInRange(1000)
	}
	if !equalWords(got, want) {
		t.Errorf("random sequence after restore = %v, want %v", got, want)
	}
	if !restored.predictable {
		t.Error("restored generator is not in the predictable state of S 2.4.2, but the saved one was")
	}
}

// TestRestoreWithoutRandomChunkReseeds covers a save written by something that
// does not know about this engine's custom chunk - another interpreter, most
// obviously.
//
// The state must still restore. S 2.4 asks only that the generator be "random"
// when nothing better is known, so the machine falls back to seeding itself as
// a new machine does, which for a host that asked for a seed means that seed.
func TestRestoreWithoutRandomChunkReseeds(t *testing.T) {
	story := loadZork1(t)

	m, err := New(story, WithRandomSeed(0xfeed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	for i := 0; i < 5; i++ {
		m.randomInRange(1000)
	}
	state, err := m.snapshot()
	if err != nil {
		t.Fatalf("snapshot() error = %v, want nil", err)
	}
	stripped := stripChunk(t, story, state, idRandom)

	const seed = 0x1234
	restored, err := New(story, WithRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := restored.Restore(stripped); err != nil {
		t.Fatalf("Restore() of a state without the %s chunk error = %v, want nil", idRandom, err)
	}

	// The generator must be where a freshly seeded machine's is.
	fresh, err := New(story, WithRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	for i := 0; i < 8; i++ {
		if got, want := restored.randomInRange(1000), fresh.randomInRange(1000); got != want {
			t.Fatalf("draw %d after restoring without the random chunk = %d, want the freshly seeded %d", i+1, got, want)
		}
	}
}

// TestScreenStateSurvivesRestore covers the custom chunk carrying the logical
// screen state of S 8.6, which Quetzal does not model.
//
// A story that has split the screen, selected the upper window or turned off
// stream 1 when it asks for a line would otherwise resume with the wrong window
// current, and its next output would land in the wrong half of the Result.
func TestScreenStateSurvivesRestore(t *testing.T) {
	story := loadZork1(t)
	m, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	m.splitWindow(3)
	if err := m.setWindow(windowUpper); err != nil {
		t.Fatalf("setWindow() error = %v, want nil", err)
	}
	// A stream 3 table in dynamic memory, still selected (S 7.1.2.1.1).
	const table = 0x0100
	if err := m.selectMemoryStream(table); err != nil {
		t.Fatalf("selectMemoryStream() error = %v, want nil", err)
	}
	if err := m.printText("abc"); err != nil {
		t.Fatalf("printText() error = %v, want nil", err)
	}

	state, err := m.snapshot()
	if err != nil {
		t.Fatalf("snapshot() error = %v, want nil", err)
	}
	restored, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := restored.Restore(state); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}

	if restored.out.window != windowUpper {
		t.Errorf("restored window = %d, want the upper window %d", restored.out.window, windowUpper)
	}
	if restored.out.upperHeight != 3 {
		t.Errorf("restored upper window height = %d, want 3", restored.out.upperHeight)
	}
	if len(restored.out.tables) != 1 {
		t.Fatalf("restored %d stream 3 tables, want 1", len(restored.out.tables))
	}
	if got := restored.out.tables[0]; got.table != table || got.count != 3 {
		t.Errorf("restored stream 3 table = %+v, want {table:0x%04x count:3}", got, table)
	}

	// A state with no screen chunk restores to the initial screen of S 7.3 and
	// S 8.6.1 rather than being refused.
	stripped := stripChunk(t, story, state, idScreen)
	plain, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := plain.Restore(stripped); err != nil {
		t.Fatalf("Restore() of a state without the %s chunk error = %v, want nil", idScreen, err)
	}
	if plain.out.window != windowLower || !plain.out.stream1 || len(plain.out.tables) != 0 {
		t.Errorf("restored screen = %+v, want the initial state of S 7.3", plain.out)
	}
}

// TestRestoreRejectsMalformedState checks that state a host did not get from
// this package cannot hurt it (spec S 26, S 44).
//
// Saved state arrives from storage, from a request body, or from an attacker;
// it is untrusted input in exactly the way a story file is. Every case here
// must be reported as an error wrapping ErrInvalidState, and none may panic,
// hang, or leave the Machine in a state that runs.
func TestRestoreRejectsMalformedState(t *testing.T) {
	story := loadZork1(t)
	valid := validZork1State(t, story)

	tests := []struct {
		name  string
		state []byte
	}{
		{"empty", nil},
		{"a single byte", []byte{0}},
		{"garbage", []byte("this is not a saved game at all, not even close")},
		{"the story file itself", story.image[:512]},
		{"truncated after the FORM header", valid[:12]},
		{"truncated half way", valid[:len(valid)/2]},
		{"truncated by one byte", valid[:len(valid)-1]},
		{"no IFhd chunk", renameChunk(valid, "IFhd", "Junk")},
		{"no memory chunk", renameChunk(valid, "CMem", "Junk")},
		{"no Stks chunk", renameChunk(valid, "Stks", "Junk")},
		{"a corrupt Stks chunk", corruptChunk(t, valid, "Stks")},
		{"a corrupt IFhd chunk", corruptChunk(t, valid, "IFhd")},
		// A compressed memory chunk relabelled as an uncompressed one, whose
		// payload is then far shorter than the story's dynamic memory. The
		// bytes of a CMem payload are not corrupted directly, because that
		// difference stream has no structure to break: any bytes decode into
		// some dynamic memory, which is why the identity check in IFhd rather
		// than the memory chunk is what protects a restore.
		{"a memory chunk of the wrong length", renameChunk(valid, "CMem", "UMem")},
		{"the wrong form type", replaceBytes(valid, "IFZS", "IFRS")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := New(story, WithRandomSeed(1))
			if err != nil {
				t.Fatalf("New() error = %v, want nil", err)
			}
			before := m.pc
			err = m.Restore(tt.state)
			if err == nil {
				t.Fatalf("Restore() error = nil, want one wrapping ErrInvalidState")
			}
			if !errors.Is(err, ErrInvalidState) {
				t.Errorf("Restore() error = %v, want one wrapping ErrInvalidState", err)
			}
			// A refused restore must leave the machine alone, so that a host
			// can report the failure and try other state.
			if m.pc != before {
				t.Errorf("program counter moved to 0x%04x on a refused restore, want 0x%04x", m.pc, before)
			}
		})
	}
}

// TestRestoreRejectsAnotherStorysState covers the identity check of the
// Quetzal header: a save records the release, serial number and checksum of the
// story it came from, and restoring it into a different story would decode its
// dynamic memory against the wrong image.
func TestRestoreRejectsAnotherStorysState(t *testing.T) {
	zork1 := loadZork1(t)
	other := loadStoryFile(t, "testdata/stories/zork3-r25-860811.z3")
	state := validZork1State(t, zork1)

	m, err := New(other, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	err = m.Restore(state)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Restore() error = %v, want one wrapping ErrInvalidState", err)
	}
	// The underlying reason stays in the chain, so a host can tell a save for
	// the wrong game from a corrupt one and say so to the player.
	if !errors.Is(err, quetzal.ErrStoryMismatch) {
		t.Errorf("Restore() error = %v, want one wrapping quetzal.ErrStoryMismatch", err)
	}
	var mismatch *quetzal.StoryMismatchError
	if !errors.As(err, &mismatch) {
		t.Errorf("Restore() error = %v, want one that is a *quetzal.StoryMismatchError", err)
	}
}

// TestRestoreRefusesStateBeyondMachineLimits checks that the resource limits a
// host set on a Machine also bound what a saved state may ask it to allocate
// (spec S 25, S 26). The limits are what stop a declared length in untrusted
// input from becoming memory.
func TestRestoreRefusesStateBeyondMachineLimits(t *testing.T) {
	story := loadZork1(t)

	build := func(t *testing.T, frames []quetzal.Frame) []byte {
		t.Helper()
		m, err := New(story, WithRandomSeed(1))
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		qs, err := m.quetzalStory()
		if err != nil {
			t.Fatalf("quetzalStory() error = %v, want nil", err)
		}
		save := &quetzal.Save{
			Header: quetzal.Header{
				Release:  story.release,
				Serial:   quetzal.Serial(story.serial),
				Checksum: qs.Checksum,
				PC:       story.initialPC,
			},
			Memory: quetzal.Memory{Encoding: quetzal.MemoryUncompressed, Data: m.mem.dynamic},
			Frames: frames,
		}
		var buf bytes.Buffer
		if err := quetzal.Write(&buf, qs, save); err != nil {
			t.Fatalf("quetzal.Write() error = %v, want nil", err)
		}
		return buf.Bytes()
	}

	t.Run("too many frames", func(t *testing.T) {
		const depth = 64
		frames := []quetzal.Frame{{}}
		for i := 0; i < depth; i++ {
			frames = append(frames, quetzal.Frame{ReturnPC: story.initialPC, Locals: []uint16{1}})
		}
		state := build(t, frames)

		m, err := New(story, WithRandomSeed(1))
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		m.maxDepth = 8
		if err := m.Restore(state); !errors.Is(err, ErrInvalidState) {
			t.Errorf("Restore() error = %v, want one wrapping ErrInvalidState", err)
		}
	})

	t.Run("too much evaluation stack", func(t *testing.T) {
		state := build(t, []quetzal.Frame{{Evaluation: make([]uint16, 4096)}})

		m, err := New(story, WithRandomSeed(1))
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		m.maxStack = 64
		if err := m.Restore(state); !errors.Is(err, ErrInvalidState) {
			t.Errorf("Restore() error = %v, want one wrapping ErrInvalidState", err)
		}
	})

	t.Run("a program counter outside the story", func(t *testing.T) {
		m, err := New(story, WithRandomSeed(1))
		if err != nil {
			t.Fatalf("New() error = %v, want nil", err)
		}
		qs, err := m.quetzalStory()
		if err != nil {
			t.Fatalf("quetzalStory() error = %v, want nil", err)
		}
		save := &quetzal.Save{
			Header: quetzal.Header{
				Release:  story.release,
				Serial:   quetzal.Serial(story.serial),
				Checksum: qs.Checksum,
				// Inside what Quetzal can express (S 1.1.4 caps a Version 3
				// story at 128K) but well past the end of this story.
				PC: quetzal.MaxPC,
			},
			Memory: quetzal.Memory{Encoding: quetzal.MemoryUncompressed, Data: m.mem.dynamic},
			Frames: []quetzal.Frame{{}},
		}
		var buf bytes.Buffer
		if err := quetzal.Write(&buf, qs, save); err != nil {
			t.Fatalf("quetzal.Write() error = %v, want nil", err)
		}
		if err := m.Restore(buf.Bytes()); !errors.Is(err, ErrInvalidState) {
			t.Errorf("Restore() error = %v, want one wrapping ErrInvalidState", err)
		}
	})
}

// TestHaltedResultHasNoState records the decision documented on Result.State: a
// story that ended itself with quit has nothing to resume from, so the Result
// that reports it carries no snapshot and the host keeps the last resumable
// point it was given.
func TestHaltedResultHasNoState(t *testing.T) {
	m := newTestMachine(t, encodeShort(0x0a)...) // quit
	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if result.Status != Halted {
		t.Fatalf("Status = %v, want %v", result.Status, Halted)
	}
	if result.State != nil {
		t.Errorf("State = %d bytes, want nil for a halted story", len(result.State))
	}
}

// TestStartIsRefusedAfterRestore records that a restored machine is one that
// has already run: Start would begin the story again from S 5.5, silently
// throwing away the state the host just restored, so it is refused and Run is
// the way to continue.
func TestStartIsRefusedAfterRestore(t *testing.T) {
	story := loadZork1(t)
	state := validZork1State(t, story)

	m, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := m.Restore(state); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}
	if _, err := m.Start(context.Background()); !errors.Is(err, ErrExecutionFault) {
		t.Errorf("Start() after Restore error = %v, want one wrapping ErrExecutionFault", err)
	}
}

// TestRestoreLeavesInterpreterHeaderFieldsAlone covers S 6.1.3 and S 11.1: the
// header fields an interpreter is responsible for describe the interpreter, not
// the story, so they are set for this machine after a restore rather than
// inherited from whatever wrote the state.
func TestRestoreLeavesInterpreterHeaderFieldsAlone(t *testing.T) {
	story := loadZork1(t)
	m, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	// A machine that claims it cannot draw a status line and has no upper
	// window, as an interpreter without either would (S 8.2, S 8.6.1.2).
	flags1, err := m.mem.readByte(hdrFlags1)
	if err != nil {
		t.Fatalf("readByte() error = %v, want nil", err)
	}
	if err := m.mem.writeByte(hdrFlags1, flags1|flags1NoStatusLine&^flags1SplitAvailable); err != nil {
		t.Fatalf("writeByte() error = %v, want nil", err)
	}
	state, err := m.snapshot()
	if err != nil {
		t.Fatalf("snapshot() error = %v, want nil", err)
	}

	restored, err := New(story, WithRandomSeed(1))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := restored.Restore(state); err != nil {
		t.Fatalf("Restore() error = %v, want nil", err)
	}
	got, err := restored.mem.readByte(hdrFlags1)
	if err != nil {
		t.Fatalf("readByte() error = %v, want nil", err)
	}
	if got&flags1NoStatusLine != 0 {
		t.Error("Flags 1 bit 4 is set after a restore: this engine reports a status line on every Result")
	}
	if got&flags1SplitAvailable == 0 {
		t.Error("Flags 1 bit 5 is clear after a restore: this engine models the upper window")
	}
}

// TestInStorySaveAndRestoreReportFailure records the host policy of spec S 24,
// which the state adapter does not change.
//
// The story-level save and restore instructions belong to the player's own
// save game, which the host owns; this engine has no filesystem and does not
// prompt. Both therefore report failure by not branching, which S 15 makes a
// legal outcome that every story must cope with, and the story prints its own
// message rather than the turn faulting.
func TestInStorySaveAndRestoreReportFailure(t *testing.T) {
	story := loadZork1(t)
	state := validZork1State(t, story)

	for _, command := range []string{"save", "restore"} {
		t.Run(command, func(t *testing.T) {
			result := runTurn(t, story, state, command, WithRandomSeed(zork1Seed))
			// Zork I prints "Failed." when the interpreter reports that the
			// operation did not succeed.
			if !strings.Contains(result.Output, "Failed.") {
				t.Errorf("%q output = %q, want the story's own failure message", command, result.Output)
			}
			if result.Status != WaitingForInput {
				t.Errorf("%q Status = %v, want %v", command, result.Status, WaitingForInput)
			}
			if len(result.State) == 0 {
				t.Errorf("%q produced no State", command)
			}
		})
	}
}

// FuzzRestore asserts that arbitrary bytes offered as saved state never crash
// the host and never provoke an unbounded allocation (spec S 34).
//
// Saved state is the second untrusted binary input this package takes, after
// the story itself, and it is the one a server reads on every request. The
// invariant is the same as for the story: any outcome is acceptable except a
// panic, and every failure must be classified so that a host can report it.
func FuzzRestore(f *testing.F) {
	data, err := os.ReadFile("testdata/stories/zork1-r119-880429.z3")
	if err != nil {
		f.Skipf("story fixture unavailable: %v", err)
	}
	story, err := LoadStory(data)
	if err != nil {
		f.Fatalf("LoadStory() error = %v, want nil", err)
	}

	// A real save is the most valuable seed: mutations of it stay close enough
	// to the format to reach the parts of it that do real work.
	seed, err := New(story, WithRandomSeed(1))
	if err != nil {
		f.Fatalf("New() error = %v, want nil", err)
	}
	opening, err := seed.Start(context.Background())
	if err != nil {
		f.Fatalf("Start() error = %v, want nil", err)
	}
	f.Add(opening.State)
	f.Add([]byte(nil))
	f.Add([]byte("FORM"))
	f.Add([]byte("FORM\x00\x00\x00\x04IFZS"))
	f.Add([]byte("FORM\xff\xff\xff\xffIFZS"))
	f.Add(bytes.Repeat([]byte{0}, 64))

	f.Fuzz(func(t *testing.T, state []byte) {
		m, err := New(story, WithRandomSeed(1))
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		// Bound the machine well below its defaults so that a case which does
		// reach the frame decoder cannot spend the budget on a legal-looking
		// but enormous call chain.
		m.maxStack, m.maxDepth = 1024, 32

		if err := m.Restore(state); err != nil {
			// Every failure must be classified, so that a host can distinguish
			// a bad request from a defect in the engine.
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("unclassified error %v", err)
			}
			return
		}
		// A state that restored must have left a machine that can be described
		// without any of the invariants below being violated.
		if len(m.frames) == 0 {
			t.Fatal("restored a machine with no call frames")
		}
		if len(m.frames) > m.maxDepth {
			t.Fatalf("restored %d frames, past the limit of %d", len(m.frames), m.maxDepth)
		}
		if len(m.stack) > m.maxStack {
			t.Fatalf("restored %d stack entries, past the limit of %d", len(m.stack), m.maxStack)
		}
		if !m.mem.readable(m.pc, 1) {
			t.Fatalf("restored a program counter of 0x%04x, outside the story", m.pc)
		}
		for i, f := range m.frames {
			if f.numLocals > maxLocalsV3 {
				t.Fatalf("restored frame %d with %d locals", i, f.numLocals)
			}
			if f.stackBase < 0 || f.stackBase > len(m.stack) {
				t.Fatalf("restored frame %d with stack base %d of %d", i, f.stackBase, len(m.stack))
			}
		}
	})
}

// Helpers.

// loadStoryFile loads a bundled story fixture, skipping the test when it is not
// present.
func loadStoryFile(t *testing.T, path string) *Story {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("story fixture unavailable: %v", err)
	}
	story, err := LoadStory(data)
	if err != nil {
		t.Fatalf("LoadStory(%s) error = %v, want nil", path, err)
	}
	return story
}

// validZork1State returns the state Zork I reaches at its first input boundary.
func validZork1State(t *testing.T, story *Story) []byte {
	t.Helper()
	m, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if len(result.State) == 0 {
		t.Fatal("Start() produced no State")
	}
	return result.State
}

// decodeState reads a snapshot back through the Quetzal package, so that a test
// inspects what the file says rather than what the adapter believes it wrote.
func decodeState(t *testing.T, story *Story, state []byte) *quetzal.Save {
	t.Helper()
	qs, err := quetzal.ParseStory(story.image)
	if err != nil {
		t.Fatalf("quetzal.ParseStory() error = %v, want nil", err)
	}
	save, err := quetzal.Read(bytes.NewReader(state), qs)
	if err != nil {
		t.Fatalf("quetzal.Read() error = %v, want nil", err)
	}
	return save
}

// stripChunk rewrites a snapshot without the named custom chunk, standing in
// for a save written by an interpreter that does not know about it.
func stripChunk(t *testing.T, story *Story, state []byte, id string) []byte {
	t.Helper()
	qs, err := quetzal.ParseStory(story.image)
	if err != nil {
		t.Fatalf("quetzal.ParseStory() error = %v, want nil", err)
	}
	save := decodeState(t, story, state)

	kept := save.Chunks[:0]
	for _, c := range save.Chunks {
		if c.ID != chunkID(id) {
			kept = append(kept, c)
		}
	}
	save.Chunks = kept

	var buf bytes.Buffer
	if err := quetzal.Write(&buf, qs, save); err != nil {
		t.Fatalf("quetzal.Write() error = %v, want nil", err)
	}
	return buf.Bytes()
}

// renameChunk returns a copy of state with a chunk identifier replaced, which
// makes a chunk Quetzal requires appear to be missing.
func renameChunk(state []byte, from, to string) []byte {
	return replaceBytes(state, from, to)
}

// corruptChunk returns a copy of state with the payload of the named chunk
// filled with a byte pattern, leaving the container intact so that the failure
// happens inside the chunk rather than in the IFF layer.
func corruptChunk(t *testing.T, state []byte, id string) []byte {
	t.Helper()
	index := bytes.Index(state, []byte(id))
	if index < 0 {
		t.Fatalf("no %s chunk in the saved state", id)
	}
	out := bytes.Clone(state)
	// The four-byte identifier is followed by a four-byte length; the payload
	// starts after them.
	start := index + 8
	length := int(out[index+4])<<24 | int(out[index+5])<<16 | int(out[index+6])<<8 | int(out[index+7])
	if start+length > len(out) {
		t.Fatalf("%s chunk declares %d bytes, past the end of the %d-byte state", id, length, len(out))
	}
	for i := start; i < start+length; i++ {
		out[i] = 0xa5
	}
	return out
}

// replaceBytes returns a copy of data with the first occurrence of from
// replaced by to, which must be the same length.
func replaceBytes(data []byte, from, to string) []byte {
	if len(from) != len(to) {
		panic("replaceBytes: lengths differ")
	}
	index := bytes.Index(data, []byte(from))
	if index < 0 {
		return bytes.Clone(data)
	}
	out := bytes.Clone(data)
	copy(out[index:], to)
	return out
}

// equalWords reports whether two word slices hold the same values.
func equalWords(a, b []uint16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRestoreForeignSaveTakesTheSaveBranch covers a saved state written by
// another interpreter (S 6.1.2, and Quetzal's definition of the IFhd program
// counter for Version 3).
//
// The differential tests exercise this against a real save from dfrotz, but
// they are skipped where dfrotz is not installed, and this path is too easy to
// break to leave uncovered. The save here is built by hand, so it runs
// everywhere.
//
// A save this engine did not write suspends inside the save instruction, and
// Quetzal records the address of that instruction's branch data. Restoring it
// means reading the branch and taking it as though save had reported success.
func TestRestoreForeignSaveTakesTheSaveBranch(t *testing.T) {
	// The branch data sits at the start of the code area. Bit 7 set means
	// branch when the condition is true, bit 6 set means a one-byte branch,
	// and the bottom six bits are the offset: 0x40|0x80|20 branches forward by
	// 20 from the byte after the branch data, less two (S 4.7, S 4.7.2).
	const offset = 20
	branchByte := byte(0x80 | 0x40 | offset)

	for _, tc := range []struct {
		name   string
		branch byte
		wantPC uint32
	}{
		{
			// next is machineCodeBase+1, and the target is next+offset-2.
			name:   "branch on true is taken because the restore succeeded",
			branch: branchByte,
			wantPC: machineCodeBase + 1 + offset - 2,
		},
		{
			// Bit 7 clear asks to branch when save failed. It did not, so
			// execution falls through to the byte after the branch data.
			name:   "branch on false falls through",
			branch: byte(0x40 | offset),
			wantPC: machineCodeBase + 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestMachine(t, tc.branch)

			// A save from another interpreter, carrying none of this engine's
			// custom chunks.
			story, err := m.quetzalStory()
			if err != nil {
				t.Fatalf("quetzalStory() error = %v", err)
			}
			foreign := &quetzal.Save{
				Header: quetzal.Header{
					Release:  story.Release,
					Serial:   story.Serial,
					Checksum: story.Checksum,
					PC:       machineCodeBase,
				},
				Memory: quetzal.Memory{
					Encoding: quetzal.MemoryCompressed,
					Data:     append([]byte(nil), m.mem.dynamic...),
				},
				// Every non-version-6 save begins with the dummy frame that
				// holds the top-level evaluation stack.
				Frames: []quetzal.Frame{{}},
			}
			var buf bytes.Buffer
			if err := quetzal.Write(&buf, story, foreign); err != nil {
				t.Fatalf("writing the foreign save: %v", err)
			}

			if err := m.Restore(buf.Bytes()); err != nil {
				t.Fatalf("Restore(a save from another interpreter) error = %v, want nil", err)
			}
			if m.pc != tc.wantPC {
				t.Errorf("program counter after restoring = 0x%04x, want 0x%04x", m.pc, tc.wantPC)
			}
		})
	}
}

// TestOwnSnapshotIsRecognised checks the mark that tells this engine's saves
// from another interpreter's, since restoring them means resuming in different
// places.
func TestOwnSnapshotIsRecognised(t *testing.T) {
	story := loadZork1(t)
	m, err := New(story, WithRandomSeed(zork1Seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	result, err := m.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	qstory, err := m.quetzalStory()
	if err != nil {
		t.Fatalf("quetzalStory() error = %v", err)
	}
	save, err := quetzal.Read(bytes.NewReader(result.State), qstory)
	if err != nil {
		t.Fatalf("reading this engine's own snapshot: %v", err)
	}
	if !ownSnapshot(save.Chunks) {
		t.Error("this engine did not recognise its own snapshot")
	}
	if ownSnapshot(nil) {
		t.Error("a save with no chunks was taken for this engine's own")
	}
}
