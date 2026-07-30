package zmachine_test

// Runnable examples for the paths a host takes.
//
// Every code sample in the README and docs/reference.md is prose, and prose
// does not compile. These do: go test builds and runs them, and pkg.go.dev
// renders them beside the symbols they document, so a rename or a changed
// signature breaks a test rather than quietly misleading the next reader.
//
// They run against a story assembled here rather than one from
// testdata/stories/, which are fixtures a checkout may not have. A test that
// skips itself proves nothing, and an example with an "// Output:" comment
// cannot skip, so the story an example needs is built from bytes below.

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/maloquacious/zmachine"
)

// Example loads a story, creates a machine and runs it up to the first input
// boundary. This is how a session begins: Start supplies no input, because a
// story prints its banner and opening room before it asks for anything.
func Example() {
	story, err := zmachine.LoadStory(exampleStory())
	if err != nil {
		fmt.Println("load:", err)
		return
	}

	// The seed only makes a run reproducible. A host serving real players
	// leaves it out and lets New seed itself unpredictably.
	machine, err := zmachine.New(story, zmachine.WithRandomSeed(1))
	if err != nil {
		fmt.Println("new:", err)
		return
	}

	result, err := machine.Start(context.Background())
	if err != nil {
		fmt.Println("start:", err)
		return
	}

	fmt.Print(result.Output)
	fmt.Println("waiting for input:", result.Status == zmachine.WaitingForInput)
	fmt.Println("resumable:", len(result.State) > 0)

	// Output:
	// West of House
	// waiting for input: true
	// resumable: true
}

// ExampleMachine_Run supplies one line of player input and executes to the
// next input boundary. Each call returns the text the story printed during
// that call alone, and a state the next call can resume from.
func ExampleMachine_Run() {
	story, err := zmachine.LoadStory(exampleStory())
	if err != nil {
		fmt.Println("load:", err)
		return
	}

	machine, err := zmachine.New(story, zmachine.WithRandomSeed(1))
	if err != nil {
		fmt.Println("new:", err)
		return
	}

	if _, err := machine.Start(context.Background()); err != nil {
		fmt.Println("start:", err)
		return
	}

	result, err := machine.Run(context.Background(), "open mailbox")
	if err != nil {
		fmt.Println("run:", err)
		return
	}

	fmt.Print(result.Output)

	// A story that ends itself with quit reports Halted and carries no state,
	// because there is nothing left to resume.
	last, err := machine.Run(context.Background(), "take leaflet")
	if err != nil {
		fmt.Println("run:", err)
		return
	}

	fmt.Print(last.Output)
	fmt.Println("halted:", last.Status == zmachine.Halted)
	fmt.Println("resumable:", len(last.State) > 0)

	// Output:
	// Opening the small mailbox reveals a leaflet.
	// Taken.
	// halted: true
	// resumable: false
}

// ExampleMachine_Restore shows the turn a request handler performs: create a
// Machine, restore the state from last time, run one command, keep the output
// and the new state, and throw the Machine away.
//
// Doing this on every turn is observably identical to keeping one Machine
// alive for the whole game, which is the central promise of the package. Note
// that the Story is loaded once and shared; only the Machine is per-turn.
func ExampleMachine_Restore() {
	story, err := zmachine.LoadStory(exampleStory())
	if err != nil {
		fmt.Println("load:", err)
		return
	}

	// playOneTurn is the whole of what a handler does. It holds no state of
	// its own: everything the next turn needs is in the bytes it returns.
	playOneTurn := func(saved []byte, command string) (zmachine.Result, error) {
		machine, err := zmachine.New(story, zmachine.WithRandomSeed(1))
		if err != nil {
			return zmachine.Result{}, err
		}
		if err := machine.Restore(saved); err != nil {
			return zmachine.Result{}, err
		}
		return machine.Run(context.Background(), command)
	}

	// The opening turn is the one that differs: there is no saved state yet,
	// so it uses Start rather than Restore and Run.
	opening, err := func() (zmachine.Result, error) {
		machine, err := zmachine.New(story, zmachine.WithRandomSeed(1))
		if err != nil {
			return zmachine.Result{}, err
		}
		return machine.Start(context.Background())
	}()
	if err != nil {
		fmt.Println("start:", err)
		return
	}

	fmt.Print(opening.Output)

	saved := opening.State
	for _, command := range []string{"open mailbox", "take leaflet"} {
		result, err := playOneTurn(saved, command)
		if err != nil {
			fmt.Println(command, "-", err)
			return
		}
		fmt.Print(result.Output)
		saved = result.State
	}

	// Output:
	// West of House
	// Opening the small mailbox reveals a leaflet.
	// Taken.
}

