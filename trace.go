package zmachine

// Execution tracing (spec S 30).
//
// Tracing exists to make this engine's behaviour comparable with an
// established interpreter's, instruction by instruction. It is off unless a
// host installs a Tracer, and a Tracer cannot change what the story does: it
// receives copies of the machine's values after the instruction has run, and
// whatever it returns is ignored.

// Tracer receives one event for each instruction the machine executes.
//
// Instruction is called after the instruction has taken effect, so that the
// event can report what it did as well as what it was. An instruction that
// failed produces no event; the error describes it instead.
type Tracer interface {
	Instruction(TraceInstruction)
}

// TraceInstruction describes one executed instruction.
type TraceInstruction struct {
	// PC is the byte address the instruction was decoded from.
	PC uint32
	// Next is the program counter after the instruction ran. It differs from
	// the address after the instruction when a branch, jump, call or return
	// moved it.
	Next uint32
	// Opcode identifies the instruction in the form used by S 14, for example
	// "2OP:20 add".
	Opcode string
	// Operands holds the operand values in the order they were evaluated
	// (S 4.5.2). It is a copy: the tracer may retain it.
	Operands []uint16
	// CallDepth is the number of routines on the call chain before the
	// instruction ran. It is zero in the initial execution environment of
	// S 5.5.
	CallDepth int

	// Stored reports whether the instruction wrote a result, and StoreVariable
	// and StoreValue say where and what (S 4.6).
	Stored        bool
	StoreVariable uint8
	StoreValue    uint16

	// Branched reports whether a branch instruction took its branch (S 4.7).
	// It is false for instructions that do not branch.
	Branched bool

	// Called reports that the instruction entered a routine, and Returned that
	// it left one. ReturnValue is meaningful only when Returned is set
	// (S 6.4, S 6.5).
	Called      bool
	Returned    bool
	ReturnValue uint16
}

// traceState accumulates what the current instruction did. It is only written
// when a Tracer is installed, so an untraced machine pays nothing for it.
type traceState struct {
	stored        bool
	storeVariable uint8
	storeValue    uint16
	branched      bool
	called        bool
	returned      bool
	returnValue   uint16
}

// tracing reports whether events are being collected.
func (m *Machine) tracing() bool { return m.tracer != nil }

// beginTrace clears the accumulated event at the start of an instruction.
func (m *Machine) beginTrace() { m.trace = traceState{} }

// emitTrace delivers the event for an instruction that completed.
func (m *Machine) emitTrace(inst *instruction, pc uint32, depth int, ops []uint16) {
	event := TraceInstruction{
		PC:            pc,
		Next:          m.pc,
		Opcode:        inst.op.String(),
		CallDepth:     depth,
		Stored:        m.trace.stored,
		StoreVariable: m.trace.storeVariable,
		StoreValue:    m.trace.storeValue,
		Branched:      m.trace.branched,
		Called:        m.trace.called,
		Returned:      m.trace.returned,
		ReturnValue:   m.trace.returnValue,
	}
	if len(ops) != 0 {
		// The machine reuses its operand scratch space, so the tracer gets a
		// copy it can keep without observing later instructions through it.
		event.Operands = append([]uint16(nil), ops...)
	}
	m.tracer.Instruction(event)
}
