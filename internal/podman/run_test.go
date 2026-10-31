package podman

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

func testRunRequest(t *testing.T) runtime.CreateRequest {
	t.Helper()
	request := testCreateRequest()
	request.WorkspacePath = t.TempDir()
	return request
}

func TestPrepareCreate(t *testing.T) {
	request := testRunRequest(t)
	client := scriptClient(t, hostStep())
	args, err := client.prepareCreate(context.Background(), request)
	if err != nil || args[0] != "create" || !slices.Contains(args, request.WorkspacePath+":/workspace:rw,Z") {
		t.Fatalf("prepared argv = %q, %v", args, err)
	}
}

func TestPrepareCreateRejectsBeforeEngine(t *testing.T) {
	for _, kind := range []string{"invalid request", "missing path", "retargeted path", "copy"} {
		t.Run(kind, func(t *testing.T) {
			request := testRunRequest(t)
			switch kind {
			case "invalid request":
				request.Environment = map[string]string{"API_KEY": "not-forwarded"}
			case "missing path":
				request.WorkspacePath = filepath.Join(request.WorkspacePath, "missing")
			case "retargeted path":
				alias := filepath.Join(t.TempDir(), "alias")
				if err := os.Symlink(request.WorkspacePath, alias); err != nil {
					t.Fatal(err)
				}
				request.WorkspacePath = alias
			case "copy":
				request.WorkspaceMode = workspace.Copy
			}
			client := scriptClient(t)
			if args, err := client.prepareCreate(context.Background(), request); args != nil || err == nil {
				t.Fatalf("unsafe preparation returned argv = %q, %v", args, err)
			}
		})
	}
}

func TestCreate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		output  string
		status  int
		runErr  error
		wantErr error
	}{
		{name: "created", output: strings.Repeat("B", 64) + "\n"},
		{name: "nonzero exit", status: 125, output: strings.Repeat("b", 64), wantErr: ErrCommandFailed},
		{name: "execution failure", runErr: context.DeadlineExceeded, wantErr: context.DeadlineExceeded},
		{name: "invalid output", output: "not an ID", wantErr: ErrInvalidResponse},
		{name: "pending marker", output: strings.Repeat("0", 64), wantErr: ErrInvalidResponse},
	} {
		t.Run(tt.name, func(t *testing.T) {
			request := testRunRequest(t)
			args, err := buildCreateArgs(request, createHost{UID: os.Getuid(), GID: os.Getgid(), SELinuxEnabled: true})
			if err != nil {
				t.Fatal(err)
			}
			client := scriptClient(t, hostStep(), runnerStep{args: args, result: process.Result{ExitCode: tt.status, Stdout: tt.output}, err: tt.runErr})
			got, err := client.Create(context.Background(), request)
			if tt.wantErr != nil {
				if got != "" || !errors.Is(err, tt.wantErr) {
					t.Fatalf("Create = %q, %v, want empty ID and %v", got, err, tt.wantErr)
				}
			} else if err != nil || got != strings.Repeat("b", 64) {
				t.Fatalf("Create = %q, %v", got, err)
			}
		})
	}
	t.Run("invalid input prevents creation", func(t *testing.T) {
		request := testRunRequest(t)
		request.Command = nil
		client := scriptClient(t)
		if id, err := client.Create(context.Background(), request); id != "" || err == nil {
			t.Fatalf("invalid Create = %q, %v", id, err)
		}
	})
	t.Run("cancelled before invocation", func(t *testing.T) {
		client := scriptClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if id, err := client.Create(ctx, testRunRequest(t)); id != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled Create = %q, %v", id, err)
		}
	})
}

