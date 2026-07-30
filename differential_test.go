package zmachine

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/maloquacious/quetzal"
)

// Differential tests against Frotz (spec S 33).
//
// Every other test in this package says that the engine agrees with what this
// project believes the Z-machine specification requires. These say it agrees
// with an interpreter written by someone else, which is a different and harder
// claim: a misreading of the standard that is consistent throughout this
// package would satisfy every other test and fail these.
//
// Almost all of it runs from committed fixtures under testdata/frotz - a save
// Frotz wrote, and transcripts Frotz printed - so the comparison is part of an
// ordinary go test run and needs nothing installed. dfrotz is a tool used to
// make those fixtures, never a dependency of the engine or of its tests.
// testdata/frotz/README.md has the recipes for remaking them.
//
// Three layers run here, in increasing order of what they can catch:
//
//	1. the transcript, which exercises text, objects, parsing and branching
//	   all at once but only proves the two agree about what to print;
//	2. the status line, which is drawn from globals rather than printed by the
//	   story, so it catches variable drift a transcript can hide;
//	3. saved state, where the position both interpreters reached after the same
//	   commands is compared object by object and global by global, so it catches
//	   divergence that never reached the screen.
//
// The tests that need dfrotz itself live in differential_live_test.go, because
// neither of the questions they ask can be answered by a file sitting in the
// repository.

// frotzFixtureDir holds everything Frotz produced.
const frotzFixtureDir = "testdata/frotz"

// A scenario is one run both interpreters can be asked to make: a seed, a list
// of commands, and the transcript Frotz printed for them.
type scenario struct {
	// name is the base of the golden file, and identifies the run.
	name string
	// seed is passed to dfrotz as -s and to this engine as
	// WithFrotzRandomSeed. Frotz's seed_random counts rather than generating
	// for a seed below 1000, so any run meaning to exercise the generator uses
	// a larger one.
	seed uint64
	// commands are fed to both interpreters, one per line.
	commands []string
}

// aboveGroundScript stays above ground, where Zork I never consults the random
// number generator, so the two interpreters agree whatever their generators do.
var aboveGroundScript = []string{
	"open mailbox", "take leaflet", "read leaflet", "drop leaflet",
	"north", "east", "open window", "west", "west",
	"take lamp", "take sword", "open trophy case", "move rug", "open trap door",
	"east", "turn on lamp", "up", "take rope", "take knife", "down",
	"west", "look", "inventory", "score", "diagnose",
}

// undergroundScript goes down the trap door and fights the troll, which is
// where Zork I draws random numbers. It is only comparable because this engine
// can be asked to draw them the way Frotz does; see WithFrotzRandomSeed.
var undergroundScript = append(append([]string{}, aboveGroundScript[:20]...),
	"west", "down", "north",
	"kill troll with sword", "kill troll with sword", "kill troll with sword",
	"kill troll with sword", "kill troll with sword", "kill troll with sword",
	"look", "inventory", "score", "east", "east", "look", "diagnose",
)

// scenarios are the runs with a committed transcript.
var scenarios = []scenario{
	{name: "zork1-r119-aboveground", seed: 20000, commands: aboveGroundScript},
	{name: "zork1-r119-underground", seed: 20000, commands: undergroundScript},
}

// goldenPath is where a scenario's committed transcript lives. The seed is part
// of the name because a different seed is a different run, not a new version of
// this one.
func (s scenario) goldenPath() string {
	return filepath.Join(frotzFixtureDir, s.name+"-s"+strconv.FormatUint(s.seed, 10)+".txt")
}

// input is what both interpreters are fed.
//
// The quit is included so that the two stop in the same place rather than one
// of them running on past the end of the other's transcript.
func (s scenario) input() []string {
	return append(append([]string{}, s.commands...), "quit", "y")
}

// golden reads a scenario's committed transcript.
func (s scenario) golden(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(s.goldenPath())
	if err != nil {
		t.Fatalf("reading the committed Frotz transcript: %v\nSee %s/README.md for how to make it.",
			err, frotzFixtureDir)
	}
	return string(data)
}

// TestDifferentialTranscripts is layer 1, against committed transcripts.
func TestDifferentialTranscripts(t *testing.T) {
	story := loadZork1(t)

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			ours := playOurs(t, story, s.input(), WithFrotzRandomSeed(s.seed))
			_, theirText := splitDfrotzOutput(s.golden(t))

			if d := diffWords(words(ours.transcript), words(theirText)); d != "" {
				t.Errorf("this engine and Frotz printed different text:\n%s", d)
			}
		})
	}
}

