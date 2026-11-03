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
	Resolve(context.Context, string) (session.ID, error)
	Close() error
}

// Options supplies process-owned dependencies. Open receives absolute state and
// project paths only after parsing succeeds. Run closes a successfully opened
// backend, but borrows Stdout/Stderr. Environment is used for state selection;
// startup separately filters it for the host engine process.
type Options struct {
	Stdout      io.Writer
	Stderr      io.Writer
	Environment []string
	Getwd       func() (string, error)
	Open        func(stateDir, projectDir string) (Backend, error)
}

// Run executes a command and returns its process status. It prints errors once
// to Stderr. Statuses are 0 for success/help, 2 for usage, 130 for cancellation,
// and 1 for other failures. No help or parse-error path opens a backend.
func Run(ctx context.Context, args []string, options Options) int {
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	err := run(ctx, args, options)
	if err == nil {
		return 0
	}
	code := 1
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

func run(ctx context.Context, args []string, options Options) (err error) {
	cmd, err := parse(args)
	if err != nil {
		return err
	}
	if cmd.help {
		_, err := io.WriteString(options.Stdout, helpText)
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if options.Getwd == nil || options.Open == nil {
		return fmt.Errorf("CLI startup dependencies are missing")
	}
	cwd, err := options.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	if !filepath.IsAbs(cwd) {
		return fmt.Errorf("working directory must be absolute")
	}
	stateDir := cmd.stateDir
	if stateDir == "" {
		stateDir, err = defaultStateDir(options.Environment)
		if err != nil {
			return err
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
		return err
	}
	if backend == nil {
		return fmt.Errorf("CLI backend is missing")
	}
	defer func() { err = errors.Join(err, backend.Close()) }()
	return dispatch(ctx, cmd, backend, options.Stdout)
}

func dispatch(ctx context.Context, cmd command, backend Backend, writer io.Writer) error {
	if cmd.name == "start" {
		view, err := backend.Start(ctx, cmd.start)
		if err != nil {
			return err
		}
		return writeSession(writer, view, cmd.json)
	}
	if cmd.name == "list" {
		views, err := backend.List(ctx)
		if err != nil {
			return err
		}
		if cmd.json {
			outputs := make([]sessionOutput, 0, len(views))
			for _, view := range views {
				outputs = append(outputs, outputFor(view))
			}
			return writeJSON(writer, outputs)
		}
		return writeList(writer, views)
	}
	id := cmd.id
	if id == "" {
		var err error
		id, err = backend.Resolve(ctx, cmd.selector)
		if err != nil {
			return err
		}
	}
	switch cmd.name {
	case "inspect":
		view, err := backend.Inspect(ctx, id)
		if err != nil {
			return err
		}
		return writeJSON(writer, outputFor(view))
	case "stop":
		view, err := backend.Stop(ctx, id)
		if err != nil {
			return err
		}
		return writeSession(writer, view, cmd.json)
	case "remove":
		if err := backend.Remove(ctx, id); err != nil {
			return err
		}
		if cmd.json {
			return writeJSON(writer, struct {
				ID      session.ID `json:"id"`
				Removed bool       `json:"removed"`
			}{ID: id, Removed: true})
		}
		selector := cmd.selector
		if selector == "" {
			selector = string(id)
		}
		_, err := fmt.Fprintf(writer, "Removed session %s\n", selector)
		return err
	default:
		return usage("unknown command %q", cmd.name)
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

Flags go before positional arguments. --state-dir and --json also work after
COMMAND, before its positional arguments. inspect always writes JSON.

Only detached mount sessions are implemented. No project configuration is loaded.
TTY, copy, exec, logs, and attachment are not implemented. Images must exist locally.
Networking defaults to none. The workspace is writable; removal preserves it.

Default state: $XDG_DATA_HOME/go-sesh or $HOME/.local/share/go-sesh.
TypeScript pi-sesh state is separate. Legacy records are not imported or migrated.
`