func TestStartDetached(t *testing.T) {
	for _, known := range []bool{false, true} {
		target := testTarget(known)
		pinned := testTarget(true)
		before := lifecycleInspectionSteps(target, runtime.Created)
		start := runnerStep{args: []string{"container", "start", pinned.ContainerID}}
		wrongOwner := lifecycleInspectionSteps(target, runtime.Created)
		wrongOwner[2].result.Stdout = inspectionOutput("created", "other", target.SessionID, pinned.ContainerID)
		changedID := lifecycleInspectionSteps(pinned, runtime.Running)
		changedID[2].result.Stdout = inspectionOutput("running", "pi-sesh", target.SessionID, strings.Repeat("c", 64))
		changedOwner := lifecycleInspectionSteps(pinned, runtime.Running)
		changedOwner[2].result.Stdout = inspectionOutput("running", "other", target.SessionID, pinned.ContainerID)
		invalidAfter := lifecycleInspectionSteps(pinned, runtime.Running)
		invalidAfter[2].result.Stdout = "not JSON"
		uncertainAfter := []runnerStep{hostStep(), {args: []string{"container", "exists", pinned.ContainerID}, result: process.Result{ExitCode: 125}}}
		for _, state := range []runtime.State{runtime.Running, runtime.Exited, runtime.Stopped, runtime.Created, runtime.Paused} {
			t.Run(string(state)+" known="+fmt.Sprint(known), func(t *testing.T) {
				steps := slices.Concat(before, []runnerStep{start}, lifecycleInspectionSteps(pinned, state))
				client := scriptClient(t, steps...)
				got, err := client.StartDetached(context.Background(), target)
				if err != nil || got.State != state || got.ContainerID != pinned.ContainerID {
					t.Fatalf("StartDetached = %+v, %v, want %s", got, err, state)
				}
			})
		}
		for _, state := range []runtime.State{runtime.Missing, runtime.Running, runtime.Exited, runtime.Stopped, runtime.Paused} {
			t.Run("refuse "+string(state)+" known="+fmt.Sprint(known), func(t *testing.T) {
				client := scriptClient(t, lifecycleInspectionSteps(target, state)...)
				got, err := client.StartDetached(context.Background(), target)
				if got != (runtime.Inspection{}) || !errors.Is(err, runtime.ErrInvalidState) {
					t.Fatalf("StartDetached = %+v, %v, want zero observation and invalid state", got, err)
				}
			})
		}
		for _, tt := range []struct {
			name    string
			steps   []runnerStep
			wantErr error
		}{
			{name: "start command fails", steps: append(slices.Clone(before), runnerStep{args: start.args, result: process.Result{ExitCode: 125}}), wantErr: ErrCommandFailed},
			{name: "start execution fails", steps: append(slices.Clone(before), runnerStep{args: start.args, err: context.DeadlineExceeded}), wantErr: context.DeadlineExceeded},
			{name: "disappeared", steps: slices.Concat(before, []runnerStep{start}, lifecycleInspectionSteps(pinned, runtime.Missing)), wantErr: runtime.ErrInvalidState},
			{name: "wrong ownership prevents start", steps: wrongOwner, wantErr: runtime.ErrOwnership},
			{name: "ID changes after start", steps: slices.Concat(before, []runnerStep{start}, changedID), wantErr: runtime.ErrOwnership},
			{name: "ownership changes after start", steps: slices.Concat(before, []runnerStep{start}, changedOwner), wantErr: runtime.ErrOwnership},
			{name: "invalid post-start inspection", steps: slices.Concat(before, []runnerStep{start}, invalidAfter), wantErr: runtime.ErrInvalidInspection},
			{name: "uncertain post-start state", steps: slices.Concat(before, []runnerStep{start}, uncertainAfter), wantErr: ErrCommandFailed},
		} {
			t.Run(tt.name+" known="+fmt.Sprint(known), func(t *testing.T) {
				client := scriptClient(t, tt.steps...)
				got, err := client.StartDetached(context.Background(), target)
				if got != (runtime.Inspection{}) || !errors.Is(err, tt.wantErr) {
					t.Fatalf("StartDetached = %+v, %v, want zero observation and %v", got, err, tt.wantErr)
				}
			})
		}
	}
	t.Run("cancelled after start", func(t *testing.T) {
		target := testTarget(false)
		pinned := testTarget(true)
		steps := append(lifecycleInspectionSteps(target, runtime.Created), runnerStep{args: []string{"container", "start", pinned.ContainerID}})
		client := scriptClient(t, steps...)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		lifecycleCancelAfter(client, "start", cancel)
		got, err := client.StartDetached(ctx, target)
		if got != (runtime.Inspection{}) || !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted start = %+v, %v", got, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		client := scriptClient(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		got, err := client.StartDetached(ctx, testTarget(true))
		if got != (runtime.Inspection{}) || !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled start = %+v, %v", got, err)
		}
	})
}
