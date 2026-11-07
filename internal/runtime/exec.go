package runtime

import (
	"fmt"
	"io"
	"path"
	"strings"
)

// ExecRequest selects literal argv inside an existing container. Workdir is a
// container path, not a host path. Empty Workdir defaults to /workspace.
// Streams belong to the caller. Nil stdin means EOF; nil writers capture only.
// Non-file stdin readers and output writers must not block indefinitely, as
// with process.Command. Context cancellation cannot interrupt arbitrary I/O.
type ExecRequest struct {
	Target  Target
	Command []string
	Workdir string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Validate checks workload input without observing or changing a container.
// Identity and ownership checks remain the engine adapter's responsibility.
func (r ExecRequest) Validate() error {
	if len(r.Command) == 0 || len(r.Command) > 256 || r.Command[0] == "" {
		return fmt.Errorf("exec requires 1 to 256 literal arguments and a nonempty program")
	}
	for _, arg := range r.Command {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("exec arguments must contain no NUL bytes")
		}
	}
	if r.Workdir != "" && (!path.IsAbs(r.Workdir) || strings.ContainsRune(r.Workdir, '\x00')) {
		return fmt.Errorf("exec working directory must be an absolute container path without NUL bytes")
	}
	return nil
}

// ExecResult contains bounded captured output, also when Exec returns an error.
// Ordinary nonzero exit codes are results, not transport failures. ExitCode is
// -1 when no ordinary exit status is available.
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}