// TestDifferentialStatusLines is layer 2, against the same transcripts.
//
// The status line is not printed by the story: the interpreter draws it from
// global variables 1, 2 and 3 before each read (S 8.2). It therefore checks
// something a transcript cannot. A story can print the right room description
// while the interpreter holds the wrong object in global 1, and only this
// catches it.
func TestDifferentialStatusLines(t *testing.T) {
	story := loadZork1(t)

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			ours := playOurs(t, story, s.input(), WithFrotzRandomSeed(s.seed))
			theirs, _ := splitDfrotzOutput(s.golden(t))
			if len(theirs) == 0 {
				t.Fatal("the transcript holds no status line; the comparison would be vacuous")
			}

			// Frotz's dumb interface draws the status line only when it
			// changes, while this engine reports one for every read. A turn
			// that consumes no move - "score" is one - therefore appears here
			// and not there. Collapsing runs of identical lines on both sides
			// compares the sequence of states each interpreter passed through,
			// which is the thing in question, rather than how often each chose
			// to mention it.
			oursChanged, theirsChanged := collapseRuns(ours.statuses), collapseRuns(theirs)

			n := min(len(oursChanged), len(theirsChanged))
			for i := 0; i < n; i++ {
				if oursChanged[i] != theirsChanged[i] {
					t.Fatalf("status line %d = %+v, Frotz drew %+v", i, oursChanged[i], theirsChanged[i])
				}
			}
			if n < 10 {
				t.Fatalf("only %d status lines were compared; the test is too weak to mean anything", n)
			}
		})
	}
}

// A frotzSave is a committed save file and the commands that produced it.
type frotzSave struct {
	// file is the save, under frotzFixtureDir.
	file string
	// commands are what was typed to reach the position it holds, so that this
	// engine can be played to the same place.
	commands []string
	// room is where those commands end up, for a legible failure.
	room string
}

// frotzSaves are the committed saves. See testdata/frotz/README.md.
var frotzSaves = []frotzSave{
	{
		file: "zork1-r119-trapdoor.qzl",
		commands: []string{
			"open mailbox", "take leaflet", "north", "east", "open window",
			"west", "west", "take lamp", "move rug", "open trap door",
		},
		room: "Living Room",
	},
}

// TestDifferentialGameState is layer 3, and the strongest thing here.
//
// This engine plays the commands that produced a committed Frotz save, and the
// state of play it ends with is compared against the state that save records:
// every object's place in the tree, all thirty-two attributes of each, and
// every global variable. The transcript tests say the two agree about what to
// print; this says they agree about the parts no command ever displayed.
//
// It compares the state of play rather than dynamic memory byte for byte,
// because the two images are captured at different instants and cannot be made
// to agree byte for byte at all. Frotz writes its file from inside the save
// instruction, part way through the turn that asked for it: its text buffer
// already holds "save" while the parser scratch beyond the globals still holds
// what the previous turn left there. This engine can only be observed at a read
// boundary, where both have settled. Objects and globals are the state of play
// of S 6.1; the bytes that differ are working space that belongs to neither
// interpreter's idea of the game.
func TestDifferentialGameState(t *testing.T) {
	story := loadZork1(t)

	for _, fixture := range frotzSaves {
		t.Run(fixture.file, func(t *testing.T) {
			// Where this engine ends up after the same commands.
			ours := playOurs(t, story, fixture.commands, WithFrotzRandomSeed(20000))
			last := ours.statuses[len(ours.statuses)-1]
			if last.room != fixture.room {
				t.Fatalf("the commands ended in %q, want %q; the fixture and the script disagree",
					last.room, fixture.room)
			}

			// What Frotz recorded, read back through this engine so that both
			// sides are inspected by the same accessors.
			theirs := restoreFrotzSave(t, story, fixture.file)

			compareObjectTrees(t, ours.machine, theirs)
			compareGlobals(t, ours.machine, theirs)

			// A failure above names one object or one global. The Quetzal
			// comparison says where in the save the disagreement lives, which
			// is the difference between knowing that something drifted and
			// knowing what did. It is logged only after a failure, because
			// against a Frotz save it always has something to report.
			if t.Failed() {
				t.Logf("this engine's snapshot against the Frotz save:\n%s",
					saveDifferences(t, ours.machine, ours.states[len(ours.states)-1], fixture.file))
			}

			// Holding the same state should also mean behaving the same way.
			compareNextTurn(t, ours.machine, theirs, "look")
		})
	}
}

