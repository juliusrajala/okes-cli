package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Run the test binary itself as a child. This avoids dependencies on Git, Podman,
// a shell, or other installed programs. The marker prevents normal test runs from
// entering helper mode.
func TestProcessHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 || len(os.Args) <= separator+2 || os.Args[separator+1] != "go-sesh-process-helper" {
		return
	}
	status, err := runHelper(os.Args[separator+2], os.Args[separator+3:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(99)
	}
	os.Exit(status)
}

func runHelper(mode string, args []string) (int, error) {
	switch mode {
	case "args":
		return 0, json.NewEncoder(os.Stdout).Encode(args)
	case "env":
		return 0, json.NewEncoder(os.Stdout).Encode(os.Environ())
	case "cwd":
		cwd, err := os.Getwd()
		if err != nil {
			return 0, err
		}
		_, err = io.WriteString(os.Stdout, cwd)
		return 0, err
	case "stdio":
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			return 0, err
		}
		_, err := io.WriteString(os.Stderr, "diagnostic\n")
		return 0, err
	case "exit":
		status, err := strconv.Atoi(args[0])
		if err != nil {
			return 0, err
		}
		if _, err := io.WriteString(os.Stdout, "output\n"); err != nil {
			return 0, err
		}
		_, err = io.WriteString(os.Stderr, "error\n")
		return status, err
	case "split-output":
		if _, err := io.WriteString(os.Stdout, "1234"); err != nil {
			return 0, err
		}
		_, err := io.WriteString(os.Stderr, "abcd")
		return 0, err
	case "overflow":
		var dst io.Writer = os.Stdout
		if args[0] == "stderr" {
			dst = os.Stderr
		}
		chunk := strings.Repeat("x", 4096)
		for {
			if _, err := io.WriteString(dst, chunk); err != nil {
				return 0, err
			}
		}
	case "signal":
		process, err := os.FindProcess(os.Getpid())
		if err != nil {
			return 0, err
		}
		return 0, process.Kill()
	case "block":
		// Wait for an external event without sleeps or a CPU-consuming loop.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		<-ctx.Done()
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown helper mode %q", mode)
	}
}

func helperCommand(t *testing.T, mode string, args ...string) Command {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{
		Path: path,
		Args: append([]string{"-test.run=^TestProcessHelper$", "--", "go-sesh-process-helper", mode}, args...),
	}
}

func TestRunLiteralArgs(t *testing.T) {
	yourArgs := []string{"", "hello world", "$(whoami)"}
	command := helperCommand(t, "args", yourArgs...)

	result, err := Run(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", result.ExitCode)
	}

	var received []string
	err = json.Unmarshal([]byte(result.Stdout), &received)
	if err != nil {
		t.Fatal(err)
	}
	if len(received) != len(yourArgs) {
		t.Fatalf("length mismatch: expected %d, got %d", len(yourArgs), len(received))
	}
	for i, arg := range yourArgs {
		if received[i] != arg {
			t.Fatalf("argument mismatch at index %d: expected %q, got %q", i, arg, received[i])
		}
	}
}

func TestRunEnvironment(t *testing.T) {
	t.Setenv("GO_SESH_PARENT_SECRET", "must-not-reach-child")
	tests := []struct {
		name string
		env  []string
	}{
		{name: "nil means empty"},
		{name: "empty means empty", env: []string{}},
		{name: "only explicit entries", env: []string{"EXPLICIT=yes", "VALUE=a=b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := helperCommand(t, "env")
			command.Env = tt.env
			result, err := Run(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 {
				t.Fatalf("exit code = %d, stderr = %q", result.ExitCode, result.Stderr)
			}
			var got []string
			if err := json.Unmarshal([]byte(result.Stdout), &got); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.env) {
				t.Errorf("child environment = %q, want %q", got, tt.env)
			}
		})
	}
}

func TestRunWorkingDirectory(t *testing.T) {
	command := helperCommand(t, "cwd")
	command.Dir = t.TempDir()
	result, err := Run(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != command.Dir {
		t.Fatalf("result = %+v, want successful cwd %q", result, command.Dir)
	}
}

func TestRunStdio(t *testing.T) {
	tests := []struct {
		name  string
		input io.Reader
		want  string
	}{
		{name: "nil stdin means EOF"},
		{name: "supplied stdin", input: strings.NewReader("hello\n"), want: "hello\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := helperCommand(t, "stdio")
			command.Stdin = tt.input
			result, err := Run(context.Background(), command)
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 || result.Stdout != tt.want || result.Stderr != "diagnostic\n" {
				t.Fatalf("result = %+v, want stdout %q and separate stderr", result, tt.want)
			}
		})
	}
}

func TestRunExitStatus(t *testing.T) {
	for _, status := range []int{0, 1, 42} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			result, err := Run(context.Background(), helperCommand(t, "exit", strconv.Itoa(status)))
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != status || result.Stdout != "output\n" || result.Stderr != "error\n" {
				t.Fatalf("result = %+v, want exit code %d with both output streams", result, status)
			}
		})
	}
}

