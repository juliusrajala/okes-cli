package podman

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
)

const testSessionID = "7ad20f51-6067-4dc4-a072-54c94d7735de"

func inspectionOutput(state, owner, sessionID, containerID string) string {
	return fmt.Sprintf(`[{"Id":%q,"State":{"Status":%q,"ExitCode":7},"Config":{"Labels":{"io.pi-sesh.owner":%q,"io.pi-sesh.session-id":%q}}}]`, containerID, state, owner, sessionID)
}

func testTarget(known bool) runtime.Target {
	target := runtime.Target{SessionID: testSessionID, ContainerName: "pi-sesh-" + testSessionID}
	if known {
		target.ContainerID = strings.Repeat("b", 64)
	}
	return target
}

func TestInspectVerifiedContainer(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(fmt.Sprint(known), func(t *testing.T) {
			target := testTarget(known)
			selector := target.ContainerName
			if known {
				selector = target.ContainerID
			}
			output := inspectionOutput("running", "pi-sesh", target.SessionID, strings.Repeat("b", 64))
			client := scriptClient(t, hostStep(),
				runnerStep{args: []string{"container", "exists", selector}},
				runnerStep{args: []string{"container", "inspect", "--format", "json", selector}, result: process.Result{Stdout: output}},
			)
			got, err := client.Inspect(context.Background(), target)
			if err != nil || got.State != runtime.Running || got.ContainerID != strings.Repeat("b", 64) || got.ExitCode != nil {
				t.Fatalf("inspection = %+v, error = %v", got, err)
			}
		})
	}
}

func TestInspectMissingOnlyFromExistsStatusOne(t *testing.T) {
	target := testTarget(false)
	for _, status := range []int{1, 2, 125} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := scriptClient(t, hostStep(), runnerStep{args: []string{"container", "exists", target.ContainerName}, result: process.Result{ExitCode: status}})
			got, err := client.Inspect(context.Background(), target)
			if status == 1 {
				if err != nil || got.State != runtime.Missing || got.ContainerID != "" || got.ExitCode != nil {
					t.Fatalf("missing inspection = %+v, %v", got, err)
				}
			} else if !errors.Is(err, ErrCommandFailed) || got != (runtime.Inspection{}) {
				t.Fatalf("inspection = %+v, error = %v, want error without guessed state", got, err)
			}
		})
	}
}

func TestInspectPreservesExecutionFailure(t *testing.T) {
	target := testTarget(false)
	cause := errors.New("engine connection failed")
	client := scriptClient(t, hostStep(), runnerStep{args: []string{"container", "exists", target.ContainerName}, err: cause})
	got, err := client.Inspect(context.Background(), target)
	if !errors.Is(err, cause) || got != (runtime.Inspection{}) {
		t.Fatalf("inspection = %+v, error = %v", got, err)
	}
}

func TestInspectRejectsUnsupportedHostBeforeContainerQuery(t *testing.T) {
	step := hostStep()
	step.result.Stdout = strings.Replace(testHostJSON, `"rootless":true`, `"rootless":false`, 1)
	client := scriptClient(t, step)
	if _, err := client.Inspect(context.Background(), testTarget(false)); !errors.Is(err, ErrUnsupportedHost) {
		t.Fatalf("error = %v", err)
	}
}

func TestInspectDoesNotGuessMissingAfterInspectFailure(t *testing.T) {
	target := testTarget(true)
	client := scriptClient(t, hostStep(),
		runnerStep{args: []string{"container", "exists", target.ContainerID}},
		runnerStep{args: []string{"container", "inspect", "--format", "json", target.ContainerID}, result: process.Result{ExitCode: 125}},
	)
	got, err := client.Inspect(context.Background(), target)
	if !errors.Is(err, ErrCommandFailed) || got != (runtime.Inspection{}) {
		t.Fatalf("inspection = %+v, error = %v, want no guessed state", got, err)
	}
}

