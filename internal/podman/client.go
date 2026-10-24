// Package podman implements local rootless container operations through literal
// Podman CLI argv. It does not expose or connect to a container-engine socket.
package podman

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go-sesh/internal/process"
)

var (
	// ErrCommandFailed means Podman returned an unexpected nonzero exit status.
	ErrCommandFailed = errors.New("Podman command failed")
	// ErrUnsupportedHost means required local rootless capabilities are unavailable.
	ErrUnsupportedHost = errors.New("unsupported Podman host")
	// ErrInvalidResponse means Podman output could not be safely decoded.
	ErrInvalidResponse = errors.New("invalid Podman response")
)

// Runner is the subprocess boundary. A fake runner can test Podman behavior
// without invoking an engine. Production uses process.Run.
type Runner func(context.Context, process.Command) (process.Result, error)

// Options selects trusted host settings, not project configuration.
type Options struct {
	// Executable must be absolute when set. Otherwise New searches a filtered PATH.
	Executable string
	// SourceEnv is filtered before use. Nil reads os.Environ; an empty slice does not.
	SourceEnv []string
	// ProjectDir excludes project-controlled executable paths during PATH filtering.
	ProjectDir string
	Runner     Runner
}

// Client owns an executable path and an explicit restricted host environment.
// It does not retain a context or start a background process.
type Client struct {
	executable string
	env        []string
	runner     Runner
}

// New prepares the local CLI boundary without running Podman. Host capabilities
// are checked by Preflight before inspecting a container or probing an image.
func New(options Options) (*Client, error) {
	source := options.SourceEnv
	if source == nil {
		source = os.Environ()
	}
	env, searchPath, project, err := buildHostEnvironment(source, options.ProjectDir)
	if err != nil {
		return nil, err
	}
	executable := options.Executable
	if executable != "" {
		// An explicit executable is a trusted caller choice, not a project field.
		// Do not search the current directory to resolve a relative selection.
		if !filepath.IsAbs(executable) || strings.ContainsRune(executable, '\x00') {
			return nil, fmt.Errorf("Podman executable must be an absolute path without NUL bytes")
		}
	} else {
		executable, err = resolveExecutable(searchPath, project)
		if err != nil {
			return nil, err
		}
	}
	runner := options.Runner
	if runner == nil {
		runner = process.Run
	}
	return &Client{executable: executable, env: env, runner: runner}, nil
}

// invoke enforces local execution but leaves exit-status meaning to the caller.
// For example, an exists query uses status 1 for absence, not command failure.
func (c *Client) invoke(ctx context.Context, args ...string) (process.Result, error) {
	if err := ctx.Err(); err != nil {
		return process.Result{ExitCode: -1}, err
	}
	// Force local mode on every call, not only at construction. Each call owns
	// its argument/environment slices so runner changes cannot affect later calls
	// or the caller's arguments. An explicit environment prevents host inheritance.
	result, err := c.runner(ctx, process.Command{
		Path: c.executable,
		Args: append([]string{"--remote=false"}, args...),
		Env:  append([]string{}, c.env...),
	})
	if err != nil {
		return result, fmt.Errorf("invoke Podman: %w", err)
	}
	return result, nil
}

// checked is for commands whose only successful status is zero. Existence
// queries must use invoke and interpret their documented statuses themselves.
func (c *Client) checked(ctx context.Context, args ...string) (process.Result, error) {
	result, err := c.invoke(ctx, args...)
	if err != nil {
		return result, err
	}
	if result.ExitCode != 0 {
		return result, fmt.Errorf("%w: %s exited with status %d", ErrCommandFailed, args[0], result.ExitCode)
	}
	return result, nil
}
