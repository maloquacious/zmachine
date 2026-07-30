package zmachine

import (
	"log/slog"
	"strconv"
)

// Variable-operand instructions (S 14, VAR).
//
// Only the opcodes with bit 5 of the opcode byte set reach here; a 2OP opcode
// assembled in variable form is still a 2OP opcode (S 4.3.3) and is dispatched
// with the other two-operand instructions.

// executeVAR carries out an instruction with a variable number of operands.
func (m *Machine) executeVAR(inst *instruction, ops []uint16) (control, error) {
	switch inst.op {
	case opCall:
		// S 15, call: call the routine with 0, 1, 2 or 3 arguments and store
		// the return value. It is the only call instruction in Version 3.
		if err := m.operands(inst, 1, maxOperandsV3); err != nil {
			return controlContinue, err
		}
		// The program counter already points past the instruction, which is
		// where the routine returns to.
		if err := m.callRoutine(ops[0], ops[1:], m.pc, inst.store, inst.hasStore); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opStoreW:
		// S 15, storew: store a word at address array + 2*index, which must
		// lie in dynamic memory.
		if err := m.operands(inst, 3, 3); err != nil {
			return controlContinue, err
		}
		addr, err := m.arrayAddress(inst, ops[0], ops[1], wordAddressScale)
		if err != nil {
			return controlContinue, err
		}
		if err := m.mem.writeWord(addr, ops[2]); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opStoreB:
		// S 15, storeb: store a byte at address array + index, which must lie
		// in dynamic memory.
		if err := m.operands(inst, 3, 3); err != nil {
			return controlContinue, err
		}
		addr, err := m.arrayAddress(inst, ops[0], ops[1], 1)
		if err != nil {
			return controlContinue, err
		}
		if err := m.mem.writeByte(addr, uint8(ops[2])); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opPrintChar:
		// S 15, print_char: print one ZSCII character. The operand must be a
		// code defined in ZSCII for output (S 3.8); one that is not prints
		// nothing rather than reaching the captured output.
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		if err := m.printZSCII(ops[0]); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opPrintNum:
		// S 15, print_num: print a signed number in decimal.
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		if err := m.printText(strconv.FormatInt(int64(signed(ops[0])), 10)); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opRandom:
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		return controlContinue, m.executeRandom(inst, signed(ops[0]))

	case opPush:
		// S 15, push: push a value onto the game stack.
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		if err := m.push(ops[0]); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opPull:
		// S 15, pull: pull a value off the game stack into the variable the
		// operand names. In Version 3 it is not a store instruction: the
		// destination is an operand, and the reference is indirect, so pulling
		// into the stack pointer overwrites the item below the one pulled
		// rather than pushing it straight back (S 6.3.4).
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		value, err := m.pop()
		if err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if err := m.writeVariableIndirect(uint8(ops[0]), value); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		if m.tracing() {
			m.trace.stored = true
			m.trace.storeVariable = uint8(ops[0])
			m.trace.storeValue = value
		}
		return controlContinue, nil

	case opSplitWindow:
		// S 15, split_window: give the upper window the stated number of
		// lines, or unsplit the screen when it is zero.
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		m.splitWindow(ops[0])
		return controlContinue, nil

	case opSetWindow:
		// S 15, set_window: select a window for text output.
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		if err := m.setWindow(ops[0]); err != nil {
			return controlContinue, m.fail(inst, err)
		}
		return controlContinue, nil

	case opOutputStream:
		return controlContinue, m.executeOutputStream(inst, ops)

	case opInputStream:
		// S 15, input_stream: select the current input stream. Stream 0 is the
		// keyboard, which here is the line the host supplied, and stream 1 is
		// a command file, which a headless engine cannot open (S 7.6.5).
		// Selecting one that is not available is reported to the log and
		// otherwise ignored, because failing here would end a turn over
		// something the story does not depend on.
		if err := m.operands(inst, 1, 1); err != nil {
			return controlContinue, err
		}
		if ops[0] != 0 {
			m.logger.Warn("input stream not available",
				slog.Uint64("stream", uint64(ops[0])), slog.Uint64("pc", uint64(inst.addr)))
		}
		return controlContinue, nil

	case opSoundEffect:
		// S 15, sound_effect: Version 3 sound, used by one story. There is no
		// audio device, and S 15 asks interpreters not to halt over it.
		m.logger.Debug("sound effect ignored", slog.Uint64("pc", uint64(inst.addr)))
		return controlContinue, nil

	case opPutProp:
		// The object model is not part of this build.
		return m.notImplemented(inst)

	case opSRead:
		// Line input is not part of this build.
		return m.notImplemented(inst)

	default:
		return controlContinue, m.fault(inst, ErrInvalidOpcode, "not dispatched")
	}
}

