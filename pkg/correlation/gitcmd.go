package correlation

import (
	"context"
	"os/exec"
)

// gitCommand returns an exec.Cmd for the git binary bound to ctx (issue #166).
// When ctx is cancelled — for example when the --robot-triage history
// prologue's timeout fires — any in-flight git subprocess is killed instead of
// leaking unbounded work. A nil ctx degrades to context.Background() (no
// cancellation), preserving the behavior of legacy constructors that never
// attach a context.
func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return exec.CommandContext(ctx, "git", args...)
}