// Example_errorClassification shows a host sorting engine errors into the
// answers it owes a caller. Every error arising from a story, a saved state or
// execution wraps one of the package's sentinels, so the classification never
// depends on message text.
//
// The case worth noticing is cancellation, which wraps no engine sentinel at
// all: it is reported as the context's own error so that errors.Is finds
// context.Canceled, as a caller expects. Test for it first, or a later clause
// will not reach it.
func Example_errorClassification() {
	describe := func(err error) string {
		switch {
		case err == nil:
			return "ok"
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return "cancelled: the caller went away; charge nobody for the turn"
		case errors.Is(err, zmachine.ErrInvalidStory):
			return "bad story: reject the upload"
		case errors.Is(err, zmachine.ErrInvalidState):
			return "bad state: the session cannot be resumed"
		case errors.Is(err, zmachine.ErrExecutionLimit):
			return "too long: the story outran its instruction limit"
		case errors.Is(err, zmachine.ErrExecutionFault),
			errors.Is(err, zmachine.ErrInvalidOpcode),
			errors.Is(err, zmachine.ErrMemoryAccess),
			errors.Is(err, zmachine.ErrInvalidText):
			return "the story faulted: end the session"
		default:
			// A mistake at the call site - a nil context, a bad option - lands
			// here, and so would a bug in the engine.
			return "unclassified"
		}
	}

	// A story that is not a Version 3 story.
	notV3 := exampleStory()
	notV3[0] = 5
	_, err := zmachine.LoadStory(notV3)
	fmt.Println(describe(err))

	// The typed errors carry the detail a log needs. StoryError names the
	// header field at fault and the value that was wrong.
	var storyErr *zmachine.StoryError
	if errors.As(err, &storyErr) {
		fmt.Printf("  field %q, value %d\n", storyErr.Field, storyErr.Value)
	}

	story, err := zmachine.LoadStory(exampleStory())
	if err != nil {
		fmt.Println("load:", err)
		return
	}
	machine, err := zmachine.New(story, zmachine.WithRandomSeed(1))
	if err != nil {
		fmt.Println("new:", err)
		return
	}

	// Saved state that is not a saved game. Restore leaves the machine exactly
	// as it was, so it is still usable below.
	fmt.Println(describe(machine.Restore([]byte("not a saved game"))))

	// A cancelled request. Execution checks the context often enough that the
	// call returns promptly however long the turn would have taken.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = machine.Start(ctx)
	fmt.Println(describe(err))
	fmt.Println("wraps an engine sentinel:", errors.Is(err, zmachine.ErrExecutionFault))

	// Output:
	// bad story: reject the upload
	//   field "version number", value 5
	// bad state: the session cannot be resumed
	// cancelled: the caller went away; charge nobody for the turn
	// wraps an engine sentinel: false
}

// The story the examples run.
//
// It is the smallest thing that can demonstrate a session: it prints a room,
// asks for a line, prints a reply, asks for a second line, prints a second
// reply and quits. That is enough to show an input boundary, a resumable
// state and a clean termination, which is all the examples assert.
//
// Its layout, which is the ordinary Version 3 memory map of S 1.1:
//
//	0x0000 header (S 11.1)
//	0x0040 global variables table, 240 words (S 6.2)
//	0x0220 object table: property defaults only, no objects (S 12.1)
//	0x0260 text buffer, 60 bytes (S 15, read)
//	0x02a0 parse buffer, room for 8 words (S 15, read)
//	0x0300 abbreviations table, 96 words, unused (S 3.3)
//	0x03c0 base of static memory
//	0x03c8 dictionary (S 13.1)
//	0x0400 base of high memory; initial program counter
//	0x0800 end of file
const (
	exampleGlobals       = 0x0040
	exampleObjectTable   = 0x0220
	exampleTextBuffer    = 0x0260
	exampleParseBuffer   = 0x02a0
	exampleAbbreviations = 0x0300
	exampleStaticBase    = 0x03c0
	exampleDictionary    = 0x03c8
	exampleInitialPC     = 0x0400
	exampleStorySize     = 0x0800
)