// restoreFrotzSave builds a machine holding the state a committed Frotz save
// records.
func restoreFrotzSave(t *testing.T, story *Story, file string) *Machine {
	t.Helper()

	saved, err := os.ReadFile(filepath.Join(frotzFixtureDir, file))
	if err != nil {
		t.Fatalf("reading the committed Frotz save: %v", err)
	}
	m, err := New(story, WithFrotzRandomSeed(20000))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if err := m.Restore(saved); err != nil {
		t.Fatalf("Restore(a save written by Frotz) error = %v, want nil", err)
	}
	return m
}

// compareObjectTrees checks every object's place in the tree and every one of
// its attributes (S 12.3).
func compareObjectTrees(t *testing.T, ours, theirs *Machine) {
	t.Helper()

	for number := uint16(1); number <= maxObjectsV3; number++ {
		for _, part := range []struct {
			name string
			read func(*memory, uint16) (uint16, error)
		}{
			{"parent", (*memory).objectParent},
			{"sibling", (*memory).objectSibling},
			{"child", (*memory).objectChild},
		} {
			a, err := part.read(ours.mem, number)
			if err != nil {
				t.Fatalf("object %d %s here: %v", number, part.name, err)
			}
			b, err := part.read(theirs.mem, number)
			if err != nil {
				t.Fatalf("object %d %s in the Frotz save: %v", number, part.name, err)
			}
			if a != b {
				name, _ := ours.mem.objectShortName(number)
				t.Errorf("object %d (%q) %s = %d here, %d in the Frotz save", number, name, part.name, a, b)
			}
		}

		for attribute := uint16(0); attribute < objectAttributeCountV3; attribute++ {
			a, err := ours.mem.objectAttribute(number, attribute)
			if err != nil {
				t.Fatalf("object %d attribute %d here: %v", number, attribute, err)
			}
			b, err := theirs.mem.objectAttribute(number, attribute)
			if err != nil {
				t.Fatalf("object %d attribute %d in the Frotz save: %v", number, attribute, err)
			}
			if a != b {
				name, _ := ours.mem.objectShortName(number)
				t.Errorf("object %d (%q) attribute %d = %v here, %v in the Frotz save",
					number, name, attribute, a, b)
			}
		}
	}
}

// compareGlobals checks the global variables the standard gives a meaning to.
//
// Only globals 1, 2 and 3 are compared, because only those three are defined
// by anything but the story: S 8.2 makes them the object the player is in, the
// score and the turn count, which is why an interpreter can draw a status line
// without understanding the game. Every other global is private to Zork, and
// several of them are parser working space that the fixture catches part way
// through a turn - global 80 holds a pointer that has already moved on to the
// word "save" in the Frotz image and has not in this one. Comparing those
// would be asserting that two interpreters stopped at the same instant, which
// they did not and cannot; see TestDifferentialGameState.
func compareGlobals(t *testing.T, ours, theirs *Machine) {
	t.Helper()

	for _, g := range []struct {
		variable uint8
		what     string
	}{
		{0x10, "the object the player is in"},
		{0x11, "the score"},
		{0x12, "the turn count"},
	} {
		a, err := ours.readGlobal(g.variable)
		if err != nil {
			t.Fatalf("global 0x%02x here: %v", g.variable, err)
		}
		b, err := theirs.readGlobal(g.variable)
		if err != nil {
			t.Fatalf("global 0x%02x in the Frotz save: %v", g.variable, err)
		}
		if a != b {
			t.Errorf("global 0x%02x (%s) = %d here, %d in the Frotz save", g.variable, g.what, a, b)
		}
	}
}

// compareNextTurn checks that two machines holding the same state also behave
// the same way, which is a claim about the state no inspection of it can make.
//
// The command is given twice and only the second answer is compared. The first
// one cannot match, and should not: the restored machine is resuming a save
// instruction Frotz was suspended inside, so before it reaches the command it
// finishes that turn and the story prints "Ok." for a save that has now
// succeeded. That is the behaviour S 6.1.2 asks for, and seeing it here is
// evidence the resume worked rather than a difference to be explained away.
func compareNextTurn(t *testing.T, ours, theirs *Machine, command string) {
	t.Helper()

	run := func(m *Machine, who string) string {
		var last string
		for i := 0; i < 2; i++ {
			result, err := m.Run(context.Background(), command)
			if err != nil {
				t.Fatalf("Run(%q) on %s: %v", command, who, err)
			}
			last = result.Output
		}
		return last
	}

	a := run(ours, "the replayed machine")
	b := run(theirs, "the machine restored from the Frotz save")
	if d := diffWords(words(a), words(b)); d != "" {
		t.Errorf("the replayed and the restored machine answered %q differently:\n%s", command, d)
	}
}

