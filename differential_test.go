package zmachine

import (
	"bytes"
	"context"
	"os"
	"os/exec"
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
// They are skipped when dfrotz is not installed. It is a tool the tests can
// use, never a dependency of the engine (CLAUDE.md), and the package must
// build and test without it.
//
// Three layers run here, in increasing order of what they can catch:
//
//	1. the transcript, which exercises text, objects, parsing and branching
//	   all at once but only proves the two agree about what to print;
//	2. the status line, which is drawn from globals rather than printed by the
//	   story, so it catches variable drift a transcript can hide;
//	3. saved state, which compares dynamic memory byte for byte and passes
//	   saves in both directions, so it catches divergence that never reached
//	   the screen at all.

// dfrotzFlags are the options that make dfrotz's output comparable.
//
// -p asks for plain ASCII, -q suppresses the interpreter's own startup banner,
// -m removes the MORE prompts that would otherwise wait for a keypress, and
// -h gives it more rows than the transcript will ever use.
//
// The width matters more than it looks. dfrotz wraps to the screen width, this
// engine never wraps at all (spec S 12 keeps story whitespace intact and
// leaves presentation to the host), and Version 3 records the width in a single
// header byte, so 255 is both the widest possible screen and the least
// wrapping that can be asked for.
var dfrotzFlags = []string{"-p", "-q", "-m", "-w", "255", "-h", "999"}

// zork1Path is the story both interpreters run.
const zork1Path = "testdata/stories/zork1-r119-880429.z3"

// requireDfrotz returns the path to dfrotz, skipping the test when it is not
// installed.
func requireDfrotz(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("dfrotz")
	if err != nil {
		t.Skip("dfrotz is not installed; differential tests need it")
	}
	return path
}

// dfrotzRun is one invocation of dfrotz.
type dfrotzRun struct {
	// seed is passed as -s. Frotz's seed_random counts rather than generating
	// for a seed below 1000, so a run that means to exercise the generator
	// uses a larger one; see WithFrotzRandomSeed.
	seed uint64
	// commands are fed on standard input, one per line.
	commands []string
	// loadSave, when set, is a save file passed as -L, which dfrotz restores
	// before the first command.
	loadSave string
	// dir is dfrotz's working directory. It matters because the dumb interface
	// resolves the filename an in-story save prompts for relative to it.
	dir string
}

// runDfrotz plays a story through dfrotz and returns everything it printed.
//
// dfrotz exits non-zero on the end of input, which is how these runs finish,
// so the exit status is deliberately ignored and only the output is used.
func runDfrotz(t *testing.T, run dfrotzRun) string {
	t.Helper()
	binary := requireDfrotz(t)

	story, err := filepath.Abs(zork1Path)
	if err != nil {
		t.Fatalf("resolving the story path: %v", err)
	}

	args := append([]string{}, dfrotzFlags...)
	args = append(args, "-s", strconv.FormatUint(run.seed, 10))
	if run.loadSave != "" {
		args = append(args, "-L", run.loadSave)
	}
	args = append(args, story)

	cmd := exec.Command(binary, args...)
	cmd.Dir = run.dir
	cmd.Stdin = strings.NewReader(strings.Join(run.commands, "\n") + "\n")
	out, _ := cmd.Output()
	return string(out)
}

// statusLinePattern matches the status line dfrotz draws for a score-and-moves
// game: the room on the left, the score and move count on the right (S 8.2).
// This engine reports the same three values as a struct instead.
var statusLinePattern = regexp.MustCompile(`^>?\s*(.+?)\s{2,}Score:\s*(-?\d+)\s+Moves:\s*(-?\d+)\s*$`)

// dfrotzStatus is one status line dfrotz drew.
type dfrotzStatus struct {
	room  string
	score int16
	moves int16
}

// splitDfrotzOutput separates what dfrotz printed into the status lines it drew
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

// words reduces a transcript to its sequence of words.
//
// Line structure cannot be compared: dfrotz wraps to its screen width and this
// engine does not wrap at all, so the two disagree about where lines end while
// agreeing about every word. The prompt character is dropped for the same
// reason - dfrotz prints it, and this engine leaves prompting to the host.
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
			"  dfrotz : " + strings.Join(theirs[i:min(len(theirs), i+12)], " ")
	}
	if len(ours) != len(theirs) {
		if len(ours) < len(theirs) {
			return "dfrotz printed " + strconv.Itoa(len(theirs)-len(ours)) +
				" more words, beginning: " + strings.Join(theirs[len(ours):min(len(theirs), len(ours)+20)], " ")
		}
		return "this engine printed " + strconv.Itoa(len(ours)-len(theirs)) +
			" more words, beginning: " + strings.Join(ours[len(theirs):min(len(ours), len(theirs)+20)], " ")
	}
	return ""
}