// executeRandom carries out random (S 15, random; S 2.4).
//
// A positive range returns a uniformly distributed number between 1 and the
// range. A negative range seeds the generator with that value and returns 0. A
// range of zero reseeds the generator as unpredictably as the engine can and
// returns 0; interpreters that treat it as illegal are wrong to.
func (m *Machine) executeRandom(inst *instruction, rangeValue int16) error {
	switch {
	case rangeValue > 0:
		// S 2.4.1: the generator must produce a uniformly random integer in
		// 1 <= x <= n for any 1 <= n <= 32767, which a positive 16-bit range
		// cannot exceed.
		return m.store(inst, m.randomInRange(rangeValue))

	case rangeValue < 0:
		// S 2.4.2: the generator moves to its predictable state, seeded with
		// this value. int32 is used because negating -32768 overflows int16.
		seed := uint64(-int32(rangeValue))
		m.setRandomSeed(seed)
		m.logger.Debug("random generator seeded", slog.Uint64("seed", seed))
		return m.store(inst, 0)

	default:
		// S 15, random: a range of zero reseeds the generator in as random a
		// way as the interpreter can.
		if err := m.reseedRandom(); err != nil {
			return m.fail(inst, err)
		}
		m.logger.Debug("random generator reseeded unpredictably")
		return m.store(inst, 0)
	}
}

// executeOutputStream carries out output_stream (S 15, output_stream; S 7.1).
//
// A stream number of zero does nothing, a positive number selects that stream
// and a negative one deselects it. Streams this engine does not provide are
// reported to the log and otherwise ignored, which is what S 7.6.5.2 asks for:
// a story does not depend on a transcript existing, and refusing the whole
// turn over one would be worse than not transcribing.
func (m *Machine) executeOutputStream(inst *instruction, ops []uint16) error {
	if err := m.operands(inst, 1, 2); err != nil {
		return err
	}
	number := signed(ops[0])

	switch number {
	case 0:
		// S 15, output_stream: if the stream is 0, nothing happens.
		return nil

	case streamScreen:
		m.out.stream1 = true
		return nil
	case -streamScreen:
		m.out.stream1 = false
		return nil

	case streamTranscript, -streamTranscript:
		// The transcript stream writes to a printer or a file, neither of
		// which exists here. S 11.1.2 requires bit 0 of Flags 2 to hold the
		// true state of transcription, so it is cleared: transcription is off
		// and stays off.
		if err := m.clearTranscriptFlag(); err != nil {
			return m.fail(inst, err)
		}
		m.logger.Warn("output stream 2 (transcript) is not available",
			slog.Uint64("pc", uint64(inst.addr)))
		return nil

	case streamMemory:
		// S 15, output_stream: when stream 3 is selected a table must be
		// given.
		if inst.numOps < 2 {
			return m.fault(inst, nil, "selecting output stream 3 requires a table address")
		}
		if err := m.selectMemoryStream(uint32(ops[1])); err != nil {
			return m.fail(inst, err)
		}
		return nil
	case -streamMemory:
		if err := m.deselectMemoryStream(); err != nil {
			return m.fail(inst, err)
		}
		return nil

	case streamCommands, -streamCommands:
		// Stream 4 records the player's commands to a script file, which a
		// headless engine cannot open (S 7.6.5).
		m.logger.Warn("output stream 4 (command script) is not available",
			slog.Uint64("pc", uint64(inst.addr)))
		return nil

	default:
		// S 7.1 defines streams 1 to 4 only. A story naming another one has a
		// defect, but nothing depends on the stream existing, so the request
		// is recorded and dropped.
		m.logger.Warn("unknown output stream",
			slog.Int("stream", int(number)), slog.Uint64("pc", uint64(inst.addr)))
		return nil
	}
}

// clearTranscriptFlag clears bit 0 of Flags 2, which must always hold the true
// state of output stream 2 (S 11.1.2).
func (m *Machine) clearTranscriptFlag() error {
	flags2, err := m.mem.readWord(hdrFlags2)
	if err != nil {
		return err
	}
	return m.mem.writeWord(hdrFlags2, flags2&^flags2Transcript)
}
