# Changelog

All notable changes to this project are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and
this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

The version constant in `version.go` is the only statement of the release in the
source, and the `vX.Y.Z` git tag is the only one Go's module resolution can see;
the two are maintained together.

## [Unreleased]

## [0.1.4] — 2026-07-30

### Added

- `testdata/local/`, holding the Lost Treasures of Infocom edition of Zork I, II
  and III. These are commercial files with no redistribution licence, so only
  `README.md` and `fetch.sh` are committed and the story files themselves are
  gitignored, the way `references/` already treats what it fetches.
- Tests over that second edition: that all three are Version 3 with the expected
  release, serial and checksum; that each plays across a request boundary; and
  that a saved state from Zork I release 119 is refused by a machine built from
  release 88. The last is the story-identity rule of the compatibility policy,
  proved against two real editions of one game rather than a constructed pair.
- The README states the minimum Go version and why it is pinned to a patch
  release, and states that the exported API may change in a minor release before
  `v1.0.0`.

## [0.1.3] — 2026-07-30

Documentation only. No behaviour changed; the two `.go` files touched carry
comment changes alone.

### Added

- `CHANGELOG.md`, this file.
- `docs/reference.md`, a reference description of the host-facing API: the
  lifecycle calls, every option, every field of `Result` and `StatusLine`, the
  error taxonomy, concurrency, limits, and what is deliberately not implemented.
- A Go Reference badge and a documentation index in the README.

### Changed

- The README is now the front door — what the package is, why it is shaped the
  way it is, and how to start — and defers per-symbol description to
  `docs/reference.md` and to `go doc`.
- `Machine.Restore`'s documentation described where a foreign save resumes by
  naming an unexported function, which a reader of the public documentation
  cannot see. It now describes the behaviour.

### Fixed

- The sentinel errors' documentation claimed that *every* error returned by the
  package wraps one of them. Two kinds do not, and a host that believed the
  claim would mishandle both: a cancelled context is reported as the context's
  own error so `errors.Is` finds `context.Canceled`, and a nil context or a
  malformed option reports a mistake at the call site as a plain error. Both are
  now documented, here and in `docs/reference.md`.

## [0.1.2] — 2026-07-30

### Added

- `TestFrotzMatchesDfrotzGoldenSequence` pins the first 1,000 results of the
  Frotz-compatible generator seeded at 1234 to a SHA-256 digest taken from
  Homebrew's Frotz 2.55, not from this package. The differential tests are only
  meaningful while this engine draws the numbers dfrotz draws, and nothing
  previously guarded the sequence itself.
- `TestFrotzGoldenNormalizationIsLossless` checks the assumption the digest
  rests on: recovering each result as `printed - 1` inverts `result % 32767 + 1`
  for every result except 32767, which collides with 0.
- `docs/prng-history.md` records the derivation of the digest — source
  provenance, the probe story and its checksums, the capture commands, the
  normalization, and the independence argument — so the constant is not
  mistaken for one generated from the code it checks.

No exported behaviour changed.

## [0.1.1] — 2026-07-30

### Changed

- `github.com/maloquacious/quetzal` moved from a commit-pinned pseudo-version to
  the tagged `v0.2.2`. The upgrade is behaviourally empty: the intervening
  upstream changes were a changelog, a tutorial, README and agent guidance, its
  own version bump, and one doc comment recording that
  `IgnoreInterpreterHeader`'s ranges are the union across Versions.
- `go mod tidy` dropped two stale pseudo-version entries from `go.sum`.
- The `version` constant's documentation claimed this repository has no tags,
  which stopped being true at `v0.1.0`.

## [0.1.0] — 2026-07-30

First tagged release: a headless Z-machine Version 3 execution engine.

### Added

- **Story loading and validation.** `LoadStory` validates a Version 3 story
  file, checking every header address and table extent before use, and returns
  an immutable `Story` safe for concurrent use by any number of machines.
- **Text.** Version 3 Z-string and ZSCII decoding, including abbreviations.
- **Instruction decoding.** The full Version 3 instruction set, decoded
  separately from execution so that a decoded instruction retains the context a
  fault message needs.
- **Execution.** `Machine`, the execution loop, and the Version 3 opcodes, with
  each machine owning its own dynamic memory, evaluation stack, call chain and
  program counter.
- **Object model, dictionary and input.** The Version 3 object tree, properties
  and attributes; dictionary lookup and tokenization; and line input handled as
  a suspension boundary rather than a blocking read.
- **The request lifecycle.** `Start`, `Run` and `Restore`, with `Result`
  carrying the story's output, the upper window, the status line, the resumable
  state and the reason execution stopped. Creating a machine, restoring, running
  one command and discarding the machine is observably identical to keeping one
  machine alive for the whole game.
- **State.** Resumable state in the Quetzal format through
  `github.com/maloquacious/quetzal`, treated as untrusted input on the way back
  in.
- **Options.** `WithLogger`, `WithRandomSeed`, `WithInstructionLimit`,
  `WithTracer` and `WithFrotzRandomSeed`.
- **Error taxonomy.** `ErrInvalidStory`, `ErrInvalidState`, `ErrInvalidOpcode`,
  `ErrMemoryAccess`, `ErrInvalidText`, `ErrExecutionLimit` and
  `ErrExecutionFault`, with typed errors carrying the program counter, opcode
  and address.
- **Differential tests against Frotz** in three layers — transcript, status
  line, and the state of play compared against a save Frotz wrote — run from
  committed fixtures, with `ZMACHINE_REQUIRE_DFROTZ` turning a missing `dfrotz`
  into a failure rather than a silent skip.
- **`Version()`**, which reports this package's release and deliberately claims
  no standard revision.

[Unreleased]: https://github.com/maloquacious/zmachine/compare/v0.1.4...HEAD
[0.1.4]: https://github.com/maloquacious/zmachine/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/maloquacious/zmachine/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/maloquacious/zmachine/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/maloquacious/zmachine/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/maloquacious/zmachine/releases/tag/v0.1.0
