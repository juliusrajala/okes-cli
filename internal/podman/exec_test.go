package podman

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
)

func TestExec(t *testing.T) {
	for _, known := range []bool{false, true} {
		target := testTarget(known)
		pinned := testTarget(true)
		args := []string{"exec", "--interactive", "--workdir", "/workspace", "--", pinned.ContainerID, "printf", "$HOME; literal", "", "--name"}
		client := scriptClient(t, append(lifecycleInspectionSteps(target, runtime.Running), runnerStep{
			args: args, result: process.Result{ExitCode: 23, Stdout: "output", Stderr: "diagnostic"},
		})...)
		got, err := client.Exec(context.Background(), runtime.ExecRequest{Target: target, Command: []string{"printf", "$HOME; literal", "", "--name"}})
		if err != nil || got.ExitCode != 23 || got.Stdout != "output" || got.Stderr != "diagnostic" {
			t.Fatalf("Exec = %+v, %v", got, err)
		}
	}
}

func TestExecPassesBorrowedStreams(t *testing.T) {
	target := testTarget(true)
	client := scriptClient(t, append(lifecycleInspectionSteps(target, runtime.Running), runnerStep{
		args: []string{"exec", "--interactive", "--workdir", "/tmp", "--", target.ContainerID, "program"},
	})...)
	stdin := strings.NewReader("input")
	var out, diagnostic strings.Builder
	run := client.runner
	client.runner = func(ctx context.Context, command process.Command) (process.Result, error) {
		if slices.Contains(command.Args, "exec") {
			if command.Stdin != stdin || command.Stdout != &out || command.Stderr != &diagnostic {
				t.Fatal("exec changed borrowed streams")
			}
		}
		return run(ctx, command)
	}
	_, err := client.Exec(context.Background(), runtime.ExecRequest{Target: target, Command: []string{"program"}, Workdir: "/tmp", Stdin: stdin, Stdout: &out, Stderr: &diagnostic})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExecRefusesInvalidStateAndOwnership(t *testing.T) {
	for _, state := range []runtime.State{runtime.Created, runtime.Exited, runtime.Stopped, runtime.Paused, runtime.Missing} {
		t.Run(string(state), func(t *testing.T) {
			target := testTarget(true)
			client := scriptClient(t, lifecycleInspectionSteps(target, state)...)
			got, err := client.Exec(context.Background(), runtime.ExecRequest{Target: target, Command: []string{"true"}})
			if got.ExitCode != -1 || !errors.Is(err, runtime.ErrInvalidState) {
				t.Fatalf("Exec = %+v, %v", got, err)
			}
		})
	}
	target := testTarget(true)
	steps := lifecycleInspectionSteps(target, runtime.Running)
	steps[2].result.Stdout = inspectionOutput("running", "other", target.SessionID, target.ContainerID)
	client := scriptClient(t, steps...)
	if _, err := client.Exec(context.Background(), runtime.ExecRequest{Target: target, Command: []string{"true"}}); !errors.Is(err, runtime.ErrOwnership) {
		t.Fatalf("foreign exec = %v", err)
	}
}

func TestExecRejectsBeforeEngine(t *testing.T) {
	for _, request := range []runtime.ExecRequest{
		{}, {Command: []string{""}}, {Command: []string{"true", "bad\x00"}},
		{Command: []string{"true"}, Workdir: "relative"},
		{Command: append([]string{"true"}, make([]string, 256)...)},
	} {
		client := scriptClient(t)
		if got, err := client.Exec(context.Background(), request); got.ExitCode != -1 || err == nil {
			t.Fatalf("invalid exec = %+v, %v", got, err)
		}
	}
}

func TestExecTransportFailureRetainsPartialOutput(t *testing.T) {
	target := testTarget(true)
	steps := append(lifecycleInspectionSteps(target, runtime.Running), runnerStep{
		args:   []string{"exec", "--interactive", "--workdir", "/workspace", "--", target.ContainerID, "program"},
		result: process.Result{ExitCode: -1, Stdout: "partial"}, err: io.ErrClosedPipe,
	})
	client := scriptClient(t, steps...)
	got, err := client.Exec(context.Background(), runtime.ExecRequest{Target: target, Command: []string{"program"}})
	if got.Stdout != "partial" || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("failed Exec = %+v, %v", got, err)
	}
}

func TestCancelExec(t *testing.T) {
	target := testTarget(true)
	for _, state := range []runtime.State{runtime.Running, runtime.Exited, runtime.Stopped, runtime.Missing} {
		t.Run(string(state), func(t *testing.T) {
			steps := lifecycleInspectionSteps(target, state)
			if state == runtime.Running {
				steps = append(steps, runnerStep{args: []string{"container", "stop", "--time", "0", target.ContainerID}})
			}
			if state != runtime.Missing {
				steps = append(steps, lifecycleInspectionSteps(target, runtime.Exited)...)
			}
			client := scriptClient(t, steps...)
			got, err := client.CancelExec(context.Background(), target)
			if err != nil || !got.State.StoppedOrMissing() {
				t.Fatalf("CancelExec = %+v, %v", got, err)
			}
		})
	}
	for _, state := range []runtime.State{runtime.Paused, runtime.Created} {
		client := scriptClient(t, lifecycleInspectionSteps(target, state)...)
		if got, err := client.CancelExec(context.Background(), target); got != (runtime.Inspection{}) || !errors.Is(err, runtime.ErrInvalidState) {
			t.Fatalf("unsafe CancelExec = %+v, %v", got, err)
		}
	}
}

func TestCancelExecDoesNotInferStopped(t *testing.T) {
	target := testTarget(true)
	for _, kind := range []string{"still running", "inspection failure", "stop failure", "ownership changes"} {
		t.Run(kind, func(t *testing.T) {
			steps := append(lifecycleInspectionSteps(target, runtime.Running), runnerStep{args: []string{"container", "stop", "--time", "0", target.ContainerID}})
			after := lifecycleInspectionSteps(target, runtime.Running)
			switch kind {
			case "inspection failure":
				after[2].result.Stdout = "invalid JSON"
			case "stop failure":
				steps[len(steps)-1].result.ExitCode = 125
				after = nil
			case "ownership changes":
				after[2].result.Stdout = inspectionOutput("exited", "other", target.SessionID, target.ContainerID)
			}
			client := scriptClient(t, append(steps, after...)...)
			if got, err := client.CancelExec(context.Background(), target); got != (runtime.Inspection{}) || err == nil {
				t.Fatalf("uncertain CancelExec = %+v, %v", got, err)
			}
		})
	}
}
