package zmachine

import (
	crand "crypto/rand"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math/rand/v2"
)

// Machine is one execution instance of the Z-machine.
//
// A Machine owns every piece of mutable state the Version 3 "state of play"
// consists of (S 6.1): its own copy of dynamic memory, the evaluation stack,
// the chain of routine call frames, and the program counter. Machines built
// from the same Story are therefore completely isolated from one another.
//
// A Machine is not safe for concurrent use. The Story it was built from is.
type Machine struct {
	story *Story
	// mem is this machine's private view of story memory. Dynamic memory is
	// copied; static and high memory are shared read-only with the Story.
	mem *memory

	// pc is the byte address of the next instruction to execute.
	pc uint32
	// frames is the routine call chain. It always holds at least the base
	// frame, which stands for the initial execution environment of S 5.5.
	frames []frame
	// stack is the evaluation stack (S 6.3). Each frame owns the entries above
	// its watermark, so a routine cannot reach its caller's values.
	stack []uint16

	// maxStack bounds the evaluation stack and maxDepth the call chain, so
	// that a runaway story cannot exhaust the host's memory (S 6.3.3).
	maxStack int
	maxDepth int

	// pcg is this machine's random source and rng draws from it. Both are
	// owned by the machine: no package-global generator is ever used.
	pcg *rand.PCG
	rng *rand.Rand
	// seed is the seed the host supplied, and hasSeed reports whether it did.
	// A seeded machine returns to that seed on restart so that a host asking
	// for reproducible execution keeps it (S 2.4 leaves the post-restart state
	// to the interpreter beyond requiring that it be "random").
	seed    uint64
	hasSeed bool
	// predictable reports whether the generator is in the "predictable" state
	// of S 2.4, which random reaches by being given a negative range.
	predictable bool

	// logger receives VM diagnostics. It is never nil and never carries story
	// output.
	logger *slog.Logger
	// tracer receives execution events, and is nil unless the host asked for
	// tracing.
	tracer Tracer
	// trace accumulates what the current instruction did, and is only touched
	// when tracer is non-nil.
	trace traceState

	// instructionLimit bounds the number of instructions one call to Start or
	// Run may execute (spec S 25).
	instructionLimit uint64
	// executed counts the instructions executed by the current call.
	executed uint64

	// out holds the output streams and the logical screen state.
	out output
	// status is the status line as of the last time it was updated (S 8.2.4).
	status StatusLine

	// input is the line of player input the host supplied, waiting to be
	// consumed by the line-input instruction, and hasInput reports whether
	// there is one.
	input    string
	hasInput bool

	// started reports whether execution has begun, and halted whether the
	// story has terminated. A halted machine refuses to run again.
	started bool
	halted  bool
}

// Status reports why execution stopped.
type Status uint8

const (
	// WaitingForInput means execution reached a line-input instruction for
	// which the host has supplied no input, and can be resumed by supplying
	// one.
	WaitingForInput Status = iota
	// Halted means the story terminated itself with quit (S 15, quit).
	Halted
)

// String returns the status name.
func (s Status) String() string {
	switch s {
	case WaitingForInput:
		return "waiting for input"
	case Halted:
		return "halted"
	default:
		return "unknown"
	}
}

// StatusLine is the Version 3 status line (S 8.2), reported to the host
// separately from story output because it is drawn by the interpreter rather
// than printed by the story.
//
// It is updated in exactly two circumstances: when the story executes
// show_status, and just before the line-input instruction reads (S 8.2.4).
type StatusLine struct {
	// Available reports whether the status line has been updated at least
	// once. The remaining fields are meaningless until it is true, because the
	// status line is not displayed when the game begins (S 8.2.4).
	Available bool

	// Object is the object number held in the first global variable, whose
	// short name belongs on the left of the line (S 8.2.2).
	Object uint16
	// Name is the short name of that object. It is empty until the object
	// model is available.
	Name string

	// TimeGame reports which form the right of the line takes: false for a
	// "score game" and true for a "time game" (S 8.2.1). It is fixed by bit 1
	// of Flags 1 in the story header.
	TimeGame bool

	// Score and Turns are the second and third globals in a score game
	// (S 8.2.3.1). They are signed: the score may be negative.
	Score int16
	Turns int16

	// Hours and Minutes are the second and third globals in a time game
	// (S 8.2.3.2).
	Hours   uint8
	Minutes uint8
}

