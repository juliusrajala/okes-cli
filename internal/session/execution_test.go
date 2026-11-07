package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go-sesh/internal/runtime"
)

func (f *lifecycleRuntime) Exec(ctx context.Context, request runtime.ExecRequest) (runtime.ExecResult, error) {
	if err := f.step(ctx, "exec"); err != nil {
		return runtime.ExecResult{ExitCode: -1}, err
	}
	if err := f.owns(request.Target); err != nil {
		return runtime.ExecResult{ExitCode: -1}, err
	}
	receipt := onlyExecution(f.t, f.store, ID(request.Target.SessionID))
	if receipt.Outcome != ExecutionRunning || receipt.EndedAt != nil {
		f.t.Fatal("exec ran without a durable running receipt")
	}
	if f.execFunc != nil {
		return f.execFunc(ctx, request)
	}
	return runtime.ExecResult{ExitCode: 0}, nil
}

func (f *lifecycleRuntime) CancelExec(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	f.cleanupWasActive = ctx.Err() == nil
	if err := f.step(ctx, "cancel-exec"); err != nil {
		return runtime.Inspection{}, err
	}
	if err := f.owns(target); err != nil {
		return runtime.Inspection{}, err
	}
	if f.cancelExecFunc != nil {
		return f.cancelExecFunc(ctx, target)
	}
	f.state = runtime.Stopped
	return f.observation(), nil
}

func onlyExecution(t *testing.T, store *Store, id ID) ExecutionReceipt {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.root.Name(), "executions", string(id)))
	if err != nil || len(entries) != 1 {
		t.Fatalf("execution directory = %v, %v; want one receipt", entries, err)
	}
	executionID, err := ParseID(strings.TrimSuffix(entries[0].Name(), ".json"))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.GetExecution(context.Background(), id, executionID)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func execManager(t *testing.T) (*Manager, *lifecycleRuntime, ExecRequest) {
	t.Helper()
	manager, engine, start := mountedManager(t)
	view, err := manager.Start(context.Background(), start)
	if err != nil {
		t.Fatal(err)
	}
	engine.calls = nil
	return manager, engine, ExecRequest{Name: start.Name, SessionID: view.Record.ID, Command: []string{"printf", "$HOME; literal", ""}}
}

func TestExecPreconditions(t *testing.T) {
	for _, kind := range []string{"name mismatch", "UUID mismatch", "missing argv", "NUL argument", "relative cwd", "negative timeout", "excessive timeout", "paused", "stopped", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			manager, engine, request := execManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "name mismatch":
				request.Name = "other"
			case "UUID mismatch":
				request.SessionID = "3b68db01-63c8-4be5-a565-850700bb00ae"
			case "missing argv":
				request.Command = nil
			case "NUL argument":
				request.Command = []string{"program", "bad\x00"}
			case "relative cwd":
				request.Workdir = "relative"
			case "negative timeout":
				request.Timeout = -time.Second
			case "excessive timeout":
				request.Timeout = 25 * time.Hour
			case "paused":
				engine.state = runtime.Paused
			case "stopped":
				engine.state = runtime.Stopped
			case "cancelled":
				cancel()
			}
			_, err := manager.Exec(ctx, request)
			if err == nil || slices.Contains(engine.calls, "exec") {
				t.Fatalf("invalid exec = %v, calls=%v", err, engine.calls)
			}
		})
	}
}