func TestInspectCancelledBeforeInvocation(t *testing.T) {
	client := scriptClient(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := client.Inspect(ctx, testTarget(false))
	if !errors.Is(err, context.Canceled) || got != (runtime.Inspection{}) {
		t.Fatalf("inspection = %+v, error = %v", got, err)
	}
}

func TestInspectionStateConversion(t *testing.T) {
	tests := []struct {
		status   string
		state    runtime.State
		wantExit bool
	}{
		{status: "running", state: runtime.Running},
		{status: "RUNNING", state: runtime.Running},
		{status: "created", state: runtime.Created},
		{status: "configured", state: runtime.Created},
		{status: "paused", state: runtime.Paused},
		{status: "exited", state: runtime.Exited, wantExit: true},
		{status: "stopped", state: runtime.Stopped, wantExit: true},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			got, err := parseInspection(inspectionOutput(tt.status, "pi-sesh", testSessionID, strings.Repeat("b", 64)), testTarget(true))
			if err != nil || got.State != tt.state || (got.ExitCode != nil) != tt.wantExit {
				t.Fatalf("inspection = %+v, error = %v", got, err)
			}
			if got.ExitCode != nil && *got.ExitCode != 7 {
				t.Errorf("exit code = %d, want 7", *got.ExitCode)
			}
		})
	}
}

func TestInspectionRejectsOwnershipMismatch(t *testing.T) {
	tests := []struct{ name, owner, sessionID, id string }{
		{name: "foreign owner", owner: "another-tool", sessionID: testSessionID, id: strings.Repeat("b", 64)},
		{name: "wrong session", owner: "pi-sesh", sessionID: "another-session", id: strings.Repeat("b", 64)},
		{name: "wrong immutable ID", owner: "pi-sesh", sessionID: testSessionID, id: strings.Repeat("c", 64)},
		{name: "missing labels", id: strings.Repeat("b", 64)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseInspection(inspectionOutput("running", tt.owner, tt.sessionID, tt.id), testTarget(true))
			if !errors.Is(err, runtime.ErrOwnership) || got != (runtime.Inspection{}) {
				t.Fatalf("inspection = %+v, error = %v, want ErrOwnership", got, err)
			}
		})
	}
}

func TestInspectionRejectsUncertainData(t *testing.T) {
	valid := inspectionOutput("running", "pi-sesh", testSessionID, strings.Repeat("b", 64))
	for _, output := range []string{
		`{`, `null`, `[]`, valid + `{}`,
		strings.Replace(valid, `"ExitCode":7`, `"ExitCode":null`, 1),
		strings.Replace(valid, `"ExitCode":7`, `"ExitCode":1.5`, 1),
		strings.Replace(valid, `"Status":"running"`, `"Status":null`, 1),
		inspectionOutput("removing", "pi-sesh", testSessionID, strings.Repeat("b", 64)),
		inspectionOutput("missing", "pi-sesh", testSessionID, strings.Repeat("b", 64)),
		inspectionOutput("running", "pi-sesh", testSessionID, "short"),
		inspectionOutput("running", "pi-sesh", testSessionID, strings.Repeat("0", 64)),
	} {
		t.Run(output, func(t *testing.T) {
			got, err := parseInspection(output, testTarget(true))
			if !errors.Is(err, runtime.ErrInvalidInspection) || got != (runtime.Inspection{}) {
				t.Fatalf("inspection = %+v, error = %v, want invalid inspection", got, err)
			}
		})
	}
}

func TestInspectionAcceptsObjectAndTopLevelLabels(t *testing.T) {
	output := fmt.Sprintf(`{"Id":%q,"State":{"Status":"exited","ExitCode":0},"Labels":{"io.pi-sesh.owner":"pi-sesh","io.pi-sesh.session-id":%q},"OtherEngineField":true}`, strings.Repeat("b", 64), testSessionID)
	got, err := parseInspection(output, testTarget(true))
	if err != nil || got.State != runtime.Exited || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Fatalf("inspection = %+v, error = %v", got, err)
	}
}

func TestInspectRejectsInvalidTargetWithoutRunningPodman(t *testing.T) {
	for _, target := range []runtime.Target{
		{},
		{SessionID: testSessionID, ContainerName: "another-name"},
		{SessionID: testSessionID, ContainerName: "pi-sesh-" + testSessionID, ContainerID: strings.Repeat("0", 64)},
		{SessionID: testSessionID, ContainerName: "pi-sesh-" + testSessionID, ContainerID: "--all"},
	} {
		client := scriptClient(t)
		if _, err := client.Inspect(context.Background(), target); err == nil {
			t.Fatal("invalid target accepted")
		}
	}
}
