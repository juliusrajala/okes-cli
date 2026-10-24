package podman

import (
	"context"
	"errors"
	"slices"
	"testing"

	"go-sesh/internal/process"
)

const testHostJSON = `{"host":{"security":{"rootless":true,"selinuxEnabled":true},"serviceIsRemote":false,"cgroupVersion":"v2","cgroupControllers":["memory","pids"],"rootlessNetworkCmd":"pasta"},"version":{"Version":"6.1.1","OsArch":"linux/amd64"}}`

type runnerStep struct {
	args   []string
	result process.Result
	err    error
}

func scriptClient(t *testing.T, steps ...runnerStep) *Client {
	t.Helper()
	calls := 0
	client, err := New(Options{
		Executable: "/trusted/podman",
		SourceEnv:  []string{"HOME=/tmp", "PATH=/usr/bin:/bin", "TOKEN=not-forwarded"},
		Runner: func(ctx context.Context, command process.Command) (process.Result, error) {
			t.Helper()
			if calls >= len(steps) {
				t.Fatalf("unexpected Podman call: %q", command.Args)
			}
			step := steps[calls]
			calls++
			want := append([]string{"--remote=false"}, step.args...)
			if command.Path != "/trusted/podman" || !slices.Equal(command.Args, want) {
				t.Fatalf("command = %q %q, want /trusted/podman %q", command.Path, command.Args, want)
			}
			if command.Env == nil || slices.Contains(command.Env, "TOKEN=not-forwarded") {
				t.Fatal("runner received inherited or credential-like environment")
			}
			if ctx == nil {
				t.Fatal("runner received nil context")
			}
			return step.result, step.err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if calls != len(steps) {
			t.Errorf("Podman calls = %d, want %d", calls, len(steps))
		}
	})
	return client
}

func hostStep() runnerStep {
	return runnerStep{args: []string{"info", "--format", "json"}, result: process.Result{Stdout: testHostJSON}}
}

func TestClientInvocation(t *testing.T) {
	client := scriptClient(t, runnerStep{args: []string{"version", "literal argument", "$(whoami)"}, result: process.Result{Stdout: "literal"}})
	result, err := client.invoke(context.Background(), "version", "literal argument", "$(whoami)")
	if err != nil || result.Stdout != "literal" {
		t.Fatalf("result = %+v, error = %v", result, err)
	}
}

func TestClientPreservesRunnerError(t *testing.T) {
	cause := errors.New("test runner failure")
	client := scriptClient(t, runnerStep{args: []string{"info"}, err: cause})
	if _, err := client.invoke(context.Background(), "info"); !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped runner error", err)
	}
}

func TestClientCancelledBeforeInvocation(t *testing.T) {
	client := scriptClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.invoke(ctx, "info"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestClientRejectsRelativeExecutable(t *testing.T) {
	if _, err := New(Options{Executable: "podman", SourceEnv: []string{}}); err == nil {
		t.Fatal("relative executable accepted")
	}
}
