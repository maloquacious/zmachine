package zmachine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The differential tests that need dfrotz on the machine running them.
//
// Everything in differential_test.go compares this engine against files Frotz
// produced, which is enough for the comparison itself and needs nothing
// installed. Two questions are left that a committed file cannot answer:
//
//   - whether those files still match the Frotz that is installed, which is
//     the only thing that can tell a change in this engine from a change in
//     Frotz;
//   - whether Frotz can take up a save this engine wrote, which requires
//     handing a freshly produced file to a running interpreter.
//
// Both are skipped when dfrotz is absent. A skip reads the same as a pass, so
// a run that means to check either can set ZMACHINE_REQUIRE_DFROTZ and find out
// that it did not.

// zork1Path is the story dfrotz is pointed at. It is the same fixture
// loadZork1 reads.
const zork1Path = "testdata/stories/zork1-r119-880429.z3"

// dfrotzFlags are the options that make dfrotz's output comparable, and are the
// ones the committed transcripts were recorded with. Changing them invalidates
// every golden file; see testdata/frotz/README.md.
//
// -p asks for plain ASCII, -q suppresses the interpreter's own startup banner,
// -m removes the MORE prompts that would otherwise wait for a keypress, and
// -h gives it more rows than a transcript will ever use.
//
// The width matters more than it looks. Frotz wraps to the screen width, this
// engine never wraps at all (spec S 12 keeps story whitespace intact and leaves
// presentation to the host), and Version 3 records the width in a single header
// byte, so 255 is both the widest possible screen and the least wrapping that
// can be asked for.
var dfrotzFlags = []string{"-p", "-q", "-m", "-w", "255", "-h", "999"}

// requireDfrotzEnv turns a missing dfrotz from a skip into a failure.
//
// This is a test reading its own environment, not the engine reading one. The
// engine never consults an environment variable during execution (spec S 27),
// and nothing here changes that.
const requireDfrotzEnv = "ZMACHINE_REQUIRE_DFROTZ"

// updateGoldenEnv rewrites the committed transcripts from the installed dfrotz
// instead of comparing against them.
const updateGoldenEnv = "ZMACHINE_UPDATE_GOLDEN"

// requireDfrotz returns the path to dfrotz, skipping the test when it is not
// installed unless the environment insists it be present.
func requireDfrotz(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("dfrotz")
	if err == nil {
		return path
	}
	if os.Getenv(requireDfrotzEnv) != "" {
		t.Fatalf("%s is set but dfrotz is not on PATH: %v", requireDfrotzEnv, err)
	}
	t.Skipf("dfrotz is not installed; this test needs it. Set %s=1 to make this a failure instead.",
		requireDfrotzEnv)
	return ""
}

// dfrotzRun is one invocation of dfrotz.
type dfrotzRun struct {
	seed     uint64
	commands []string
	// loadSave, when set, is a save file passed as -L, which dfrotz restores
	// before the first command.
	loadSave string
	// dir is dfrotz's working directory. It matters because the dumb interface
	// resolves the filename an in-story save prompts for relative to it.
	dir string
}

// runDfrotz plays the story through dfrotz and returns everything it printed.
//
// dfrotz exits non-zero on the end of input, which is how these runs finish, so
// the exit status is deliberately ignored and only the output is used.
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

// TestFrotzGoldensAreCurrent checks the committed transcripts against the
// installed dfrotz.
//
// Its job is not to compare the two interpreters - the tests in
// differential_test.go do that, and do it everywhere. Its job is to notice when
// a committed transcript has stopped describing the Frotz people actually have,
// which is the one failure mode a golden file has and the reason the live run
// is worth keeping.
//
// Setting ZMACHINE_UPDATE_GOLDEN rewrites the files instead of comparing.
func TestFrotzGoldensAreCurrent(t *testing.T) {
	requireDfrotz(t)
	update := os.Getenv(updateGoldenEnv) != ""

	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			fresh := runDfrotz(t, dfrotzRun{seed: s.seed, commands: s.input()})

			if update {
				if err := os.WriteFile(s.goldenPath(), []byte(fresh), 0o644); err != nil {
					t.Fatalf("writing %s: %v", s.goldenPath(), err)
				}
				t.Logf("rewrote %s (%d bytes)", s.goldenPath(), len(fresh))
				return
			}

			if d := diffWords(words(s.golden(t)), words(fresh)); d != "" {
				t.Errorf("the committed transcript no longer matches the installed dfrotz.\n"+
					"Either Frotz changed or the fixture is stale; rerun with %s=1 to rewrite it,\n"+
					"and read the diff before believing it.\n%s", updateGoldenEnv, d)
			}
		})
	}
}

// TestDfrotzResumesOurSnapshot is the direction of layer 3 that matters for a
// host: a state this engine handed back must be a Quetzal file another
// interpreter can take up.
//
// It also settles what the recorded program counter means. This engine
// snapshots at the read instruction itself, so that resuming re-executes it
// with input available. Quetzal describes its program counter relative to a
// save instruction instead, and whether that convention travels is not a
// question this package can answer about itself.
//
// This one cannot be a fixture: the file under test is produced by the engine
// as the test runs, and the thing being checked is that a running interpreter
// accepts it.
func TestDfrotzResumesOurSnapshot(t *testing.T) {
	requireDfrotz(t)
	story := loadZork1(t)

	fixture := frotzSaves[0]
	const seed = 20000
	ours := playOurs(t, story, fixture.commands, WithFrotzRandomSeed(seed))
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
	// prints is what it made of this engine's state.
	raw := runDfrotz(t, dfrotzRun{
		seed:     seed,
		commands: []string{"look", "inventory", "quit", "y"},
		loadSave: path,
		dir:      dir,
	})
	if strings.Contains(raw, "Failed") {
		t.Fatalf("dfrotz refused this engine's snapshot; it printed:\n%s", raw)
	}
	_, theirText := splitDfrotzOutput(raw)
	for _, want := range []string{fixture.room, "trap door", "brass lantern"} {
		if !strings.Contains(theirText, want) {
			t.Errorf("dfrotz did not see %q in this engine's snapshot; it printed:\n%s", want, theirText)
		}
	}

	// The two must also agree about what the resumed session prints next: the
	// same save file, the same commands, one continued here and one by dfrotz.
	resumed, err := New(story, WithFrotzRandomSeed(seed))
	if err != nil {
		t.Fatalf("New() error = %v, want nil", err)
	}
	if err := resumed.Restore(snapshot); err != nil {
		t.Fatalf("Restore(this engine's own snapshot) error = %v, want nil", err)
	}
	var ourContinuation string
	for _, command := range []string{"look", "inventory", "quit", "y"} {
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