// Result is what one call to Start or Run produced.
type Result struct {
	// Output is the text the story printed to the screen during this call,
	// with the story's whitespace preserved exactly. It never contains the
	// status line and never contains interpreter diagnostics.
	Output string

	// UpperWindow is the text the story printed while the upper window was
	// selected (S 8.6.1). It is reported separately because the upper window
	// overlays fixed screen positions rather than joining the narrative, so
	// merging it into Output would corrupt both.
	UpperWindow string

	// StatusLine is the status line as of the moment execution stopped.
	StatusLine StatusLine

	// State is the resumable machine state. It is nil until Quetzal
	// persistence is available.
	State []byte

	// Status reports why execution stopped.
	Status Status
}

const (
	// maxLocalsV3 is the greatest number of local variables a routine may
	// declare (S 5.2).
	maxLocalsV3 = 15

	// defaultStackLimit bounds the evaluation stack. S 6.3.3 sets the minimum
	// standard at 1024 words and advises a larger stack for modern games; this
	// matches the 32768 words of Windows Frotz.
	defaultStackLimit = 32768

	// defaultCallDepthLimit bounds the routine call chain. S 6.3.3 guarantees
	// a depth of at least 90 calls for a game using the minimum stack, so this
	// is far above anything a working story needs, and exists only to stop a
	// runaway story from growing the frame slice without bound.
	defaultCallDepthLimit = 1024

	// defaultInstructionLimit bounds one call to Start or Run. It is large
	// enough for the opening of a substantial story and small enough that a
	// story looping forever cannot hold a server worker for long.
	defaultInstructionLimit = 10_000_000

	// contextCheckInterval is how often the execution loop checks for
	// cancellation. Checking every instruction would cost more than it is
	// worth; at this interval the cancellation latency is well under a
	// millisecond (spec S 25).
	contextCheckInterval = 1024
)

// Header bits the interpreter is responsible for in Version 3 (S 11.1).
const (
	// Flags 1, bit 1: the status line shows hours:minutes rather than
	// score/turns (S 8.2.1). It belongs to the story and is never written.
	flags1TimeGame = 1 << 1
	// Flags 1, bit 4: set when the interpreter cannot produce a status line
	// (S 8.2).
	flags1NoStatusLine = 1 << 4
	// Flags 1, bit 5: set when screen splitting is available (S 8.6.1.2). A
	// story may only use set_window or split_window when it is set.
	flags1SplitAvailable = 1 << 5
	// Flags 1, bit 6: set when a variable-pitch font is the default (S 11.1).
	flags1VariablePitch = 1 << 6

	// Flags 2, bit 0: set while transcription to output stream 2 is on
	// (S 11.1.2).
	flags2Transcript = 1 << 0
	// Flags 2, bit 1: the story asks for a fixed-pitch font (S 11.1). It
	// belongs to the story; the interpreter only preserves it across restart.
	flags2FixedPitch = 1 << 1
)

