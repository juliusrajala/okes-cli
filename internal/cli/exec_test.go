package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"go-sesh/internal/runtime"
	"go-sesh/internal/session"
)

func execArgs() []string {
	return []string{"exec", "--session-id", string(testID)}
}

func TestParseExec(t *testing.T) {
	args := append(execArgs(), "--cwd", "/tmp", "--timeout", "2.5", "demo", "--", "printf", "$HOME; literal", "", "--json")
	cmd, err := parse(args)
	if err != nil || cmd.exec.Name != "demo" || cmd.exec.SessionID != testID || cmd.exec.Workdir != "/tmp" || cmd.exec.Timeout != 2500*time.Millisecond ||
		!slices.Equal(cmd.exec.Command, []string{"printf", "$HOME; literal", "", "--json"}) || cmd.json {
		t.Fatalf("parsed exec = %+v, %v", cmd, err)
	}
}

func TestExecUsageDoesNotOpenBackend(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "missing UUID", args: []string{"exec", "demo", "--", "true"}},
		{name: "missing name", args: append(execArgs(), "--", "true")},
		{name: "missing separator", args: append(execArgs(), "demo", "true")},
		{name: "missing workload", args: append(execArgs(), "demo", "--")},
		{name: "empty program", args: append(execArgs(), "demo", "--", "")},
		{name: "NUL argument", args: append(execArgs(), "demo", "--", "true", "bad\x00")},
		{name: "relative container cwd", args: append(execArgs(), "--cwd", "relative", "demo", "--", "true")},
		{name: "zero timeout", args: append(execArgs(), "--timeout", "0", "demo", "--", "true")},
		{name: "negative timeout", args: append(execArgs(), "--timeout", "-1", "demo", "--", "true")},
		{name: "large timeout", args: append(execArgs(), "--timeout", "86401", "demo", "--", "true")},
		{name: "NaN timeout", args: append(execArgs(), "--timeout", "NaN", "demo", "--", "true")},
		{name: "infinite timeout", args: append(execArgs(), "--timeout", "+Inf", "demo", "--", "true")},
		{name: "rounded zero timeout", args: append(execArgs(), "--timeout", "0.000000000001", "demo", "--", "true")},
		{name: "too many args", args: append(append(execArgs(), "demo", "--", "true"), make([]string, 256)...)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, diagnostic bytes.Buffer
			options := Options{Stdout: &out, Stderr: &diagnostic, Open: func(string, string) (Backend, error) {
				t.Fatal("invalid exec opened backend")
				return nil, nil
			}}
			if code := Run(context.Background(), tt.args, options); code != 2 {
				t.Fatalf("invalid exec exit=%d, stderr=%q", code, diagnostic.String())
			}
		})
	}
}

func TestRunExecStreamsAndExitStatus(t *testing.T) {
	backend := &fakeBackend{}
	input := strings.NewReader("input")
	backend.execFunc = func(_ context.Context, request session.ExecRequest) (session.ExecResult, error) {
		if request.Stdin != input {
			t.Fatal("CLI changed borrowed stdin")
		}
		if _, err := io.Copy(request.Stdout, request.Stdin); err != nil {
			return session.ExecResult{}, err
		}
		if _, err := io.WriteString(request.Stderr, "diagnostic"); err != nil {
			return session.ExecResult{}, err
		}
		return session.ExecResult{ExecResult: runtime.ExecResult{ExitCode: 23}}, nil
	}
	var out, diagnostic bytes.Buffer
	options := fakeOptions(backend, &out, &diagnostic)
	options.Stdin = input
	code := Run(context.Background(), append(execArgs(), "demo", "--", "program", ""), options)
	if code != 23 || out.String() != "input" || diagnostic.String() != "diagnostic" || backend.closed != 1 || !slices.Equal(backend.calls, []string{"exec"}) {
		t.Fatalf("exec exit=%d, stdout=%q, stderr=%q, calls=%v, close=%d", code, out.String(), diagnostic.String(), backend.calls, backend.closed)
	}
	if backend.execRequest.SessionID != testID || backend.execRequest.Name != "demo" || backend.execRequest.Workdir != "/workspace" {
		t.Fatalf("CLI lost explicit identity: %+v", backend.execRequest)
	}
}

func TestRunExecJSON(t *testing.T) {
	backend := &fakeBackend{execResult: session.ExecResult{
		ExecResult: runtime.ExecResult{ExitCode: 23, Stdout: "output", Stderr: "diagnostic"},
		Receipt:    session.ExecutionReceipt{ID: "3b68db01-63c8-4be5-a565-850700bb00ae"},
	}}
	var out, diagnostic bytes.Buffer
	code := Run(context.Background(), append(execArgs(), "--json", "demo", "--", "program"), fakeOptions(backend, &out, &diagnostic))
	var result executionOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || code != 23 || result.Stdout != "output" || result.Stderr != "diagnostic" || result.ExecutionID != backend.execResult.Receipt.ID || result.SessionID != testID || diagnostic.Len() != 0 {
		t.Fatalf("JSON exec exit=%d, output=%q, stderr=%q, decode=%v", code, out.String(), diagnostic.String(), err)
	}
	if backend.execRequest.Stdout != nil || backend.execRequest.Stderr != nil {
		t.Fatal("JSON mode would mix live output with its JSON object")
	}
}

func TestRunExecFailuresCloseBackend(t *testing.T) {
	for _, kind := range []string{"transport", "cancelled", "close", "invalid result", "JSON writer"} {
		t.Run(kind, func(t *testing.T) {
			backend := &fakeBackend{}
			var out, diagnostic bytes.Buffer
			options := fakeOptions(backend, &out, &diagnostic)
			args := append(execArgs(), "demo", "--", "true")
			want := 1
			switch kind {
			case "transport":
				backend.err = io.ErrClosedPipe
			case "cancelled":
				backend.err = context.Canceled
				want = 130
			case "close":
				backend.execResult.ExitCode = 23
				backend.closeErr = errors.New("close failed")
			case "invalid result":
				backend.execResult.ExitCode = -1
			case "JSON writer":
				args = append(execArgs(), "--json", "demo", "--", "true")
				options.Stdout = failingWriter{err: io.ErrClosedPipe}
			}
			if code := Run(context.Background(), args, options); code != want || backend.closed != 1 || diagnostic.Len() == 0 {
				t.Fatalf("exec failure exit=%d, close=%d, stderr=%q", code, backend.closed, diagnostic.String())
			}
		})
	}
}
