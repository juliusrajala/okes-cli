package podman

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
)

// lifecycleInspectionSteps describes every CLI call made by one real Inspect.
// An unbound target observes the same fixture ID as testTarget(true).
func lifecycleInspectionSteps(target runtime.Target, state runtime.State) []runnerStep {
	selector := target.ContainerID
	if selector == "" {
		selector = target.ContainerName
	}
	steps := []runnerStep{
		hostStep(),
		{args: []string{"container", "exists", selector}},
	}
	if state == runtime.Missing {
		steps[1].result.ExitCode = 1
		return steps
	}
	id := target.ContainerID
	if id == "" {
		id = strings.Repeat("b", 64)
	}
	return append(steps, runnerStep{
		args:   []string{"container", "inspect", "--format", "json", selector},
		result: process.Result{Stdout: inspectionOutput(string(state), "pi-sesh", target.SessionID, id)},
	})
}

// Cancel after a scripted mutation returns, before the next inspection starts.
// This avoids sleeps and does not start a real process.
func lifecycleCancelAfter(client *Client, verb string, cancel context.CancelFunc) {
	runner := client.runner
	client.runner = func(ctx context.Context, command process.Command) (process.Result, error) {
		result, err := runner(ctx, command)
		if len(command.Args) >= 3 && command.Args[1] == "container" && command.Args[2] == verb {
			cancel()
		}
		return result, err
	}
}

func TestStop(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known_ID_%t", known), func(t *testing.T) {
			target := testTarget(known)
			pinned := target
			pinned.ContainerID = strings.Repeat("b", 64)
			before := lifecycleInspectionSteps(target, runtime.Running)
			stop := runnerStep{args: []string{"container", "stop", "--time", "10", pinned.ContainerID}}
			exited := lifecycleInspectionSteps(pinned, runtime.Exited)
			stopped := lifecycleInspectionSteps(pinned, runtime.Stopped)
			missing := lifecycleInspectionSteps(pinned, runtime.Missing)

			wrongOwner := lifecycleInspectionSteps(target, runtime.Running)
			wrongOwner[2].result.Stdout = inspectionOutput("running", "other", target.SessionID, pinned.ContainerID)
			changedOwner := lifecycleInspectionSteps(pinned, runtime.Stopped)
			changedOwner[2].result.Stdout = inspectionOutput("stopped", "pi-sesh", "different-session", pinned.ContainerID)
			changedID := lifecycleInspectionSteps(pinned, runtime.Stopped)
			changedID[2].result.Stdout = inspectionOutput("stopped", "pi-sesh", target.SessionID, strings.Repeat("c", 64))
			invalidAfter := lifecycleInspectionSteps(pinned, runtime.Stopped)
			invalidAfter[2].result.Stdout = "not JSON"
			uncertainAfter := []runnerStep{
				hostStep(),
				{args: []string{"container", "exists", pinned.ContainerID}, result: process.Result{ExitCode: 125}},
			}
			unsupportedHost := hostStep()
			unsupportedHost.result.Stdout = strings.Replace(unsupportedHost.result.Stdout, `"rootless":true`, `"rootless":false`, 1)
			cause := errors.New("stop process failed")
			failedStop := stop
			failedStop.err = cause
			nonzeroStop := stop
			nonzeroStop.result.ExitCode = 125

			tests := []struct {
				name                string
				steps               []runnerStep
				wantState           runtime.State
				wantErr             error
				cancelBefore        bool
				cancelAfterMutation bool
			}{
				{name: "running to exited", steps: slices.Concat(before, []runnerStep{stop}, exited), wantState: runtime.Exited},
				{name: "running to stopped", steps: slices.Concat(before, []runnerStep{stop}, stopped), wantState: runtime.Stopped},
				{name: "running to missing", steps: slices.Concat(before, []runnerStep{stop}, missing), wantState: runtime.Missing},
				{name: "already exited still rechecks", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Exited), exited), wantState: runtime.Exited},
				{name: "already stopped still rechecks", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Stopped), stopped), wantState: runtime.Stopped},
				{name: "exited disappears before recheck", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Exited), missing), wantState: runtime.Missing},
				{name: "created is not stopped", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Created), lifecycleInspectionSteps(pinned, runtime.Created)), wantErr: runtime.ErrInvalidState},
				{name: "created then exited", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Created), exited), wantState: runtime.Exited},
				{name: "initially missing", steps: lifecycleInspectionSteps(target, runtime.Missing), wantErr: runtime.ErrInvalidState},
				{name: "initially paused", steps: lifecycleInspectionSteps(target, runtime.Paused), wantErr: runtime.ErrInvalidState},
				{name: "wrong ownership prevents stop", steps: wrongOwner, wantErr: runtime.ErrOwnership},
				{name: "unsupported host prevents stop", steps: []runnerStep{unsupportedHost}, wantErr: ErrUnsupportedHost},
				{name: "stop execution failure", steps: slices.Concat(before, []runnerStep{failedStop}), wantErr: cause},
				{name: "nonzero stop status", steps: slices.Concat(before, []runnerStep{nonzeroStop}), wantErr: ErrCommandFailed},
				{name: "still running after stop", steps: slices.Concat(before, []runnerStep{stop}, lifecycleInspectionSteps(pinned, runtime.Running)), wantErr: runtime.ErrInvalidState},
				{name: "paused after stop", steps: slices.Concat(before, []runnerStep{stop}, lifecycleInspectionSteps(pinned, runtime.Paused)), wantErr: runtime.ErrInvalidState},
				{name: "already exited becomes running", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Exited), lifecycleInspectionSteps(pinned, runtime.Running)), wantErr: runtime.ErrInvalidState},
				{name: "ownership changes after stop", steps: slices.Concat(before, []runnerStep{stop}, changedOwner), wantErr: runtime.ErrOwnership},
				{name: "ID changes after stop", steps: slices.Concat(before, []runnerStep{stop}, changedID), wantErr: runtime.ErrOwnership},
				{name: "invalid response after stop", steps: slices.Concat(before, []runnerStep{stop}, invalidAfter), wantErr: runtime.ErrInvalidInspection},
				{name: "uncertain absence after stop", steps: slices.Concat(before, []runnerStep{stop}, uncertainAfter), wantErr: ErrCommandFailed},
				{name: "cancelled before lookup", wantErr: context.Canceled, cancelBefore: true},
				{name: "cancelled after stop cannot report success", steps: slices.Concat(before, []runnerStep{stop}), wantErr: context.Canceled, cancelAfterMutation: true},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					client := scriptClient(t, tt.steps...)
					if tt.cancelBefore {
						cancel()
					}
					if tt.cancelAfterMutation {
						lifecycleCancelAfter(client, "stop", cancel)
					}
					got, err := client.Stop(ctx, target)
					if tt.wantErr != nil {
						if got != (runtime.Inspection{}) || !errors.Is(err, tt.wantErr) {
							t.Fatalf("Stop = %+v, %v, want zero inspection and %v", got, err, tt.wantErr)
						}
						return
					}
					if err != nil || got.State != tt.wantState {
						t.Fatalf("Stop = %+v, %v, want state %q and nil error", got, err, tt.wantState)
					}
					if got.State == runtime.Missing {
						if got.ContainerID != "" || got.ExitCode != nil {
							t.Fatalf("missing result includes container data: %+v", got)
						}
					} else if got.ContainerID != pinned.ContainerID || got.ExitCode == nil || *got.ExitCode != 7 {
						t.Fatalf("Stop did not return the final verified observation: %+v", got)
					}
				})
			}
		})
	}
	t.Run("invalid target prevents process execution", func(t *testing.T) {
		target := testTarget(false)
		target.ContainerName = "unrelated"
		client := scriptClient(t)
		got, err := client.Stop(context.Background(), target)
		if got != (runtime.Inspection{}) || err == nil {
			t.Fatalf("Stop = %+v, %v, want zero inspection and validation error", got, err)
		}
	})
}

