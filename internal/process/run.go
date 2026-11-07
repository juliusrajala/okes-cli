package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultOutputLimit is the maximum captured byte count per output stream unless
// Command.OutputLimit specifies another limit.
const DefaultOutputLimit = 16 * 1024 * 1024

// Command describes a host subprocess, not an in-container workload.
// The caller must resolve Path and choose which environment variables to pass.
type Command struct {
	Path string
	Args []string
	// Dir defaults to the caller's working directory. If set, it must be absolute.
	Dir string
	// Env contains KEY=value entries. Nil and empty both mean no environment.
	Env []string
	// Stdin defaults to EOF. A supplied non-file reader must not block
	// indefinitely; cancellation cannot interrupt an arbitrary Read method.
	// An *os.File is inherited directly without a parent input-copy goroutine.
	Stdin io.Reader
	// Stdout and Stderr optionally receive live output in addition to capture.
	// Run borrows these writers. It serializes their Write calls because both
	// streams may share a writer. A write failure cancels the host process.
	// Writers must not block indefinitely; cancellation cannot interrupt an
	// arbitrary writer's Write method.
	Stdout io.Writer
	Stderr io.Writer
	// OutputLimit applies separately to stdout and stderr. Zero uses
	// DefaultOutputLimit. Negative values are invalid.
	OutputLimit int
}

// Result contains a subprocess's exit status and captured output, including
// partial output on failure. ExitCode is -1 if no ordinary exit code is available.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Run executes literal argv without a shell. Ordinary nonzero exit codes are
// results, not errors. Start failures, signals, cancellation, input failures, and
// output overflow are errors. Wrapped errors support errors.Is and errors.As.
//
// Cancellation kills the direct host process. It does not prove that descendants
// or an in-container workload stopped; session code must verify that separately.
func Run(ctx context.Context, command Command) (Result, error) {
	result := Result{ExitCode: -1}
	if err := validateCommand(command.Path, command.Args); err != nil {
		return result, fmt.Errorf("validate command: %w", err)
	}
	if command.Dir != "" && (!filepath.IsAbs(command.Dir) || strings.ContainsRune(command.Dir, '\x00')) {
		return result, fmt.Errorf("working directory must be an absolute path without NUL bytes")
	}
	for i, entry := range command.Env {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || strings.ContainsRune(entry, '\x00') {
			return result, fmt.Errorf("invalid environment entry at index %d", i)
		}
	}
	if command.OutputLimit < 0 {
		return result, fmt.Errorf("output limit must not be negative")
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("run %q: %w", command.Path, err)
	}

	limit := command.OutputLimit
	if limit == 0 {
		limit = DefaultOutputLimit
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := capture{limit: limit, onOverflow: cancel}
	stderr := capture{limit: limit, onOverflow: cancel}
	cmd := exec.CommandContext(runCtx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	// A non-nil empty slice prevents os/exec from inheriting the host environment.
	cmd.Env = append([]string{}, command.Env...)
	input := inputReader{source: command.Stdin, cancel: cancel}
	if file, ok := command.Stdin.(*os.File); ok {
		// Inherit the descriptor without a copy goroutine blocked on terminal
		// input. The child closes its descriptor; the caller retains its own.
		cmd.Stdin = file
	} else if command.Stdin != nil {
		cmd.Stdin = &input
	}
	var outputMu sync.Mutex
	out := outputWriter{capture: &stdout, destination: command.Stdout, mu: &outputMu, cancel: cancel}
	diagnostic := outputWriter{capture: &stderr, destination: command.Stderr, mu: &outputMu, cancel: cancel}
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	// Descendants can keep output pipes open after the direct process exits.
	// Bound that wait rather than leaving the caller blocked on those pipes.
	cmd.WaitDelay = time.Second

	err := cmd.Run()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	result.Stdout = string(stdout.data)
	result.Stderr = string(stderr.data)

	if stdout.exceeded || stderr.exceeded {
		return result, fmt.Errorf("run %q: %w", command.Path, ErrOutputLimit)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, fmt.Errorf("run %q: %w", command.Path, ctxErr)
	}
	if out.err != nil || diagnostic.err != nil {
		return result, fmt.Errorf("write output for %q: %w", command.Path, errors.Join(out.err, diagnostic.err))
	}
	if input.err != nil {
		return result, fmt.Errorf("read stdin for %q: %w", command.Path, input.err)
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() >= 0 {
			return result, nil
		}
		return result, fmt.Errorf("run %q: %w", command.Path, err)
	}
	return result, nil
}

// Stop the host process when its input fails, even if it ignores stdin closing.
// os/exec uses one input-copy goroutine; inspect err only after cmd.Run returns.
type inputReader struct {
	source io.Reader
	cancel context.CancelFunc
	err    error
}

func (r *inputReader) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
		r.cancel()
	}
	return n, err
}
