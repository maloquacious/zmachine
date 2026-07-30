package zmachine

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/bits"

	"github.com/maloquacious/quetzal"
)

// The Quetzal state adapter (spec S 22).
//
// This file is the only place that knows the saved-state format exists. It
// translates in both directions between a Machine and the github.com/
// maloquacious/quetzal package's model of a saved game:
//
//	Machine  <->  quetzal.Save  <->  []byte
//
// Nothing above this file deals in chunks, and nothing in this file executes
// instructions. The engine never touches a filesystem: a snapshot is bytes
// handed back to the host, and a restore is bytes handed in.
//
// # Where the saved program counter points
//
// Quetzal describes IFhd's program counter relative to a save instruction: on
// Version 3 it addresses the branch data of the save that produced the file.
// This engine does not snapshot at a save instruction. It snapshots at the
// input boundary of spec S 4, which is the moment the story asked for a line
// with sread and none had been supplied, and the program counter it records is
// the address of that sread instruction itself.
//
// That is the convention the engine's suspension design already requires.
// S 4.5.2 makes operand evaluation order observable, because reading a variable
// operand can pop the evaluation stack (S 6.3.1), so the execution loop
// suspends before sread's operands are evaluated and leaves the program counter
// on the instruction. Resuming therefore re-decodes and re-executes sread with
// the host's line available, and the stack is in exactly the state it was in
// before the instruction was first reached. Recording anything else - the
// address after the instruction, say - would need the operand values and the
// buffer addresses to be carried separately, which is more state to get wrong
// for no gain.
//
// The convention is well defined for a foreign interpreter too. Such an
// interpreter restores the file, resumes at the address recorded, decodes an
// ordinary sread, and asks its player for a line: the save simply lands at a
// prompt. Only an interpreter that expected to skip a save instruction's branch
// data would be confused, and there is no save instruction here to skip.

const (
	// idRandom is the custom chunk holding the machine's random number
	// generator state. Quetzal defines no chunk for it, and S 2.4 requires that
	// a generator seeded by the story keep producing that sequence, so a save
	// that dropped it would silently change the story's random behaviour across
	// a request boundary.
	//
	// The identifier is this engine's own and collides with none that Quetzal
	// defines (IFhd, CMem, UMem, Stks, IntD, ANNO, AUTH and "(c) "). An
	// interpreter that does not recognise it ignores it, which is the correct
	// outcome: it will seed its own generator instead.
	idRandom = "ZRnd"

	// idScreen is the custom chunk holding the logical screen state of S 8.6:
	// which window is selected, the height of the upper window, whether the
	// screen stream is on, and any output stream 3 tables still selected. None
	// of it is story memory, so Quetzal does not carry it, but all of it can
	// differ at an input boundary, and spec S 23 promises that restoring is
	// indistinguishable from never having stopped.
	idScreen = "ZScr"

	// stateChunkVersion is the first byte of both custom chunks, so that a
	// later revision of either payload can be told from this one. A chunk whose
	// version is not understood is ignored rather than refused: it belongs to
	// this engine, and the fallbacks below are safe.
	//
	// Version 2 added the generator byte to the random chunk.
	stateChunkVersion = 2

	// maxRandomStateBytes bounds the marshalled generator state a save may
	// carry. math/rand/v2's PCG marshals to 20 bytes; the ceiling is generous
	// enough to survive a change in that encoding and small enough that a
	// hostile save cannot use the chunk to force an allocation.
	maxRandomStateBytes = 256
)

// snapshot encodes the machine's current state as a Quetzal file (spec S 22).
//
// It is taken at an input boundary, where the program counter stands on the
// sread instruction that asked for the line; see the note above on what the
// recorded program counter means.
func (m *Machine) snapshot() ([]byte, error) {
	story, err := m.quetzalStory()
	if err != nil {
		return nil, err
	}

	frames, err := m.saveFrames()
	if err != nil {
		return nil, err
	}
	random, err := m.encodeRandomChunk()
	if err != nil {
		return nil, err
	}

	save := &quetzal.Save{
		Header: quetzal.Header{
			Release:  m.story.release,
			Serial:   quetzal.Serial(m.story.serial),
			Checksum: story.Checksum,
			PC:       m.pc,
		},
		Memory: quetzal.Memory{
			// Compressed memory is what interpreters normally write and is a
			// difference against the story, so a snapshot costs bytes in
			// proportion to what the turn changed rather than to the size of
			// dynamic memory.
			Encoding: quetzal.MemoryCompressed,
			Data:     m.mem.dynamic,
		},
		Frames: frames,
		Chunks: []quetzal.Chunk{random, m.encodeScreenChunk()},
	}

	var buf bytes.Buffer
	if err := quetzal.Write(&buf, story, save); err != nil {
		// The save was assembled from this machine's own state, so a refusal
		// here is a defect in the adapter rather than bad input.
		return nil, fmt.Errorf("zmachine: writing the saved state at pc 0x%04x: %w", m.pc, err)
	}
	m.logger.Debug("state snapshot taken",
		slog.Uint64("pc", uint64(m.pc)),
		slog.Int("frames", len(frames)),
		slog.Int("bytes", buf.Len()))
	return buf.Bytes(), nil
}