func TestRemove(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprintf("known_ID_%t", known), func(t *testing.T) {
			target := testTarget(known)
			pinned := target
			pinned.ContainerID = strings.Repeat("b", 64)
			before := lifecycleInspectionSteps(target, runtime.Exited)
			rm := runnerStep{args: []string{"container", "rm", pinned.ContainerID}}
			missing := lifecycleInspectionSteps(pinned, runtime.Missing)
			initiallyMissing := lifecycleInspectionSteps(target, runtime.Missing)

			wrongOwner := lifecycleInspectionSteps(target, runtime.Exited)
			wrongOwner[2].result.Stdout = inspectionOutput("exited", "other", target.SessionID, pinned.ContainerID)
			changedOwner := lifecycleInspectionSteps(pinned, runtime.Created)
			changedOwner[2].result.Stdout = inspectionOutput("created", "pi-sesh", "different-session", pinned.ContainerID)
			changedID := lifecycleInspectionSteps(pinned, runtime.Created)
			changedID[2].result.Stdout = inspectionOutput("created", "pi-sesh", target.SessionID, strings.Repeat("c", 64))
			invalidAfter := lifecycleInspectionSteps(pinned, runtime.Exited)
			invalidAfter[2].result.Stdout = "not JSON"
			uncertainAfter := []runnerStep{
				hostStep(),
				{args: []string{"container", "exists", pinned.ContainerID}, result: process.Result{ExitCode: 125}},
			}
			uncertainMissing := []runnerStep{
				hostStep(),
				{args: initiallyMissing[1].args, result: process.Result{ExitCode: 125}},
			}
			unsupportedHost := hostStep()
			unsupportedHost.result.Stdout = strings.Replace(unsupportedHost.result.Stdout, `"rootless":true`, `"rootless":false`, 1)
			cause := errors.New("remove process failed")
			failedRM := rm
			failedRM.err = cause
			statusOne := rm
			statusOne.result.ExitCode = 1
			status125 := rm
			status125.result.ExitCode = 125
			verificationFailure := []runnerStep{hostStep()}
			verificationFailure[0].err = cause

			tests := []struct {
				name                string
				steps               []runnerStep
				wantErr             error
				cancelBefore        bool
				cancelAfterMutation bool
			}{
				{name: "remove exited then verify twice", steps: slices.Concat(before, []runnerStep{rm}, missing, missing)},
				{name: "remove stopped", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Stopped), []runnerStep{rm}, missing, missing)},
				{name: "remove created", steps: slices.Concat(lifecycleInspectionSteps(target, runtime.Created), []runnerStep{rm}, missing, missing)},
				{name: "initially missing still verifies again", steps: slices.Concat(initiallyMissing, initiallyMissing)},
				{name: "nonzero rm status with proven absence", steps: slices.Concat(before, []runnerStep{statusOne}, missing, missing)},
				{name: "status 125 with proven absence", steps: slices.Concat(before, []runnerStep{status125}, missing, missing)},
				{name: "running refuses removal", steps: lifecycleInspectionSteps(target, runtime.Running), wantErr: runtime.ErrInvalidState},
				{name: "paused refuses removal", steps: lifecycleInspectionSteps(target, runtime.Paused), wantErr: runtime.ErrInvalidState},
				{name: "wrong ownership prevents removal", steps: wrongOwner, wantErr: runtime.ErrOwnership},
				{name: "unsupported host prevents removal", steps: []runnerStep{unsupportedHost}, wantErr: ErrUnsupportedHost},
				{name: "rm execution failure", steps: slices.Concat(before, []runnerStep{failedRM}), wantErr: cause},
				{name: "successful rm but container remains", steps: slices.Concat(before, []runnerStep{rm}, lifecycleInspectionSteps(pinned, runtime.Exited)), wantErr: runtime.ErrInvalidState},
				{name: "failed rm and container remains", steps: slices.Concat(before, []runnerStep{status125}, lifecycleInspectionSteps(pinned, runtime.Exited)), wantErr: runtime.ErrInvalidState},
				{name: "container starts during removal", steps: slices.Concat(before, []runnerStep{rm}, lifecycleInspectionSteps(pinned, runtime.Running)), wantErr: runtime.ErrInvalidState},
				{name: "uncertain absence after rm", steps: slices.Concat(before, []runnerStep{rm}, uncertainAfter), wantErr: ErrCommandFailed},
				{name: "invalid response after rm", steps: slices.Concat(before, []runnerStep{rm}, invalidAfter), wantErr: runtime.ErrInvalidInspection},
				{name: "ownership changes after rm", steps: slices.Concat(before, []runnerStep{rm}, changedOwner), wantErr: runtime.ErrOwnership},
				{name: "ID changes after rm", steps: slices.Concat(before, []runnerStep{rm}, changedID), wantErr: runtime.ErrOwnership},
				{name: "final absence check is uncertain", steps: slices.Concat(before, []runnerStep{rm}, missing, uncertainAfter), wantErr: ErrCommandFailed},
				{name: "final inspection invocation fails", steps: slices.Concat(before, []runnerStep{rm}, missing, verificationFailure), wantErr: cause},
				{name: "container present at final check", steps: slices.Concat(before, []runnerStep{rm}, missing, lifecycleInspectionSteps(pinned, runtime.Created)), wantErr: runtime.ErrInvalidState},
				{name: "ownership changes at final check", steps: slices.Concat(before, []runnerStep{rm}, missing, changedOwner), wantErr: runtime.ErrOwnership},
				{name: "missing then uncertain", steps: slices.Concat(initiallyMissing, uncertainMissing), wantErr: ErrCommandFailed},
				{name: "missing then container appears", steps: slices.Concat(initiallyMissing, lifecycleInspectionSteps(target, runtime.Created)), wantErr: runtime.ErrInvalidState},
				{name: "cancelled before lookup", wantErr: context.Canceled, cancelBefore: true},
				{name: "cancelled after rm cannot report success", steps: slices.Concat(before, []runnerStep{rm}), wantErr: context.Canceled, cancelAfterMutation: true},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					client := scriptClient(t, tt.steps...)
					if tt.cancelBefore {
						cancel()
					}
					if tt.cancelAfterMutation {
						lifecycleCancelAfter(client, "rm", cancel)
					}
					err := client.Remove(ctx, target)
					if tt.wantErr != nil {
						if !errors.Is(err, tt.wantErr) {
							t.Fatalf("Remove error = %v, want %v", err, tt.wantErr)
						}
					} else if err != nil {
						t.Fatalf("Remove error = %v, want nil", err)
					}
				})
			}
		})
	}
	t.Run("invalid target prevents process execution", func(t *testing.T) {
		target := testTarget(false)
		target.ContainerName = "unrelated"
		client := scriptClient(t)
		if err := client.Remove(context.Background(), target); err == nil {
			t.Fatal("invalid target was accepted")
		}
	})
}
