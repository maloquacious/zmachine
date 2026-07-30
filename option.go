package zmachine

import (
	"fmt"
	"log/slog"
)

// Option configures a Machine. Options are applied in order by New, and an
// option that cannot be satisfied makes New fail rather than silently leaving
// the machine misconfigured.
type Option func(*config) error

// config is the settled configuration an Option writes into.
type config struct {
	logger           *slog.Logger
	tracer           Tracer
	seed             uint64
	hasSeed          bool
	instructionLimit uint64
}

// defaultConfig is the configuration of a Machine created with no options: no
// tracing, diagnostics discarded, an unpredictable random seed, and an
// instruction limit suitable for a server.
func defaultConfig() config {
	return config{
		logger:           slog.New(slog.DiscardHandler),
		instructionLimit: defaultInstructionLimit,
	}
}

// WithLogger sets the logger the machine writes diagnostics to.
//
// The logger is used for interpreter diagnostics only. Story output is never
// written to it, and logging never changes execution semantics. A Machine
// created without this option discards its diagnostics; it never falls back to
// slog.Default.
func WithLogger(logger *slog.Logger) Option {
	return func(c *config) error {
		if logger == nil {
			return fmt.Errorf("zmachine: WithLogger: logger is nil")
		}
		c.logger = logger
		return nil
	}
}

// WithRandomSeed seeds the machine's random number generator, making execution
// reproducible (spec S 21).
//
// The generator starts in the "predictable" state of S 2.4.2: two machines
// given the same story and the same seed produce the same sequence of random
// numbers. Without this option the generator is seeded unpredictably, which is
// the "random" state S 2.4 requires at the start of a game.
//
// The story can still change the state itself: random with a negative range
// reseeds the generator, and random with a range of zero reseeds it
// unpredictably (S 15, random).
func WithRandomSeed(seed uint64) Option {
	return func(c *config) error {
		c.seed = seed
		c.hasSeed = true
		return nil
	}
}

// WithInstructionLimit bounds the number of instructions one call to Start or
// Run may execute (spec S 25).
//
// Reaching the limit stops execution with an error wrapping ErrExecutionLimit.
// The limit applies to each call separately, so a story that legitimately runs
// for a long time is not penalised for having done so on an earlier turn. The
// limit must be positive: a machine with no limit at all cannot be made safe
// for a server, because a story may loop forever without executing an illegal
// instruction.
func WithInstructionLimit(limit uint64) Option {
	return func(c *config) error {
		if limit == 0 {
			return fmt.Errorf("zmachine: WithInstructionLimit: limit must be positive")
		}
		c.instructionLimit = limit
		return nil
	}
}

// WithTracer installs a Tracer, which receives one event per executed
// instruction (spec S 30).
//
// Tracing is off unless this option is given, and a Tracer cannot change what
// the story does: it is handed copies of the machine's values and its return
// is ignored.
func WithTracer(tracer Tracer) Option {
	return func(c *config) error {
		if tracer == nil {
			return fmt.Errorf("zmachine: WithTracer: tracer is nil")
		}
		c.tracer = tracer
		return nil
	}
}