// New creates a Machine that will execute story from its initial program
// counter (S 5.5).
//
// Creating a Machine is cheap: only dynamic memory is copied, while static and
// high memory are shared with the Story. Any number of Machines may be built
// from one Story and used independently.
func New(story *Story, opts ...Option) (*Machine, error) {
	if story == nil {
		return nil, fmt.Errorf("zmachine: no story: %w", ErrInvalidStory)
	}

	cfg := defaultConfig()
	for i, opt := range opts {
		if opt == nil {
			return nil, fmt.Errorf("zmachine: option %d is nil", i+1)
		}
		if err := opt(&cfg); err != nil {
			return nil, err
		}
	}

	m := &Machine{
		story:            story,
		mem:              newMemory(story),
		pc:               story.initialPC,
		maxStack:         defaultStackLimit,
		maxDepth:         defaultCallDepthLimit,
		seed:             cfg.seed,
		hasSeed:          cfg.hasSeed,
		logger:           cfg.logger,
		tracer:           cfg.tracer,
		instructionLimit: cfg.instructionLimit,
	}
	// S 5.5: execution begins in an environment with no local variables. It is
	// modelled as a frame so that variable and stack access need no special
	// case, and returning from it is refused rather than crashing.
	m.frames = []frame{{returnPC: story.initialPC}}
	m.stack = make([]uint16, 0, 64)
	m.out.init()

	if err := m.seedRandom(); err != nil {
		return nil, err
	}
	if err := m.initHeader(); err != nil {
		return nil, err
	}

	m.logger.Debug("machine created",
		slog.Uint64("pc", uint64(m.pc)),
		slog.Uint64("release", uint64(story.release)),
		slog.String("serial", story.Serial()))

	return m, nil
}

// Halted reports whether the story has terminated. A halted machine cannot be
// run again.
func (m *Machine) Halted() bool { return m.halted }

// initHeader sets the header fields the interpreter is responsible for
// (the "Rst" entries of S 11.1). It runs when the machine is created and again
// after a restart, because those fields describe the interpreter rather than
// the story and so must not be inherited from a saved image.
func (m *Machine) initHeader() error {
	flags1, err := m.mem.readByte(hdrFlags1)
	if err != nil {
		return err
	}
	// S 8.2: a Version 3 interpreter must set bit 4 if it cannot produce a
	// status line. This engine reports one on every Result, so the bit is
	// cleared.
	flags1 &^= flags1NoStatusLine
	// S 8.6.1.2: bit 5 signals that the upper window exists. It is only legal
	// for a story to use set_window or split_window when the bit is set, and
	// this engine models both, so the bit is set.
	flags1 |= flags1SplitAvailable
	// S 11.1: bit 6 says a variable-pitch font is the default. A headless
	// engine has no fonts, so it claims none.
	flags1 &^= flags1VariablePitch
	if err := m.mem.writeByte(hdrFlags1, flags1); err != nil {
		return err
	}

	flags2, err := m.mem.readWord(hdrFlags2)
	if err != nil {
		return err
	}
	// S 11.1.2: bit 0 must always hold the true state of transcription. This
	// engine provides no output stream 2, so transcription is never on.
	flags2 &^= flags2Transcript
	return m.mem.writeWord(hdrFlags2, flags2)
}

// seedRandom puts the random generator into its starting state: the seed the
// host supplied if there was one, and otherwise an unpredictable seed, which
// is the "random" state S 2.4 requires at the start of a game.
func (m *Machine) seedRandom() error {
	if m.hasSeed {
		m.setRandomSeed(m.seed)
		return nil
	}
	return m.reseedRandom()
}

// setRandomSeed puts the generator into the predictable state of S 2.4.2 with
// the given seed. Sowing the same seed twice must produce the same sequence.
func (m *Machine) setRandomSeed(seed uint64) {
	// PCG takes a 128-bit seed. The second word is a fixed constant so that
	// the whole state is a function of the seed alone, which is what S 2.4.2
	// requires, and PCG's mixing means a very low seed - S 2.4.2 mentions 10 -
	// is still a usable starting point.
	m.pcg = rand.NewPCG(seed, 0x9e3779b97f4a7c15)
	m.rng = rand.New(m.pcg)
	m.predictable = true
}

// reseedRandom puts the generator into the "random" state of S 2.4 with a seed
// the story cannot predict.
//
// The entropy comes from crypto/rand, which reads the host's random source
// directly. The engine never uses the package-global generators of math/rand
// or math/rand/v2, so no process-global state is touched or shared between
// machines.
func (m *Machine) reseedRandom() error {
	var b [16]byte
	if _, err := crand.Read(b[:]); err != nil {
		return fmt.Errorf("zmachine: seeding the random number generator: %w", err)
	}
	m.pcg = rand.NewPCG(binary.BigEndian.Uint64(b[0:8]), binary.BigEndian.Uint64(b[8:16]))
	m.rng = rand.New(m.pcg)
	m.predictable = false
	return nil
}