// Restore replaces the machine's state with one previously returned in
// Result.State (spec S 9).
//
// The machine must have been created from the same story the state was saved
// from: a state belonging to another story is refused with an error wrapping
// ErrInvalidState rather than being decoded against the wrong memory. Saved
// state is untrusted input (spec S 26), so every address, count and length in
// it is checked before anything is allocated or written; malformed state
// returns an error and never panics.
//
// A successful restore leaves the machine at an input boundary, so that the
// next call is Run, which supplies a line. On failure the machine is left
// exactly as it was, so a host may report the error and retry with different
// state.
//
// Saves written by this engine and saves written by another interpreter
// suspend in different places, and Restore accepts both; see
// resumeForeignSave.
func (m *Machine) Restore(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("zmachine: Restore: no saved state: %w", ErrInvalidState)
	}

	story, err := m.quetzalStory()
	if err != nil {
		return err
	}

	// The limits are this machine's own, so a save cannot ask for a stack or a
	// call chain larger than the machine would ever have built itself. Quetzal
	// checks them before allocating, which is what keeps a hostile save from
	// turning a declared length into memory (spec S 26).
	limits := quetzal.Limits{
		MaxFrames: m.maxDepth,
		// Locals are counted alongside evaluation words, so the ceiling covers
		// a full evaluation stack plus the most locals the call chain can hold.
		MaxStackWords: m.maxStack + m.maxDepth*maxLocalsV3,
	}
	save, err := quetzal.Read(bytes.NewReader(data), story, quetzal.WithLimits(limits))
	if err != nil {
		return restoreError(err)
	}

	// Everything below builds the new state in local variables and only commits
	// it once all of it has been checked, so a rejected state cannot leave the
	// machine half-restored.
	if err := m.checkRestoredPC(save.Header.PC); err != nil {
		return err
	}
	frames, stack, err := m.restoredFrames(save.Frames)
	if err != nil {
		return err
	}
	if len(save.Memory.Data) != len(m.mem.dynamic) {
		// quetzal.Read enforces this against the story, so reaching it would
		// mean the story this machine holds and the one handed to Read differ.
		return fmt.Errorf("zmachine: Restore: saved dynamic memory is %d bytes, but the story's is %d: %w",
			len(save.Memory.Data), len(m.mem.dynamic), ErrInvalidState)
	}

	copy(m.mem.dynamic, save.Memory.Data)
	m.pc = save.Header.PC
	m.frames = frames
	m.stack = stack
	// A restored machine is one that has already run: Start would begin the
	// story again, so it is refused, and Run continues from the boundary.
	m.started = true
	m.halted = false
	m.input, m.hasInput = "", false
	m.status = StatusLine{}

	if err := m.restoreRandomChunk(save.Chunks); err != nil {
		return err
	}
	m.restoreScreenChunk(save.Chunks)

	// A save this engine did not write suspends somewhere else entirely, and
	// the program counter has to be moved before the machine can run.
	if !ownSnapshot(save.Chunks) {
		if err := m.resumeForeignSave(); err != nil {
			return err
		}
	}

	// S 11.1: the header fields describing the interpreter belong to this
	// machine and not to the machine that wrote the save, so they are set again
	// over the restored dynamic memory. S 6.1.3 makes the same point about
	// restore: the flags an interpreter is responsible for are not restored
	// from the file.
	if err := m.initHeader(); err != nil {
		return fmt.Errorf("zmachine: Restore: %w", err)
	}

	m.logger.Debug("state restored",
		slog.Uint64("pc", uint64(m.pc)),
		slog.Int("frames", len(m.frames)),
		slog.Int("stack", len(m.stack)))
	return nil
}