// TestDifferentialRestoreFrotzSave is the other half of layer 3: a save another
// interpreter wrote must restore here and carry on.
//
// It is not the same claim as the memory comparison above. That one says the
// two interpreters reach the same state; this one says this engine can take up
// a file it did not write, which additionally exercises resuming from the save
// instruction another interpreter suspended in.
func TestDifferentialRestoreFrotzSave(t *testing.T) {
	story := loadZork1(t)

	for _, fixture := range frotzSaves {
		t.Run(fixture.file, func(t *testing.T) {
			saved, err := os.ReadFile(filepath.Join(frotzFixtureDir, fixture.file))
			if err != nil {
				t.Fatalf("reading the committed Frotz save: %v", err)
			}

			m, err := New(story, WithFrotzRandomSeed(20000))
			if err != nil {
				t.Fatalf("New() error = %v, want nil", err)
			}
			if _, err := m.Start(context.Background()); err != nil {
				t.Fatalf("Start() error = %v, want nil", err)
			}
			if err := m.Restore(saved); err != nil {
				t.Fatalf("Restore(a save written by Frotz) error = %v, want nil", err)
			}

			result, err := m.Run(context.Background(), "look")
			if err != nil {
				t.Fatalf("Run(\"look\") after restoring a Frotz save error = %v, want nil", err)
			}
			if !strings.Contains(result.Output, fixture.room) {
				t.Errorf("after restoring the Frotz save, look printed %q, want %q",
					result.Output, fixture.room)
			}
			// The trap door was opened before the save was taken, so a restore
			// that lost dynamic memory would not mention it.
			if !strings.Contains(result.Output, "trap door") {
				t.Errorf("the restored session does not see the opened trap door: %q", result.Output)
			}
		})
	}
}

// readFrotzSave decodes a committed save against the story a machine holds.
func readFrotzSave(t *testing.T, m *Machine, file string) *quetzal.Save {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(frotzFixtureDir, file))
	if err != nil {
		t.Fatalf("reading the committed Frotz save: %v", err)
	}
	story, err := m.quetzalStory()
	if err != nil {
		t.Fatalf("describing the story to the Quetzal package: %v", err)
	}
	save, err := quetzal.Read(bytes.NewReader(data), story)
	if err != nil {
		t.Fatalf("reading the Frotz save: %v", err)
	}
	return save
}

// saveDifferences describes, in the Quetzal package's own terms, every way in
// which a snapshot this engine produced differs from a save Frotz wrote.
//
// It is diagnostic and asserts nothing. Differences are expected here and are
// not a failure: the two files are written at different instants inside the
// turn that saved, which is what TestDifferentialGameState explains and why
// that test compares the state of play rather than these bytes. What this is
// for is the moment after such a comparison has already failed, when knowing
// that one object moved is less useful than knowing which address, which frame
// or which local carries the disagreement.
//
// Every option given disregards a difference between two interpreters rather
// than between two positions: the header fields an interpreter fills in to
// describe itself (S 11.1), whether dynamic memory happened to be stored
// compressed, and the chunks this engine adds for state Quetzal does not model.
func saveDifferences(t *testing.T, m *Machine, snapshot []byte, file string) string {
	t.Helper()

	story, err := m.quetzalStory()
	if err != nil {
		t.Fatalf("describing the story to the Quetzal package: %v", err)
	}
	ours, err := quetzal.Read(bytes.NewReader(snapshot), story)
	if err != nil {
		t.Fatalf("reading this engine's own snapshot: %v", err)
	}

	diffs := quetzal.Compare(ours, readFrotzSave(t, m, file),
		quetzal.IgnoreInterpreterHeader(),
		quetzal.IgnoreMemoryEncoding(),
		quetzal.IgnoreChunks(chunkID(idRandom), chunkID(idScreen)),
	)
	if len(diffs) == 0 {
		return "  the two saves agree in everything Quetzal records"
	}

	lines := make([]string, 0, len(diffs))
	for _, d := range diffs {
		lines = append(lines, "  "+d.String())
	}
	return strings.Join(lines, "\n")
}

