// Package cli parses sesh commands and renders results. Session policy and
// cleanup remain in the session manager, not in command handlers.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"go-sesh/internal/session"
)

// Backend is the application capability consumed by command dispatch. Production
// promotes Manager methods and adds store lookup/closure; tests need no Podman.
type Backend interface {
	Start(context.Context, session.StartRequest) (session.View, error)
	Inspect(context.Context, session.ID) (session.View, error)
	List(context.Context) ([]session.View, error)
	Stop(context.Context, session.ID) (session.View, error)
	Remove(context.Context, session.ID) error
	Exec(context.Context, session.ExecRequest) (session.ExecResult, error)
	Resolve(context.Context, string) (session.ID, error)
	Close() error
}

// Options supplies process-owned dependencies. Open receives absolute state and
// project paths only after parsing succeeds. Run closes a successfully opened
// backend, but borrows streams. Environment is used for state selection;
// startup separately filters it for the host engine process.
type Options struct {
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Environment []string
	Getwd       func() (string, error)
	Open        func(stateDir, projectDir string) (Backend, error)
}

// Run executes a command and returns its process status. Ordinary exec exits
// retain the workload status without printing an extra diagnostic. Other errors
// produce 2 for usage, 130 for cancellation, and 1 for other failures.
// No help or parse-error path opens a backend.
func Run(ctx context.Context, args []string, options Options) int {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	code, err := run(ctx, args, options)
	if err == nil {
		return code
	}
	code = 1
	var invalid *usageError
	if errors.As(err, &invalid) {
		code = 2
	} else if errors.Is(err, context.Canceled) {
		code = 130
	}
	if _, writeErr := fmt.Fprintf(options.Stderr, "sesh: %v\n", err); writeErr != nil {
		return 1
	}
	return code
}

func run(ctx context.Context, args []string, options Options) (code int, err error) {
	cmd, err := parse(args)
	if err != nil {
		return 0, err
	}
	if cmd.help {
		_, err := io.WriteString(options.Stdout, helpText)
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if options.Getwd == nil || options.Open == nil {
		return 0, fmt.Errorf("CLI startup dependencies are missing")
	}
	cwd, err := options.Getwd()
	if err != nil {
		return 0, fmt.Errorf("get working directory: %w", err)
	}
	if !filepath.IsAbs(cwd) {
		return 0, fmt.Errorf("working directory must be absolute")
	}
	stateDir := cmd.stateDir
	if stateDir == "" {
		stateDir, err = defaultStateDir(options.Environment)
		if err != nil {
			return 0, err
		}
	}
	if !filepath.IsAbs(stateDir) {
		stateDir = filepath.Join(cwd, stateDir)
	}
	projectDir := cwd
	if cmd.name == "start" {
		if !filepath.IsAbs(cmd.start.WorkspacePath) {
			cmd.start.WorkspacePath = filepath.Join(cwd, cmd.start.WorkspacePath)
		}
		projectDir = cmd.start.WorkspacePath
	}
	backend, err := options.Open(filepath.Clean(stateDir), projectDir)
	if err != nil {
		return 0, err
	}
	if backend == nil {
		return 0, fmt.Errorf("CLI backend is missing")
	}
	defer func() { err = errors.Join(err, backend.Close()) }()
	return dispatch(ctx, cmd, backend, options)
}

func dispatch(ctx context.Context, cmd command, backend Backend, options Options) (int, error) {
	writer := options.Stdout
	if cmd.name == "start" {
		view, err := backend.Start(ctx, cmd.start)
		if err != nil {
			return 0, err
		}
		return 0, writeSession(writer, view, cmd.json)
	}
	if cmd.name == "exec" {
		cmd.exec.Stdin = options.Stdin
		if !cmd.json {
			cmd.exec.Stdout, cmd.exec.Stderr = writer, options.Stderr
		}
		result, err := backend.Exec(ctx, cmd.exec)
		if err != nil {
			return 0, err
		}
		if result.ExitCode < 0 || result.ExitCode > 255 {
			return 0, fmt.Errorf("exec returned no valid workload exit status")
		}
		if cmd.json {
			if err := writeJSON(writer, executionOutput{
				SessionID: cmd.exec.SessionID, ExecutionID: result.Receipt.ID,
				ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr,
			}); err != nil {
				return 0, err
			}
		}
		return result.ExitCode, nil
	}
	if cmd.name == "list" {
		views, err := backend.List(ctx)
		if err != nil {
			return 0, err
		}
		if cmd.json {
			outputs := make([]sessionOutput, 0, len(views))
			for _, view := range views {
				outputs = append(outputs, outputFor(view))
			}
			return 0, writeJSON(writer, outputs)
		}
		return 0, writeList(writer, views)
	}
	id := cmd.id
	if id == "" {
		var err error
		id, err = backend.Resolve(ctx, cmd.selector)
		if err != nil {
			return 0, err
		}
	}
	switch cmd.name {
	case "inspect":
		view, err := backend.Inspect(ctx, id)
		if err != nil {
			return 0, err
		}
		return 0, writeJSON(writer, outputFor(view))
	case "stop":
		view, err := backend.Stop(ctx, id)
		if err != nil {
			return 0, err
		}
		return 0, writeSession(writer, view, cmd.json)
	case "remove":
		if err := backend.Remove(ctx, id); err != nil {
			return 0, err
		}
		if cmd.json {
			return 0, writeJSON(writer, struct {
				ID      session.ID `json:"id"`
				Removed bool       `json:"removed"`
			}{ID: id, Removed: true})
		}
		selector := cmd.selector
		if selector == "" {
			selector = string(id)
		}
		_, err := fmt.Fprintf(writer, "Removed session %s\n", selector)
		return 0, err
	default:
		return 0, usage("unknown command %q", cmd.name)
	}
}

const helpText = `sesh - local rootless Podman sessions

Usage:
  sesh [--state-dir DIR] [--json] COMMAND [flags] [arguments]

Commands:
  start --detach --name NAME --image IMAGE [--network none|pasta]
        [--workspace mount] [--env NAME=VALUE ...] DIRECTORY -- PROGRAM [ARG ...]
  inspect NAME | --id UUID
  list
  stop NAME | --id UUID
  remove NAME | --id UUID
  exec --session-id UUID [--cwd /workspace] [--timeout SECONDS] NAME -- PROGRAM [ARG ...]

Flags go before positional arguments. --state-dir and --json also work after
COMMAND, before its positional arguments. inspect always writes JSON.

Only detached mount sessions are operational. No project configuration is loaded.
TTY, copy, logs, and attachment are not implemented. Images must exist locally.
Networking defaults to none. The workspace is writable; removal preserves it.

Default state: $XDG_DATA_HOME/go-sesh or $HOME/.local/share/go-sesh.
TypeScript pi-sesh state is separate. Legacy records are not imported or migrated.
`