func TestRunStartFailures(t *testing.T) {
	t.Run("missing executable", func(t *testing.T) {
		result, err := Run(context.Background(), Command{Path: filepath.Join(t.TempDir(), "missing")})
		if !errors.Is(err, os.ErrNotExist) || result.ExitCode != -1 {
			t.Fatalf("result = %+v, error = %v, want ErrNotExist and exit code -1", result, err)
		}
	})
	t.Run("not executable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not-executable")
		if err := os.WriteFile(path, []byte("not a program"), 0o600); err != nil {
			t.Fatal(err)
		}
		result, err := Run(context.Background(), Command{Path: path})
		if !errors.Is(err, os.ErrPermission) || result.ExitCode != -1 {
			t.Fatalf("result = %+v, error = %v, want ErrPermission and exit code -1", result, err)
		}
	})
}

func TestRunRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		command Command
	}{
		{name: "relative executable", command: Command{Path: "git"}},
		{name: "NUL in executable", command: Command{Path: "/bin/gi\x00t"}},
		{name: "NUL in argument", command: Command{Path: "/bin/git", Args: []string{"bad\x00"}}},
		{name: "relative directory", command: Command{Path: "/bin/git", Dir: "relative"}},
		{name: "NUL in directory", command: Command{Path: "/bin/git", Dir: "/bad\x00"}},
		{name: "negative limit", command: Command{Path: "/bin/git", OutputLimit: -1}},
		{name: "missing environment separator", command: Command{Path: "/bin/git", Env: []string{"BAD"}}},
		{name: "empty environment name", command: Command{Path: "/bin/git", Env: []string{"=bad"}}},
		{name: "NUL in environment", command: Command{Path: "/bin/git", Env: []string{"NAME=bad\x00"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Run(context.Background(), tt.command)
			var pathErr *os.PathError
			if err == nil || errors.As(err, &pathErr) || result.ExitCode != -1 {
				t.Fatalf("result = %+v, error = %v, want validation failure before process startup", result, err)
			}
		})
	}
}

func TestRunCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Run(ctx, helperCommand(t, "block"))
	if !errors.Is(err, context.Canceled) || result.ExitCode != -1 {
		t.Fatalf("result = %+v, error = %v, want context.Canceled and exit code -1", result, err)
	}
}

// readerFunc lets tests supply Read behavior without a separate struct.
type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestRunCancelledAfterStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := helperCommand(t, "block")
	// os/exec starts the stdin copy only after it starts the child. This gives
	// the test a deterministic cancellation point without sleeps.
	command.Stdin = readerFunc(func([]byte) (int, error) {
		cancel()
		return 0, io.EOF
	})
	result, err := Run(ctx, command)
	if !errors.Is(err, context.Canceled) || result.ExitCode != -1 {
		t.Fatalf("result = %+v, error = %v, want context.Canceled and a killed child", result, err)
	}
}

func TestRunDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Run(ctx, helperCommand(t, "block"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestRunInputFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	inputErr := errors.New("test input failure")
	command := helperCommand(t, "block")
	command.Stdin = readerFunc(func([]byte) (int, error) { return 0, inputErr })
	_, err := Run(ctx, command)
	if !errors.Is(err, inputErr) {
		t.Fatalf("error = %v, want wrapped input error", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("input failure required the caller's deadline to stop the process: %v", ctx.Err())
	}
}

func TestRunOutputLimitsAreSeparate(t *testing.T) {
	command := helperCommand(t, "split-output")
	command.OutputLimit = 4
	result, err := Run(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != "1234" || result.Stderr != "abcd" {
		t.Fatalf("result = %+v, want both streams to reach their separate limits", result)
	}
}

func TestRunOutputOverflow(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			command := helperCommand(t, "overflow", stream)
			command.OutputLimit = 32
			result, err := Run(ctx, command)
			if !errors.Is(err, ErrOutputLimit) {
				t.Fatalf("error = %v, want ErrOutputLimit", err)
			}
			if ctx.Err() != nil {
				t.Fatalf("overflow required the caller's deadline to stop the process: %v", ctx.Err())
			}
			output, other := result.Stdout, result.Stderr
			if stream == "stderr" {
				output, other = result.Stderr, result.Stdout
			}
			if output != strings.Repeat("x", 32) || other != "" {
				t.Fatalf("output lengths = %d/%d, want 32/0", len(output), len(other))
			}
		})
	}
}

func TestRunDefaultOutputLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := Run(ctx, helperCommand(t, "overflow", "stdout"))
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("error = %v, want ErrOutputLimit", err)
	}
	if len(result.Stdout) != DefaultOutputLimit {
		t.Errorf("captured bytes = %d, want %d", len(result.Stdout), DefaultOutputLimit)
	}
	if ctx.Err() != nil {
		t.Fatalf("overflow required the caller's deadline: %v", ctx.Err())
	}
}

func TestRunSignalIsAnError(t *testing.T) {
	result, err := Run(context.Background(), helperCommand(t, "signal"))
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || result.ExitCode != -1 {
		t.Fatalf("result = %+v, error = %v, want wrapped ExitError and exit code -1", result, err)
	}
}