// statusLinePattern matches the status line Frotz draws for a score-and-moves
// game: the room on the left, the score and move count on the right (S 8.2).
// This engine reports the same three values as a struct instead.
var statusLinePattern = regexp.MustCompile(`^>?\s*(.+?)\s{2,}Score:\s*(-?\d+)\s+Moves:\s*(-?\d+)\s*$`)

// dfrotzStatus is one status line Frotz drew.
type dfrotzStatus struct {
	room  string
	score int16
	moves int16
}

// splitDfrotzOutput separates what Frotz printed into the status lines it drew
// and the story text it printed, in order.
func splitDfrotzOutput(raw string) ([]dfrotzStatus, string) {
	var statuses []dfrotzStatus
	var text []string

	for _, line := range strings.Split(raw, "\n") {
		if m := statusLinePattern.FindStringSubmatch(line); m != nil {
			score, _ := strconv.ParseInt(m[2], 10, 16)
			moves, _ := strconv.ParseInt(m[3], 10, 16)
			statuses = append(statuses, dfrotzStatus{
				room:  strings.TrimSpace(m[1]),
				score: int16(score),
				moves: int16(moves),
			})
			continue
		}
		text = append(text, line)
	}

	return statuses, strings.Join(text, "\n")
}

// collapseRuns removes consecutive repeats, leaving the sequence of distinct
// states a run passed through.
func collapseRuns(in []dfrotzStatus) []dfrotzStatus {
	out := make([]dfrotzStatus, 0, len(in))
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// words reduces a transcript to its sequence of words.
//
// Line structure cannot be compared: Frotz wraps to its screen width and this
// engine does not wrap at all, so the two disagree about where lines end while
// agreeing about every word. The prompt character is dropped for the same
// reason - Frotz prints it, and this engine leaves prompting to the host.
func words(s string) []string {
	return strings.Fields(strings.ReplaceAll(s, ">", " "))
}

// diffWords reports the first place two word sequences differ, with a little
// context, or the empty string when they agree.
func diffWords(ours, theirs []string) string {
	for i := 0; i < len(ours) && i < len(theirs); i++ {
		if ours[i] == theirs[i] {
			continue
		}
		from := max(0, i-12)
		return "first difference at word " + strconv.Itoa(i) + "\n" +
			"  context: ..." + strings.Join(ours[from:i], " ") + "\n" +
			"  ours   : " + strings.Join(ours[i:min(len(ours), i+12)], " ") + "\n" +
			"  Frotz  : " + strings.Join(theirs[i:min(len(theirs), i+12)], " ")
	}
	if len(ours) != len(theirs) {
		if len(ours) < len(theirs) {
			return "Frotz printed " + strconv.Itoa(len(theirs)-len(ours)) +
				" more words, beginning: " + strings.Join(theirs[len(ours):min(len(theirs), len(ours)+20)], " ")
		}
		return "this engine printed " + strconv.Itoa(len(ours)-len(theirs)) +
			" more words, beginning: " + strings.Join(ours[len(theirs):min(len(ours), len(theirs)+20)], " ")
	}
	return ""
}

// ourRun is the result of playing a script on this engine.
type ourRun struct {
	machine    *Machine
	transcript string
	statuses   []dfrotzStatus
	states     [][]byte
}

// playOurs plays a script on one Machine and collects everything the
// differential tests compare.
func playOurs(t *testing.T, story *Story, commands []string, opts ...Option) ourRun {
	t.Helper()

	m, err := New(story, opts...)
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	ctx := context.Background()

	result, err := m.Start(ctx)
	if err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}

	run := ourRun{machine: m}
	record := func(r Result) {
		run.transcript += r.Output
		run.statuses = append(run.statuses, dfrotzStatus{
			room:  r.StatusLine.Name,
			score: r.StatusLine.Score,
			moves: r.StatusLine.Turns,
		})
		run.states = append(run.states, r.State)
	}
	record(result)

	for _, command := range commands {
		result, err = m.Run(ctx, command)
		if err != nil {
			t.Fatalf("Run(%q) error = %v, want nil", command, err)
		}
		record(result)
		if result.Status == Halted {
			break
		}
	}

	return run
}
