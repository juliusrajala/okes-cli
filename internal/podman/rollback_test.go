package podman

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
)

func TestRollbackCreate(t *testing.T) {
	for _, state := range []runtime.State{runtime.Created, runtime.Running, runtime.Paused, runtime.Exited} {
		t.Run(string(state), func(t *testing.T) {
			target := testTarget(false)
			pinned := testTarget(true)
			missing := lifecycleInspectionSteps(pinned, runtime.Missing)
			steps := slices.Concat(lifecycleInspectionSteps(target, state), []runnerStep{
				{args: []string{"container", "rm", "--force", pinned.ContainerID}},
			}, missing, missing)
			client := scriptClient(t, steps...)
			if err := client.RollbackCreate(context.Background(), target); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("foreign ownership prevents force removal", func(t *testing.T) {
		target := testTarget(false)
		steps := lifecycleInspectionSteps(target, runtime.Running)
		steps[2].result.Stdout = inspectionOutput("running", "other", target.SessionID, strings.Repeat("b", 64))
		client := scriptClient(t, steps...)
		if err := client.RollbackCreate(context.Background(), target); !errors.Is(err, runtime.ErrOwnership) {
			t.Fatalf("foreign rollback = %v", err)
		}
	})
	t.Run("unknown absence is not success", func(t *testing.T) {
		target := testTarget(true)
		steps := append(lifecycleInspectionSteps(target, runtime.Created),
			runnerStep{args: []string{"container", "rm", "--force", target.ContainerID}},
			hostStep(), runnerStep{args: []string{"container", "exists", target.ContainerID}, result: process.Result{ExitCode: 125}},
		)
		client := scriptClient(t, steps...)
		if err := client.RollbackCreate(context.Background(), target); !errors.Is(err, ErrCommandFailed) {
			t.Fatalf("uncertain rollback = %v", err)
		}
	})
}