// ourRun is the result of playing a script on this engine.
type ourRun struct {
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

	run := ourRun{}
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

// TestDifferentialTranscriptAboveGround is layer 1 on a script that cannot
// depend on the generator.
func TestDifferentialTranscriptAboveGround(t *testing.T) {
	requireDfrotz(t)
	story := loadZork1(t)

	const seed = 20000
	ours := playOurs(t, story, quitAfter(aboveGroundScript), WithFrotzRandomSeed(seed))
	raw := runDfrotz(t, dfrotzRun{seed: seed, commands: quitAfter(aboveGroundScript)})
	_, theirText := splitDfrotzOutput(raw)

	if d := diffWords(words(ours.transcript), words(theirText)); d != "" {
		t.Errorf("transcripts diverged from dfrotz:\n%s", d)
	}
}

// TestDifferentialTranscriptWithRandom is layer 1 on a script that does depend
// on the generator, which is the case the Frotz-compatible generator exists
// for. A failure here is either a difference in the engine or a difference in
// that generator, and the generator has its own unit tests to tell them apart.
func TestDifferentialTranscriptWithRandom(t *testing.T) {
	requireDfrotz(t)
	story := loadZork1(t)

	// Frotz counts rather than generating for a seed below 1000, so this is
	// comfortably above that; see WithFrotzRandomSeed.
	const seed = 20000
	ours := playOurs(t, story, quitAfter(undergroundScript), WithFrotzRandomSeed(seed))
	raw := runDfrotz(t, dfrotzRun{seed: seed, commands: quitAfter(undergroundScript)})
	_, theirText := splitDfrotzOutput(raw)

	if d := diffWords(words(ours.transcript), words(theirText)); d != "" {
		t.Errorf("transcripts diverged from dfrotz:\n%s", d)
	}
}

// quitAfter appends the commands that make dfrotz leave the game, so that both
// interpreters stop at the same point rather than one of them running on.
func quitAfter(commands []string) []string {
	return append(append([]string{}, commands...), "quit", "y")
}

// TestDifferentialStatusLine is layer 2.
//
// The status line is not printed by the story: the interpreter draws it from
// global variables 1, 2 and 3 before each read (S 8.2). It therefore checks
// something a transcript cannot. A story can print the right room description
// while the interpreter holds the wrong object in global 1, and only this
// catches it.
func TestDifferentialStatusLine(t *testing.T) {
	requireDfrotz(t)
	story := loadZork1(t)

	const seed = 20000
	ours := playOurs(t, story, quitAfter(undergroundScript), WithFrotzRandomSeed(seed))
	raw := runDfrotz(t, dfrotzRun{seed: seed, commands: quitAfter(undergroundScript)})
	theirs, _ := splitDfrotzOutput(raw)

	if len(theirs) == 0 {
		t.Fatal("dfrotz drew no status line; the comparison would be vacuous")
	}

	// dfrotz's dumb interface draws the status line only when it changes,
	// while this engine reports one for every read. A turn that consumes no
	// move - "score" is one - therefore appears here and not there. Collapsing
	// runs of identical lines on both sides compares the sequence of states
	// each interpreter passed through, which is the thing in question, rather
	// than how often each chose to mention it.
	oursChanged, theirsChanged := collapseRuns(ours.statuses), collapseRuns(theirs)

	// Comparing the shorter length keeps a difference in how the two treat the
	// final quit from hiding the differences that matter.
	n := min(len(oursChanged), len(theirsChanged))
	for i := 0; i < n; i++ {
		if oursChanged[i] != theirsChanged[i] {
			t.Fatalf("status line %d = %+v, dfrotz drew %+v", i, oursChanged[i], theirsChanged[i])
		}
	}
	if n < 10 {
		t.Fatalf("only %d status lines were compared; the test is too weak to mean anything", n)
	}
	t.Logf("%d distinct status lines agree with dfrotz", n)
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

// volatileHeaderBytes are the header addresses two interpreters may legitimately
// differ on, and which are therefore excluded when dynamic memory is compared.
//
// Everything here is written by the interpreter to describe itself or its
// screen rather than by the story to record the state of play (S 11.1): the
// flags it sets to advertise what it can do, the interpreter number and
// version, the screen it happens to have, and the standard it claims to meet.
// A difference in any of them says the two interpreters are different programs,
// which is already known.
var volatileHeaderBytes = map[uint32]string{
	0x01: "Flags 1: interpreter capabilities",
	0x10: "Flags 2, high byte",
	0x11: "Flags 2, low byte",
	0x1e: "interpreter number",
	0x1f: "interpreter version",
	0x20: "screen height",
	0x21: "screen width",
	0x32: "standard revision, high byte",
	0x33: "standard revision, low byte",
}

// compareDynamicMemory reports every address at which two images of dynamic
// memory differ, ignoring the header bytes an interpreter owns.
func compareDynamicMemory(t *testing.T, ours, theirs []byte) {
	t.Helper()

	if len(ours) != len(theirs) {
		t.Fatalf("dynamic memory is %d bytes here and %d bytes in the dfrotz save", len(ours), len(theirs))
	}

	var differences []string
	for i := range ours {
		if ours[i] == theirs[i] {
			continue
		}
		addr := uint32(i)
		if _, volatile := volatileHeaderBytes[addr]; volatile {
			continue
		}
		if len(differences) < 16 {
			differences = append(differences,
				"0x"+strconv.FormatUint(uint64(addr), 16)+
					": ours 0x"+strconv.FormatUint(uint64(ours[i]), 16)+
					", dfrotz 0x"+strconv.FormatUint(uint64(theirs[i]), 16))
		}
	}
	if len(differences) > 0 {
		t.Errorf("dynamic memory differs from the dfrotz save at %d addresses:\n  %s",
			len(differences), strings.Join(differences, "\n  "))
	}
}

// saveScript is short and entirely above ground, so that neither generator can
// affect the state being compared.
var saveScript = []string{
	"open mailbox", "take leaflet", "north", "east", "open window", "west",
	"west", "take lamp", "move rug", "open trap door",
}

// TestDifferentialRestoreDfrotzSave is the first half of layer 3: a save
// another interpreter wrote must restore here, and must describe the same state
// of play.
//
// This is the strongest statement the differential tests make. The transcript
// says the two agree about what to print; this says they agree, byte for byte,
// about the contents of dynamic memory - the object tree, every property, every
// global - after the same commands.
func TestDifferentialRestoreDfrotzSave(t *testing.T) {
	requireDfrotz(t)
	story := loadZork1(t)

	dir := t.TempDir()
	const saveName = "differential.qzl"
	const seed = 20000

	// Drive dfrotz to an in-story save. The dumb interface prompts for a
	// filename, so the name is fed as the line after the save command, and it
	// is resolved relative to dfrotz's working directory.
	commands := append(append([]string{}, saveScript...), "save", saveName, "look", "quit", "y")
	raw := runDfrotz(t, dfrotzRun{seed: seed, commands: commands, dir: dir})
	if !strings.Contains(raw, "Ok.") {
		t.Fatalf("dfrotz did not report a successful save; it printed:\n%s", raw)
	}

	saved, err := os.ReadFile(filepath.Join(dir, saveName))
	if err != nil {
		t.Fatalf("dfrotz wrote no save file: %v", err)
	}

	// This engine must accept it.
	m, err := New(story, WithFrotzRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v, want nil", err)
	}
	if err := m.Restore(saved); err != nil {
		t.Fatalf("Restore(a save written by dfrotz) error = %v, want nil", err)
	}

	// And must hold the same dynamic memory as the save it just read, which is
	// what dfrotz had when it wrote it.
	qstory, err := m.quetzalStory()
	if err != nil {
		t.Fatalf("describing the story to the Quetzal package: %v", err)
	}
	save, err := quetzal.Read(bytes.NewReader(saved), qstory)
	if err != nil {
		t.Fatalf("reading the dfrotz save: %v", err)
	}
	compareDynamicMemory(t, m.mem.dynamic, save.Memory.Data)

	// A restored session must also carry on correctly rather than merely load.
	result, err := m.Run(context.Background(), "look")
	if err != nil {
		t.Fatalf("Run(\"look\") after restoring a dfrotz save error = %v, want nil", err)
	}
	if !strings.Contains(result.Output, "Living Room") {
		t.Errorf("after restoring the dfrotz save, look printed %q, want the Living Room", result.Output)
	}
}

// TestDifferentialDfrotzResumesOurSnapshot is the second half of layer 3, and
// the direction that matters for a host: a state this engine handed back must
// be a Quetzal file another interpreter can take up.
//
// It also settles what the recorded program counter means. This engine
// snapshots at the read instruction itself, so that resuming re-executes it
// with input available. Quetzal describes its program counter relative to a
// save instruction instead, and the question of whether that convention travels
// is not one this package can answer about itself.
func TestDifferentialDfrotzResumesOurSnapshot(t *testing.T) {
	requireDfrotz(t)
	story := loadZork1(t)

	const seed = 20000
	ours := playOurs(t, story, saveScript, WithFrotzRandomSeed(seed))
	snapshot := ours.states[len(ours.states)-1]
	if len(snapshot) == 0 {
		t.Fatal("the last turn produced no state to hand to dfrotz")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "ours.qzl")
	if err := os.WriteFile(path, snapshot, 0o644); err != nil {
		t.Fatalf("writing the snapshot: %v", err)
	}

	// dfrotz restores the file before reading its first command, so what it
	// prints for "look" is what it made of this engine's state.
	raw := runDfrotz(t, dfrotzRun{
		seed:     seed,
		commands: []string{"look", "inventory", "quit", "y"},
		loadSave: path,
		dir:      dir,
	})
	if strings.Contains(raw, "Failed") {
		t.Fatalf("dfrotz refused this engine's snapshot; it printed:\n%s", raw)
	}
	if !strings.Contains(raw, "Living Room") {
		t.Errorf("dfrotz resumed this engine's snapshot somewhere unexpected; it printed:\n%s", raw)
	}
	if !strings.Contains(raw, "brass lantern") {
		t.Errorf("dfrotz did not see the lamp this engine was carrying; it printed:\n%s", raw)
	}

	// dfrotz must also see the state of play this engine had reached, not
	// merely a loadable file: the rug moved and the trap door open.
	_, theirText := splitDfrotzOutput(raw)
	if !strings.Contains(theirText, "trap door") {
		t.Errorf("dfrotz did not see the opened trap door; it printed:\n%s", theirText)
	}

	// And the two must agree about what the resumed session prints next: the
	// same save file, the same commands, one continued here and one continued
	// by dfrotz.
	resumed, err := New(story, WithFrotzRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := resumed.Restore(snapshot); err != nil {
		t.Fatalf("Restore(this engine's own snapshot) error = %v, want nil", err)
	}
	var ourContinuation string
	for _, command := range quitAfter([]string{"look", "inventory"}) {
		result, err := resumed.Run(context.Background(), command)
		if err != nil {
			t.Fatalf("Run(%q) after restoring error = %v, want nil", command, err)
		}
		ourContinuation += result.Output
		if result.Status == Halted {
			break
		}
	}
	if d := diffWords(words(ourContinuation), words(theirText)); d != "" {
		t.Errorf("the two interpreters continued this engine's snapshot differently:\n%s", d)
	}
}
