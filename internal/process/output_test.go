package process

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type outputFunc func([]byte) (int, error)

func (f outputFunc) Write(p []byte) (int, error) { return f(p) }

func TestRunLiveOutput(t *testing.T) {
	command := helperCommand(t, "stdio")
	var out, diagnostic bytes.Buffer
	command.Stdin = strings.NewReader("literal $HOME\n")
	command.Stdout, command.Stderr = &out, &diagnostic
	result, err := Run(context.Background(), command)
	if err != nil || result.Stdout != out.String() || result.Stderr != diagnostic.String() || out.String() != "literal $HOME\n" || diagnostic.String() != "diagnostic\n" {
		t.Fatalf("live output = %+v, %v; streams=%q, %q", result, err, out.String(), diagnostic.String())
	}
}

func TestRunOutputIsLive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := helperCommand(t, "overflow", "stdout")
	command.Stdout = outputFunc(func(p []byte) (int, error) {
		cancel()
		return len(p), nil
	})
	if _, err := Run(ctx, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("live output did not cancel the still-running child: %v", err)
	}
}

func TestRunLiveOutputLimit(t *testing.T) {
	command := helperCommand(t, "overflow", "stdout")
	command.OutputLimit = 32
	var out bytes.Buffer
	command.Stdout = &out
	result, err := Run(context.Background(), command)
	if !errors.Is(err, ErrOutputLimit) || out.Len() != 32 || result.Stdout != out.String() {
		t.Fatalf("bounded live output = %q, captured=%q, error=%v", out.String(), result.Stdout, err)
	}
}

func TestRunSharedOutputWriter(t *testing.T) {
	command := helperCommand(t, "split-output")
	var shared bytes.Buffer
	command.Stdout, command.Stderr = &shared, &shared
	result, err := Run(context.Background(), command)
	if err != nil || shared.Len() != 8 || !strings.Contains(shared.String(), "1234") || !strings.Contains(shared.String(), "abcd") || result.Stdout != "1234" || result.Stderr != "abcd" {
		t.Fatalf("shared output = %q, %+v, %v", shared.String(), result, err)
	}
}

func TestRunOutputFailureCancelsChild(t *testing.T) {
	cause := errors.New("closed destination")
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) {
			command := helperCommand(t, "overflow", stream)
			writer := outputFunc(func([]byte) (int, error) { return 0, cause })
			if stream == "stdout" {
				command.Stdout = writer
			} else {
				command.Stderr = writer
			}
			_, err := Run(context.Background(), command)
			if !errors.Is(err, cause) {
				t.Fatalf("output failure = %v, want %v", err, cause)
			}
		})
	}
}

func TestRunShortOutputWrite(t *testing.T) {
	command := helperCommand(t, "overflow", "stdout")
	command.Stdout = outputFunc(func(p []byte) (int, error) { return len(p) - 1, nil })
	if _, err := Run(context.Background(), command); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write = %v", err)
	}
}

func TestRunFileStdinIsBorrowed(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := io.WriteString(file, "file input"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	command := helperCommand(t, "stdio")
	command.Stdin = file
	result, err := Run(context.Background(), command)
	if err != nil || result.Stdout != "file input" {
		t.Fatalf("file stdin = %+v, %v", result, err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatalf("Run closed caller stdin: %v", err)
	}
}