// exampleStory assembles the story image. It returns a fresh copy each time,
// so an example may corrupt it to produce an error without disturbing another.
func exampleStory() []byte {
	image := make([]byte, exampleStorySize)

	putByte := func(addr int, v uint8) { image[addr] = v }
	putWord := func(addr int, v uint16) { binary.BigEndian.PutUint16(image[addr:], v) }

	// S 11.1. Flags 1 is left zero, which in Version 3 means a score game with
	// a status line available.
	putByte(0x00, 3) // version
	putWord(0x02, 1) // release
	putWord(0x04, exampleInitialPC)
	putWord(0x06, exampleInitialPC) // in Version 3 this is a byte address
	putWord(0x08, exampleDictionary)
	putWord(0x0a, exampleObjectTable)
	putWord(0x0c, exampleGlobals)
	putWord(0x0e, exampleStaticBase)
	copy(image[0x12:0x18], "000000") // serial number
	putWord(0x18, exampleAbbreviations)
	putWord(0x1a, exampleStorySize/2) // S 11.1.6: length divided by 2 in Version 3

	// S 15, read: byte 0 of the text buffer is the maximum number of letters
	// that may be typed, and byte 0 of the parse buffer the maximum number of
	// words that may be parsed.
	putByte(exampleTextBuffer, 60)
	putByte(exampleParseBuffer, 8)

	// S 13.1: a word separator count and the separators, then the entry length
	// and the number of entries. The entries themselves are left zero. The
	// story never reads the parse buffer, so a word the player types simply
	// fails to be found, which is a lookup the tokeniser is required to handle.
	putByte(exampleDictionary, 3)
	copy(image[exampleDictionary+1:], ".,\"")
	putByte(exampleDictionary+4, 7) // bytes per entry
	putWord(exampleDictionary+5, 2) // entries

	// The program. Each turn prints one line and asks for the next, and the
	// last one quits.
	code := concat(
		printString("West of House"),
		newLine(),
		sread(),
		printString("Opening the small mailbox reveals a leaflet."),
		newLine(),
		sread(),
		printString("Taken."),
		newLine(),
		quit(),
	)
	copy(image[exampleInitialPC:], code)

	// S 15, verify: the sum of the bytes from $0040 to the end of the file.
	// The engine records a mismatch rather than refusing the story, but a
	// story worth using as an example should be correct.
	var sum uint16
	for _, b := range image[0x40:] {
		sum += uint16(b)
	}
	putWord(0x1c, sum)

	return image
}

// Instruction encodings (S 4.3). Only the four instructions this story uses
// are here.

// printString is the 0OP instruction print with its text inline (S 15, print).
func printString(s string) []byte {
	return append([]byte{0xb2}, encodeZString(s)...)
}

// newLine is the 0OP instruction new_line (S 15, new_line).
func newLine() []byte { return []byte{0xbb} }

// quit is the 0OP instruction quit (S 15, quit).
func quit() []byte { return []byte{0xba} }

// sread is the VAR instruction read, taking the text and parse buffers as
// large constants (S 15, read). It is the only instruction that suspends.
//
// The type byte gives four two-bit operand types, most significant first:
// two large constants ($$00) and two omitted ($$11).
func sread() []byte {
	return []byte{
		0xe4, 0x0f,
		exampleTextBuffer >> 8, exampleTextBuffer & 0xff,
		exampleParseBuffer >> 8, exampleParseBuffer & 0xff,
	}
}

// encodeZString encodes text as a Version 3 Z-string (S 3.2): three
// five-bit Z-characters to a word, the top bit of the last word set.
//
// It handles the subset the example story needs. A character not in it is a
// mistake in this file rather than anything the engine could be given, so it
// panics rather than encoding something else.
func encodeZString(s string) []byte {
	// S 3.5.3. Z-character 6 of A2 introduces a ten-bit ZSCII escape rather
	// than naming a character, so position 0 here is never encoded to.
	const alphabetA2 = "\x00\r0123456789.,!?_#'\"/\\-:()"

	var chars []uint8
	for _, r := range s {
		switch {
		case r == ' ':
			// S 3.5.1: Z-character 0 is a space in every alphabet.
			chars = append(chars, 0)
		case r >= 'a' && r <= 'z':
			chars = append(chars, uint8(r-'a')+6)
		case r >= 'A' && r <= 'Z':
			// S 3.2.3: Z-character 4 shifts the next character to A1, and in
			// Version 3 the shift lasts for that one character only.
			chars = append(chars, 4, uint8(r-'A')+6)
		default:
			i := strings.IndexRune(alphabetA2, r)
			if i < 1 {
				panic(fmt.Sprintf("example story: %q cannot be encoded", r))
			}
			chars = append(chars, 5, uint8(i)+6)
		}
	}
	// S 3.2.4: a string is padded to a whole number of words with Z-character
	// 5, a shift whose effect never arrives.
	for len(chars) == 0 || len(chars)%3 != 0 {
		chars = append(chars, 5)
	}

	out := make([]byte, 0, len(chars)/3*2)
	for i := 0; i < len(chars); i += 3 {
		word := uint16(chars[i])<<10 | uint16(chars[i+1])<<5 | uint16(chars[i+2])
		if i+3 == len(chars) {
			word |= 0x8000
		}
		out = append(out, uint8(word>>8), uint8(word))
	}
	return out
}

// concat joins encoded instructions.
func concat(parts ...[]byte) []byte {
	var out []byte
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}