// randomState returns the generator's state, so that it can be carried across
// a request boundary. It is the marshalled PCG state together with the
// predictable flag of S 2.4.
func (m *Machine) randomState() ([]byte, bool, error) {
	state, err := m.pcg.MarshalBinary()
	if err != nil {
		return nil, false, fmt.Errorf("zmachine: reading the random number generator state: %w", err)
	}
	return state, m.predictable, nil
}

// setRandomState restores a generator state produced by randomState.
func (m *Machine) setRandomState(state []byte, predictable bool) error {
	pcg := &rand.PCG{}
	if err := pcg.UnmarshalBinary(state); err != nil {
		return fmt.Errorf("zmachine: restoring the random number generator state: %w: %w", err, ErrInvalidState)
	}
	m.pcg = pcg
	m.rng = rand.New(pcg)
	m.predictable = predictable
	return nil
}

// randomInRange returns a uniformly distributed number between 1 and n
// inclusive (S 2.4.1). n must be positive.
func (m *Machine) randomInRange(n int16) uint16 {
	return uint16(m.rng.IntN(int(n))) + 1
}

// restart resets the machine to the state it had when the story was loaded
// (S 6.1.3, S 15 restart).
//
// Dynamic memory is reloaded from the story image, the stack and the call
// chain are emptied, and the program counter returns to the initial one -
// which is read from the original story, so a story that changed the header
// word at $06 does not restart from the new address. Only the transcription
// and fixed-pitch bits of Flags 2 survive.
func (m *Machine) restart() error {
	flags2, err := m.mem.readWord(hdrFlags2)
	if err != nil {
		return err
	}
	surviving := flags2 & (flags2Transcript | flags2FixedPitch)

	m.mem = newMemory(m.story)
	m.pc = m.story.initialPC
	m.frames = []frame{{returnPC: m.story.initialPC}}
	m.stack = m.stack[:0]
	m.status = StatusLine{}
	// S 8.5.2: the screen is cleared at the start of a game, so the text
	// captured before the restart is discarded along with the stream and
	// window state the story had set up.
	m.out.init()

	fresh, err := m.mem.readWord(hdrFlags2)
	if err != nil {
		return err
	}
	if err := m.mem.writeWord(hdrFlags2, fresh&^(flags2Transcript|flags2FixedPitch)|surviving); err != nil {
		return err
	}
	if err := m.initHeader(); err != nil {
		return err
	}

	// S 2.4: the generator becomes "random" when the game restarts. A machine
	// the host seeded deliberately returns to that seed instead, so that
	// seeding a machine makes the whole run reproducible.
	if err := m.seedRandom(); err != nil {
		return err
	}

	m.logger.Debug("story restarted", slog.Uint64("pc", uint64(m.pc)))
	return nil
}

// updateStatusLine recomputes the status line from the first three global
// variables (S 8.2.2, S 8.2.3).
//
// The short name of the location object is not filled in yet: it needs the
// object model, which this build does not have.
func (m *Machine) updateStatusLine() error {
	object, err := m.readGlobal(globalFirst)
	if err != nil {
		return err
	}
	second, err := m.readGlobal(globalFirst + 1)
	if err != nil {
		return err
	}
	third, err := m.readGlobal(globalFirst + 2)
	if err != nil {
		return err
	}

	status := StatusLine{
		Available: true,
		Object:    object,
		// S 8.2.1: bit 1 of Flags 1 fixes the form of the line. It is not a
		// field the story may alter, so it is taken from the loaded story.
		TimeGame: m.story.flags1&flags1TimeGame != 0,
	}
	if status.TimeGame {
		// S 8.2.3.2: hours in the second global, minutes in the third.
		status.Hours = uint8(second)
		status.Minutes = uint8(third)
	} else {
		// S 8.2.3.1: the score may be negative, so both are read signed.
		status.Score = signed(second)
		status.Turns = signed(third)
	}
	m.status = status
	return nil
}
