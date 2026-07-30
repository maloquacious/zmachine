# How-to guides

Directions for the goals a host embedding this package actually has. Each guide
assumes you know what you want to do and sticks to doing it.

| Guide | For when you want to |
| --- | --- |
| [Persist and restore session state](persist-and-restore-session-state.md) | Store `Result.State` between turns and hand it back so a player resumes. |
| [Handle a cancelled request mid-turn](handle-a-cancelled-request-mid-turn.md) | Do the right thing when a context is cancelled or its deadline passes while the story is running. |
| [Serve many concurrent players from one story](serve-many-concurrent-players-from-one-story.md) | Run many simultaneous sessions of one game in one process. |

New to the package? Start with the [tutorial](../tutorial.md). For why the
package is shaped this way, see the [README](../../README.md); for what each
call, option, field and error is, the [reference](../reference.md).