// quetzalStory describes this machine's story to the Quetzal package: the
// identity a save is matched against and the original dynamic memory that
// compressed memory is a difference against.
//
// It is built from the story image on each call rather than cached, so that the
// identity is exactly the one quetzal.ParseStory computes - which matters,
// because a story carrying no checksum in its header has one computed from the
// image instead, and a save must record the value other interpreters agree on.
// The cost is one copy of dynamic memory per request boundary, which is nothing
// beside executing a turn.
func (m *Machine) quetzalStory() (quetzal.Story, error) {
	story, err := quetzal.ParseStory(m.story.image)
	if err != nil {
		// The image has already been validated as a Version 3 story, so Quetzal
		// disagreeing about it is an internal inconsistency, not bad input.
		return quetzal.Story{}, fmt.Errorf("zmachine: describing the story to the saved-state format: %w", err)
	}
	if story.ChecksumComputed {
		m.logger.Debug("story carries no header checksum; one was computed from the image",
			slog.Uint64("checksum", uint64(story.Checksum)))
	}
	return story, nil
}

// saveFrames translates the machine's call chain into the frames a Quetzal file
// records, oldest first.
//
// The first is the dummy frame every version but 6 requires. Execution in
// Version 3 begins at an address rather than in a routine (S 5.5), so words can
// sit on the evaluation stack while no routine has been called; the dummy frame
// is where Quetzal keeps them. This engine models that initial environment as
// frame 0, whose fields are all zero, so it maps onto the dummy frame directly.
func (m *Machine) saveFrames() ([]quetzal.Frame, error) {
	out := make([]quetzal.Frame, 0, len(m.frames))
	for i, f := range m.frames {
		// S 6.3.1: a routine owns the stack entries pushed since it was called,
		// so each frame's slice runs from its own watermark to the next
		// frame's, and the innermost frame's runs to the top of the stack.
		top := len(m.stack)
		if i+1 < len(m.frames) {
			top = m.frames[i+1].stackBase
		}
		if f.stackBase < 0 || top < f.stackBase || top > len(m.stack) {
			return nil, fmt.Errorf("zmachine: frame %d spans stack entries %d to %d of %d: %w",
				i, f.stackBase, top, len(m.stack), ErrExecutionFault)
		}
		evaluation := make([]uint16, top-f.stackBase)
		copy(evaluation, m.stack[f.stackBase:top])

		if i == 0 {
			// Frame 0 is the initial execution environment of S 5.5. It carries
			// no locals, no arguments and no result variable, and its
			// returnPC - which New sets to the initial program counter so that
			// the field is never meaningless - is not part of the saved state,
			// because S 5.5 forbids returning from it at all. Writing it as
			// zero is what makes Frame.IsDummy true.
			out = append(out, quetzal.Frame{Evaluation: evaluation})
			continue
		}

		// S 6.4.4.1 lets a call supply more or fewer arguments than the routine
		// has locals, so the count is recorded rather than derived. Quetzal
		// stores it as the supplied-argument mask 0gfedcba, which has room for
		// the seven arguments a routine can take; Version 3's call takes at
		// most three.
		if f.argCount > 7 {
			return nil, fmt.Errorf("zmachine: frame %d was called with %d arguments, more than the 7 the saved-state format records: %w",
				i, f.argCount, ErrExecutionFault)
		}
		if f.numLocals > maxLocalsV3 {
			return nil, fmt.Errorf("zmachine: frame %d declares %d local variables, more than the %d of S 5.2: %w",
				i, f.numLocals, maxLocalsV3, ErrExecutionFault)
		}
		locals := make([]uint16, f.numLocals)
		copy(locals, f.locals[:f.numLocals])

		out = append(out, quetzal.Frame{
			ReturnPC: f.returnPC,
			// Every Version 3 call stores its result (S 6.4.5); the p bit
			// exists for the call_xn family of later versions.
			DiscardResult:  !f.hasStore,
			ResultVariable: f.store,
			Arguments:      uint8(1)<<f.argCount - 1,
			Locals:         locals,
			Evaluation:     evaluation,
		})
	}
	return out, nil
}