func TestCancelExecutionChecksObservation(t *testing.T) {
	for _, kind := range []string{"stopped", "missing", "running", "wrong ID", "invalid missing", "transport"} {
		t.Run(kind, func(t *testing.T) {
			manager, engine, request := execManager(t)
			record, err := manager.Store.Get(context.Background(), request.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			engine.cancelExecFunc = func(context.Context, runtime.Target) (runtime.Inspection, error) {
				switch kind {
				case "stopped":
					engine.state = runtime.Stopped
					return engine.observation(), nil
				case "missing":
					return runtime.Inspection{State: runtime.Missing}, nil
				case "wrong ID":
					return runtime.Inspection{State: runtime.Running, ContainerID: strings.Repeat("c", 64)}, nil
				case "invalid missing":
					return runtime.Inspection{State: runtime.Missing, ContainerID: engine.target.ContainerID}, nil
				case "transport":
					return runtime.Inspection{}, io.ErrClosedPipe
				default:
					return engine.observation(), nil
				}
			}
			err = manager.cancelExecution(context.Background(), record)
			valid := kind == "stopped" || kind == "missing"
			if (err == nil) != valid || !engine.cleanupWasActive {
				t.Fatalf("cancel verification = %v, valid=%v", err, valid)
			}
			if !valid && !strings.Contains(err.Error(), "inspect and stop") {
				t.Fatalf("uncertain stop lost recovery guidance: %v", err)
			}
		})
	}
}

func TestExecutionReceiptValidate(t *testing.T) {
	initial := runningReceipt(t)
	for _, receipt := range []ExecutionReceipt{initial, completedReceipt(initial)} {
		if err := receipt.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*ExecutionReceipt){
		func(r *ExecutionReceipt) { r.Version = 2 },
		func(r *ExecutionReceipt) { r.SessionID = "../outside" },
		func(r *ExecutionReceipt) { r.Outcome = "unknown" },
		func(r *ExecutionReceipt) { r.StartedAt = time.Time{} },
		func(r *ExecutionReceipt) { r.Outcome = ExecutionCompleted },
		func(r *ExecutionReceipt) { status := 0; r.Status = &status },
		func(r *ExecutionReceipt) { end := r.StartedAt; r.EndedAt = &end },
	} {
		r := initial
		change(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("invalid receipt = %+v, %v", r, err)
		}
	}
}

func TestExecutionFinish(t *testing.T) {
	for _, tt := range []struct {
		name       string
		code       int
		cause      error
		cleanupErr error
		outcome    ExecutionOutcome
	}{
		{name: "zero", code: 0, outcome: ExecutionCompleted},
		{name: "nonzero", code: 23, outcome: ExecutionCompleted},
		{name: "cancelled", code: -1, cause: fmt.Errorf("wrapped: %w", context.Canceled), outcome: ExecutionCancelled},
		{name: "timeout", code: -1, cause: context.DeadlineExceeded, outcome: ExecutionTimeout},
		{name: "deadline precedence", code: -1, cause: errors.Join(context.Canceled, context.DeadlineExceeded), outcome: ExecutionTimeout},
		{name: "transport", code: -1, cause: io.ErrClosedPipe, outcome: ExecutionError},
		{name: "cleanup overrides cancellation", code: -1, cause: context.Canceled, cleanupErr: io.ErrClosedPipe, outcome: ExecutionError},
		{name: "cleanup overrides result", code: 0, cleanupErr: io.ErrClosedPipe, outcome: ExecutionError},
		{name: "cancelled with observed exit", code: 137, cause: context.Canceled, outcome: ExecutionCancelled},
	} {
		t.Run(tt.name, func(t *testing.T) {
			receipt := runningReceipt(t)
			initial := receipt
			end := receipt.StartedAt.Add(time.Second)
			err := receipt.finish(executionCompletion{Result: runtime.ExecResult{ExitCode: tt.code}, Cause: tt.cause, CleanupErr: tt.cleanupErr, EndedAt: end})
			if err != nil || receipt.Outcome != tt.outcome || receipt.EndedAt == nil || !receipt.EndedAt.Equal(end) || receipt.ID != initial.ID || !receipt.StartedAt.Equal(initial.StartedAt) {
				t.Fatalf("final receipt = %+v, %v", receipt, err)
			}
			if tt.code >= 0 {
				if receipt.Status == nil || *receipt.Status != tt.code {
					t.Fatalf("lost known exit status: %+v", receipt)
				}
			} else if receipt.Status != nil {
				t.Fatalf("invented exit status: %+v", receipt)
			}
			data, err := json.Marshal(receipt)
			if err != nil {
				t.Fatal(err)
			}
			if tt.code == 0 && !strings.Contains(string(data), `"status":0`) {
				t.Fatalf("zero status disappeared from JSON: %s", data)
			}
		})
	}
}

func TestExecutionFinishRejectsInvalidWithoutMutation(t *testing.T) {
	for _, kind := range []string{"terminal", "missing end", "end before start", "missing completed status", "invalid status", "invalid negative status"} {
		t.Run(kind, func(t *testing.T) {
			receipt := runningReceipt(t)
			completion := executionCompletion{Result: runtime.ExecResult{ExitCode: 0}, EndedAt: receipt.StartedAt.Add(time.Second)}
			switch kind {
			case "terminal":
				receipt = completedReceipt(receipt)
			case "missing end":
				completion.EndedAt = time.Time{}
			case "end before start":
				completion.EndedAt = receipt.StartedAt.Add(-time.Second)
			case "missing completed status":
				completion.Result.ExitCode = -1
			case "invalid status":
				completion.Result.ExitCode = 256
			case "invalid negative status":
				completion.Result.ExitCode = -2
				completion.Cause = io.ErrClosedPipe
			}
			before := receipt
			if err := receipt.finish(completion); err == nil || !reflect.DeepEqual(receipt, before) {
				t.Fatalf("invalid finalization changed receipt: %+v, %v", receipt, err)
			}
		})
	}
}

func TestExecTransaction(t *testing.T) {
	manager, engine, request := execManager(t)
	engine.execFunc = func(_ context.Context, workload runtime.ExecRequest) (runtime.ExecResult, error) {
		if !slices.Equal(workload.Command, request.Command) || workload.Target.ContainerID != engine.target.ContainerID {
			t.Fatalf("literal exec changed: %+v", workload)
		}
		return runtime.ExecResult{ExitCode: 23, Stdout: "result", Stderr: "diagnostic"}, nil
	}
	result, err := manager.Exec(context.Background(), request)
	if err != nil || result.ExitCode != 23 || result.Stdout != "result" || result.Receipt.Outcome != ExecutionCompleted || engine.state != runtime.Running || slices.Contains(engine.calls, "cancel-exec") {
		t.Fatalf("transaction = %+v, %v, calls=%v", result, err, engine.calls)
	}
	saved := onlyExecution(t, manager.Store, request.SessionID)
	if saved.Status == nil || *saved.Status != 23 || saved.EndedAt == nil {
		t.Fatalf("terminal receipt not saved: %+v", saved)
	}
}

func TestExecReceiptFailureBeforeWorkload(t *testing.T) {
	manager, engine, request := execManager(t)
	external := t.TempDir()
	if err := os.Symlink(external, filepath.Join(manager.Store.root.Name(), "executions")); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exec(context.Background(), request); !errors.Is(err, ErrUnsafeStore) || slices.Contains(engine.calls, "exec") {
		t.Fatalf("unsafe receipt storage = %v, calls=%v", err, engine.calls)
	}
}

func TestExecReceiptPersistenceFailure(t *testing.T) {
	manager, engine, request := execManager(t)
	engine.execFunc = func(context.Context, runtime.ExecRequest) (runtime.ExecResult, error) {
		receipt := onlyExecution(t, manager.Store, request.SessionID)
		path := filepath.Join(manager.Store.root.Name(), "executions", string(request.SessionID), recordPath(receipt.ID))
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		return runtime.ExecResult{ExitCode: 0, Stdout: "completed output"}, nil
	}
	result, err := manager.Exec(context.Background(), request)
	if !errors.Is(err, ErrUnsafeStore) || result.Stdout != "completed output" {
		t.Fatalf("receipt persistence failure = %+v, %v", result, err)
	}
	if _, err := manager.Store.Resolve(context.Background(), request.Name); err != nil {
		t.Fatalf("receipt failure removed recovery metadata: %v", err)
	}
}

func TestExecRecovery(t *testing.T) {
	for _, kind := range []string{"cancelled", "timeout", "transport", "unverified", "invalid result"} {
		t.Run(kind, func(t *testing.T) {
			manager, engine, request := execManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			want := ExecutionError
			engine.execFunc = func(ctx context.Context, _ runtime.ExecRequest) (runtime.ExecResult, error) {
				if kind == "cancelled" {
					cancel()
					return runtime.ExecResult{ExitCode: -1, Stdout: "partial"}, ctx.Err()
				}
				if kind == "timeout" {
					<-ctx.Done()
					return runtime.ExecResult{ExitCode: -1}, ctx.Err()
				}
				if kind == "invalid result" {
					return runtime.ExecResult{ExitCode: -1}, nil
				}
				return runtime.ExecResult{ExitCode: -1}, io.ErrClosedPipe
			}
			switch kind {
			case "cancelled":
				want = ExecutionCancelled
			case "timeout":
				request.Timeout = time.Millisecond
				want = ExecutionTimeout
			case "unverified":
				engine.cancelExecFunc = func(context.Context, runtime.Target) (runtime.Inspection, error) {
					return runtime.Inspection{}, runtime.ErrOwnership
				}
			}
			_, err := manager.Exec(ctx, request)
			if err == nil || !engine.cleanupWasActive || !slices.Contains(engine.calls, "cancel-exec") {
				t.Fatalf("recovery = %v, calls=%v, live cleanup=%v", err, engine.calls, engine.cleanupWasActive)
			}
			var wantCause error = io.ErrClosedPipe
			if kind == "cancelled" {
				wantCause = context.Canceled
			} else if kind == "timeout" {
				wantCause = context.DeadlineExceeded
			}
			if kind != "invalid result" && !errors.Is(err, wantCause) {
				t.Fatalf("recovery lost original cause: %v, want %v", err, wantCause)
			}
			if kind == "unverified" && !errors.Is(err, runtime.ErrOwnership) {
				t.Fatalf("recovery lost cleanup cause: %v", err)
			}
			if kind == "unverified" && !strings.Contains(err.Error(), "inspect and stop") {
				t.Fatalf("missing recovery guidance: %v", err)
			}
			if saved := onlyExecution(t, manager.Store, request.SessionID); saved.Outcome != want || saved.EndedAt == nil {
				t.Fatalf("recovery receipt = %+v, want %s", saved, want)
			}
		})
	}
}