// restoredFrames translates saved frames back into the machine's call chain and
// evaluation stack.
//
// Every field comes from untrusted input, so each is checked against what this
// machine could have produced before any of it is used: the frame count against
// the call depth limit, each return address against the story, each local count
// against S 5.2, and the total of all the evaluation stacks against the stack
// limit. The stack is allocated once, at a size the checks have already bounded.
func (m *Machine) restoredFrames(saved []quetzal.Frame) ([]frame, []uint16, error) {
	if len(saved) == 0 {
		return nil, nil, fmt.Errorf("zmachine: Restore: the saved state has no call frames, not even the dummy frame that holds the top-level evaluation stack: %w",
			ErrInvalidState)
	}
	if len(saved) > m.maxDepth {
		return nil, nil, fmt.Errorf("zmachine: Restore: the saved state has %d call frames, more than this machine's limit of %d: %w",
			len(saved), m.maxDepth, ErrInvalidState)
	}
	if !saved[0].IsDummy() {
		// quetzal.Read checks this too. Repeating it keeps the requirement
		// visible where the initial environment of S 5.5 is rebuilt, and gives
		// the host an error in this package's own terms.
		return nil, nil, fmt.Errorf("zmachine: Restore: the first saved frame is not the dummy frame that a Version 3 save must begin with: %w",
			ErrInvalidState)
	}

	total := 0
	for i, f := range saved {
		total += len(f.Evaluation)
		if total > m.maxStack {
			return nil, nil, fmt.Errorf("zmachine: Restore: frame %d takes the saved evaluation stack past this machine's limit of %d entries: %w",
				i, m.maxStack, ErrInvalidState)
		}
		if len(f.Locals) > maxLocalsV3 {
			return nil, nil, fmt.Errorf("zmachine: Restore: frame %d has %d local variables, more than the %d S 5.2 permits: %w",
				i, len(f.Locals), maxLocalsV3, ErrInvalidState)
		}
		if i > 0 && !m.mem.readable(f.ReturnPC, 1) {
			return nil, nil, fmt.Errorf("zmachine: Restore: frame %d returns to 0x%04x, which is beyond the end of the story (0x%04x): %w",
				i, f.ReturnPC, m.mem.size(), ErrInvalidState)
		}
	}

	frames := make([]frame, 0, len(saved))
	stack := make([]uint16, 0, total)
	for i, f := range saved {
		out := frame{stackBase: len(stack)}
		if i == 0 {
			// S 5.5: the initial environment has no locals and cannot be
			// returned from. Its return address is the story's initial program
			// counter, as it is in a machine that has never been saved, rather
			// than the zero the dummy frame stores.
			out.returnPC = m.story.initialPC
		} else {
			out.returnPC = f.ReturnPC
			out.numLocals = uint8(len(f.Locals))
			copy(out.locals[:], f.Locals)
			out.hasStore = !f.DiscardResult
			out.store = f.ResultVariable
			// The mask records which of the seven arguments were supplied
			// (S 6.4.4.1). A well-formed mask is a run of low bits, so counting
			// them recovers the argument count; counting rather than measuring
			// the run means a mask with a gap in it still yields a number in
			// range instead of being refused, since Version 3 has no
			// instruction that can observe the difference.
			out.argCount = uint8(bits.OnesCount8(f.Arguments))
		}
		frames = append(frames, out)
		stack = append(stack, f.Evaluation...)
	}
	return frames, stack, nil
}

// checkRestoredPC rejects a saved program counter that does not address an
// instruction in this story.
func (m *Machine) checkRestoredPC(pc uint32) error {
	if pc < headerSize {
		return fmt.Errorf("zmachine: Restore: the saved program counter 0x%04x is inside the %d-byte header: %w",
			pc, headerSize, ErrInvalidState)
	}
	if !m.mem.readable(pc, 1) {
		return fmt.Errorf("zmachine: Restore: the saved program counter 0x%04x is beyond the end of the story (0x%04x): %w",
			pc, m.mem.size(), ErrInvalidState)
	}
	return nil
}

// encodeRandomChunk stores the random generator state in the custom chunk
// described at the top of this file.
//
// The payload is a version byte, a flags byte whose bit 0 is the "predictable"
// state of S 2.4, a byte naming which generator the state belongs to, and the
// marshalled generator state itself.
//
// The generator is named because this engine has more than one: the default,
// and the Frotz-compatible generator used for differential testing (S 33).
// Reading a state back into the wrong generator would produce plausible
// numbers from the wrong sequence, which is worse than refusing it.
func (m *Machine) encodeRandomChunk() (quetzal.Chunk, error) {
	state, predictable, err := m.randomState()
	if err != nil {
		return quetzal.Chunk{}, err
	}
	if len(state) > maxRandomStateBytes {
		return quetzal.Chunk{}, fmt.Errorf("zmachine: the random generator state is %d bytes, more than the %d a saved state carries: %w",
			len(state), maxRandomStateBytes, ErrExecutionFault)
	}
	flags := uint8(0)
	if predictable {
		flags = 1
	}
	data := make([]byte, 0, 3+len(state))
	data = append(data, stateChunkVersion, flags, m.random.Kind())
	data = append(data, state...)
	return quetzal.Chunk{ID: chunkID(idRandom), Data: data}, nil
}

// restoreRandomChunk puts the generator back into the state a snapshot
// recorded.
//
// A save without the chunk is normal rather than exceptional: another
// interpreter's save will not have one, and neither will a save this engine
// wrote before the chunk existed. In that case the generator is seeded as it is
// for a new machine - from the host's seed if one was given, and unpredictably
// otherwise - which is the "random" state S 2.4 asks for when nothing better is
// known.
func (m *Machine) restoreRandomChunk(chunks []quetzal.Chunk) error {
	data, ok := findChunk(chunks, idRandom)
	if !ok || len(data) < 3 || data[0] != stateChunkVersion {
		if ok {
			m.logger.Warn("saved state carries a random generator chunk this engine cannot read; reseeding",
				slog.Int("bytes", len(data)))
		}
		return m.seedRandom()
	}
	state := data[3:]
	if len(state) > maxRandomStateBytes {
		return fmt.Errorf("zmachine: Restore: the saved random generator state is %d bytes, more than the %d permitted: %w",
			len(state), maxRandomStateBytes, ErrInvalidState)
	}
	if err := m.setRandomState(data[2], state, data[1]&1 != 0); err != nil {
		return fmt.Errorf("zmachine: Restore: %w", err)
	}
	return nil
}

// encodeScreenChunk stores the logical screen state of S 8.6 in the custom
// chunk described at the top of this file.
//
// The payload is a version byte, the selected window, a flags byte whose bit 0
// is the state of output stream 1, the height of the upper window as a word,
// and then one selected stream 3 table per three bytes: its address as a word
// and the count of characters written to it as a word.
//
// The captured text itself is deliberately absent. It is returned to the host
// in each Result and discarded at the start of the next call (spec S 12), so it
// is not part of the state execution resumes from.
func (m *Machine) encodeScreenChunk() quetzal.Chunk {
	flags := uint8(0)
	if m.out.stream1 {
		flags = 1
	}
	data := make([]byte, 0, 5+4*len(m.out.tables))
	data = append(data, stateChunkVersion, m.out.window, flags,
		uint8(m.out.upperHeight>>8), uint8(m.out.upperHeight))
	for _, table := range m.out.tables {
		data = append(data,
			uint8(table.table>>8), uint8(table.table),
			uint8(table.count>>8), uint8(table.count))
	}
	return quetzal.Chunk{ID: chunkID(idScreen), Data: data}
}

// restoreScreenChunk puts the logical screen back into the state a snapshot
// recorded.
//
// A save without the chunk leaves the screen as a new machine has it: the lower
// window selected, the screen stream on and no memory streams (S 7.3, S 8.6.1).
// That is where a Version 3 story stands at an input boundary in all but the
// unusual cases this chunk exists for, so a foreign save restores sensibly.
// Anything the payload cannot express - a table address outside dynamic memory,
// a window Version 3 does not have, more tables than S 7.1.2.1.1 allows - is
// dropped in favour of that default rather than refused, because none of it is
// story state and losing it costs at most some redirected output.
func (m *Machine) restoreScreenChunk(chunks []quetzal.Chunk) {
	m.out.init()

	data, ok := findChunk(chunks, idScreen)
	if !ok {
		return
	}
	if len(data) < 5 || data[0] != stateChunkVersion {
		m.logger.Warn("saved state carries a screen chunk this engine cannot read; the screen returns to its initial state",
			slog.Int("bytes", len(data)))
		return
	}
	window, flags := data[1], data[2]
	if window != windowLower && window != windowUpper {
		m.logger.Warn("saved state names a window Version 3 does not have",
			slog.Uint64("window", uint64(window)))
		return
	}
	tables := data[5:]
	if len(tables)%4 != 0 || len(tables)/4 > memoryStreamDepth {
		m.logger.Warn("saved state has a malformed list of output stream 3 tables",
			slog.Int("bytes", len(tables)))
		return
	}

	restored := make([]memoryStream, 0, len(tables)/4)
	for i := 0; i < len(tables); i += 4 {
		addr := uint32(tables[i])<<8 | uint32(tables[i+1])
		count := uint16(tables[i+2])<<8 | uint16(tables[i+3])
		// S 7.1.2.1: the table is written to as the story prints, so it must
		// lie in dynamic memory, exactly as when the stream was selected.
		if !m.mem.writable(addr, memoryStreamHeader) {
			m.logger.Warn("saved state has an output stream 3 table outside dynamic memory",
				slog.Uint64("table", uint64(addr)))
			return
		}
		restored = append(restored, memoryStream{table: addr, count: count})
	}

	m.out.window = window
	m.out.stream1 = flags&1 != 0
	m.out.upperHeight = uint16(data[3])<<8 | uint16(data[4])
	m.out.tables = restored
}

// findChunk returns the payload of the first chunk with the given identifier.
func findChunk(chunks []quetzal.Chunk, id string) ([]byte, bool) {
	want := chunkID(id)
	for _, c := range chunks {
		if c.ID == want {
			return c.Data, true
		}
	}
	return nil, false
}

// chunkID turns a four-character identifier into the form Quetzal compares.
func chunkID(id string) quetzal.ID {
	var out quetzal.ID
	copy(out[:], id)
	return out
}

// restoreError classifies a failure reported by the Quetzal package.
//
// Every one of them describes state this engine cannot resume from, so all of
// them wrap ErrInvalidState; the underlying error stays in the chain so that a
// host can tell a save for another story from a truncated one with errors.Is or
// errors.As on the Quetzal sentinels.
func restoreError(err error) error {
	var mismatch *quetzal.StoryMismatchError
	if errors.As(err, &mismatch) {
		return fmt.Errorf("zmachine: Restore: the saved state belongs to another story (save %s, story %s): %w: %w",
			mismatch.Save, mismatch.Story, err, ErrInvalidState)
	}
	return fmt.Errorf("zmachine: Restore: reading the saved state: %w: %w", err, ErrInvalidState)
}

// ownSnapshot reports whether a save was written by this engine.
//
// The custom chunks are the mark. Another interpreter has no reason to write
// them and would not know what to put in them, so their presence identifies a
// snapshot this engine produced, and their absence a save from somewhere else.
func ownSnapshot(chunks []quetzal.Chunk) bool {
	if _, ok := findChunk(chunks, idScreen); ok {
		return true
	}
	_, ok := findChunk(chunks, idRandom)
	return ok
}

// resumeForeignSave moves the program counter to where a save written by
// another interpreter should resume.
//
// The two kinds of save suspend at different instructions, and the difference
// is not a disagreement about the format but about what was happening when the
// state was written.
//
// This engine snapshots automatically at an input boundary, so its program
// counter is the address of the read instruction that asked for the line, and
// resuming means executing that instruction again with input available. Quetzal
// has no way to describe that, because it was designed for the other case: a
// story that executed the save instruction itself. There, S 6.1.2 requires
// execution to continue from the save, and in Version 3 save is a branch
// instruction, so Quetzal records the address of its branch data.
//
// Restoring such a save therefore means reading that branch and taking it as
// though save had just reported success, which is what the story is waiting to
// be told. Execution then runs on to the next read, which is where the machine
// stops and the host supplies a line.
func (m *Machine) resumeForeignSave() error {
	branch, next, err := decodeBranchAt(m.mem, m.pc)
	if err != nil {
		return fmt.Errorf("zmachine: Restore: reading the branch of the save instruction at 0x%04x: %w: %w",
			m.pc, err, ErrInvalidState)
	}

	m.logger.Debug("restoring a save written by another interpreter",
		slog.Uint64("pc", uint64(m.pc)),
		slog.Bool("branch_on_true", branch.onTrue))

	// The condition is "the save succeeded", and it did: the state is loaded.
	if !branch.onTrue {
		// The story asked to branch when save failed, so success falls through
		// to the instruction after the branch data.
		m.pc = next
		return nil
	}

	if returns, value := branch.returns(); returns {
		// S 4.7.1: the offsets 0 and 1 return from the current routine rather
		// than jumping.
		result := uint16(0)
		if value {
			result = 1
		}
		if err := m.returnFromRoutine(result); err != nil {
			return fmt.Errorf("zmachine: Restore: returning from the save instruction: %w: %w", err, ErrInvalidState)
		}
		return nil
	}

	target, err := branch.target(next)
	if err != nil {
		return fmt.Errorf("zmachine: Restore: the save instruction branches outside the story: %w: %w", err, ErrInvalidState)
	}
	m.pc = target
	return nil
}
