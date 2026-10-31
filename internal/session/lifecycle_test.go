package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

// This fake models engine state separately from saved metadata. Its create/start
// checks prove that recovery records are present before either side effect.
type lifecycleRuntime struct {
	t                *testing.T
	store            *Store
	calls            []string
	state            runtime.State
	target           runtime.Target
	request          runtime.CreateRequest
	failAt           string
	cause            error
	rollbackErr      error
	createSideEffect bool
	returnID         string
	startObservation *runtime.Inspection
	startState       runtime.State
	keepAfterRemove  bool
	afterCreate      func()
	afterStart       func()
	cleanupWasActive bool
}

func (f *lifecycleRuntime) step(ctx context.Context, op string) error {
	f.calls = append(f.calls, op)
	if err := ctx.Err(); err != nil {
		return err
	}
	if op == f.failAt {
		return f.cause
	}
	return nil
}

func (f *lifecycleRuntime) Preflight(ctx context.Context, _ runtime.NetworkMode) (runtime.HostInfo, error) {
	return runtime.HostInfo{Version: "6.1.1"}, f.step(ctx, "preflight")
}

func (f *lifecycleRuntime) ImageID(ctx context.Context, _ string) (string, error) {
	return strings.Repeat("a", 64), f.step(ctx, "image")
}

func (f *lifecycleRuntime) Create(ctx context.Context, request runtime.CreateRequest) (string, error) {
	err := f.step(ctx, "create")
	record, readErr := f.store.Get(context.Background(), ID(request.Target.SessionID))
	if readErr != nil || record.ContainerID != PendingContainerID {
		f.t.Fatalf("create ran without pending recovery metadata: %+v, %v", record, readErr)
	}
	if _, readErr := f.store.Resolve(context.Background(), record.Name); readErr != nil {
		f.t.Fatalf("create ran without a matching name reservation: %v", readErr)
	}
	if err != nil && !f.createSideEffect {
		return "", err
	}
	f.request = request
	f.target = request.Target
	f.target.ContainerID = strings.Repeat("b", 64)
	f.state = runtime.Created
	if f.afterCreate != nil {
		f.afterCreate()
	}
	if err != nil {
		return "", err
	}
	if f.returnID != "" {
		return f.returnID, nil
	}
	return f.target.ContainerID, nil
}

func (f *lifecycleRuntime) StartDetached(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	record, err := f.store.Get(context.Background(), ID(target.SessionID))
	if err != nil || record.ContainerID != f.target.ContainerID || target.ContainerID != record.ContainerID {
		f.t.Fatalf("start ran before immutable container ID was saved: %+v, %v", record, err)
	}
	f.state = f.startState
	if f.state == "" {
		f.state = runtime.Running
	}
	if err := f.step(ctx, "start"); err != nil {
		return runtime.Inspection{}, err
	}
	if f.afterStart != nil {
		f.afterStart()
	}
	if f.startObservation != nil {
		return *f.startObservation, nil
	}
	return f.observation(), nil
}

func (f *lifecycleRuntime) owns(target runtime.Target) error {
	if f.state == runtime.Missing || f.state == "" {
		return nil
	}
	if target.SessionID != f.target.SessionID || target.ContainerName != f.target.ContainerName ||
		(target.ContainerID != "" && target.ContainerID != f.target.ContainerID) {
		return runtime.ErrOwnership
	}
	return nil
}

func (f *lifecycleRuntime) observation() runtime.Inspection {
	if f.state == runtime.Missing || f.state == "" {
		return runtime.Inspection{State: runtime.Missing}
	}
	observed := runtime.Inspection{State: f.state, ContainerID: f.target.ContainerID}
	if f.state == runtime.Exited || f.state == runtime.Stopped {
		code := 0
		observed.ExitCode = &code
	}
	return observed
}

func (f *lifecycleRuntime) Inspect(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	if err := f.step(ctx, "inspect"); err != nil {
		return runtime.Inspection{}, err
	}
	if err := f.owns(target); err != nil {
		return runtime.Inspection{}, err
	}
	return f.observation(), nil
}

func (f *lifecycleRuntime) Stop(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	if err := f.step(ctx, "stop"); err != nil {
		return runtime.Inspection{}, err
	}
	if err := f.owns(target); err != nil {
		return runtime.Inspection{}, err
	}
	if f.state == runtime.Paused || f.state == runtime.Missing {
		return runtime.Inspection{}, runtime.ErrInvalidState
	}
	f.state = runtime.Stopped
	return f.observation(), nil
}

func (f *lifecycleRuntime) Remove(ctx context.Context, target runtime.Target) error {
	if err := f.step(ctx, "remove"); err != nil {
		return err
	}
	if err := f.owns(target); err != nil {
		return err
	}
	if f.state == runtime.Running || f.state == runtime.Paused {
		return runtime.ErrInvalidState
	}
	if !f.keepAfterRemove {
		f.state = runtime.Missing
	}
	return nil
}

func (f *lifecycleRuntime) RollbackCreate(ctx context.Context, target runtime.Target) error {
	f.cleanupWasActive = ctx.Err() == nil
	if err := f.step(ctx, "rollback"); err != nil {
		return err
	}
	if err := f.owns(target); err != nil {
		return err
	}
	if f.rollbackErr != nil {
		return f.rollbackErr
	}
	f.state = runtime.Missing
	return nil
}

func mountedManager(t *testing.T) (*Manager, *lifecycleRuntime, StartRequest) {
	t.Helper()
	store := testStore(t, filepath.Join(t.TempDir(), "state"))
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "source.txt"), []byte("caller data"), 0o600); err != nil {
		t.Fatal(err)
	}
	engine := &lifecycleRuntime{t: t, store: store}
	return &Manager{Store: store, Runtime: engine}, engine, StartRequest{
		Name: "checks", Image: "localhost/worker:1", WorkspacePath: source,
		Command: []string{"worker", "argument with spaces", "$(not a host command)"},
	}
}

func TestMountedSessionWorkflow(t *testing.T) {
	manager, engine, request := mountedManager(t)
	ctx := context.Background()
	view, err := manager.Start(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Record.ID.Validate(); err != nil {
		t.Fatal(err)
	}
	if view.Inspection.State != runtime.Running || view.Record.ContainerID != engine.target.ContainerID ||
		view.Record.ImageID != strings.Repeat("a", 64) || view.Record.Network != runtime.NetworkNone || view.Record.TTY {
		t.Fatalf("unexpected started session: %+v", view)
	}
	if !slices.Equal(engine.request.Command, request.Command) || engine.request.WorkspaceMode != workspace.Mount {
		t.Fatalf("runtime request changed command or workspace: %+v", engine.request)
	}
	if !slices.Equal(engine.calls, []string{"preflight", "image", "create", "start"}) {
		t.Fatalf("startup order = %v", engine.calls)
	}
	if _, err := manager.Inspect(ctx, view.Record.ID); err != nil {
		t.Fatal(err)
	}
	if err := manager.Remove(ctx, view.Record.ID); !errors.Is(err, runtime.ErrInvalidState) {
		t.Fatalf("running removal = %v", err)
	}
	if _, err := manager.Stop(ctx, view.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Store.Resolve(ctx, request.Name); err != nil {
		t.Fatalf("stop released the session: %v", err)
	}
	if err := manager.Remove(ctx, view.Record.ID); err != nil {
		t.Fatal(err)
	}
	if records, err := manager.Store.List(ctx); err != nil || len(records) != 0 {
		t.Fatalf("records after removal = %v, %v", records, err)
	}
	if _, err := manager.Store.Resolve(ctx, request.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("name remains reserved: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(request.WorkspacePath, "source.txt"))
	if err != nil || string(data) != "caller data" {
		t.Fatalf("source changed: %q, %v", data, err)
	}
	// Alias reuse creates a new immutable identity, not a restart of the old one.
	next, err := manager.Start(ctx, request)
	if err != nil || next.Record.ID == view.Record.ID {
		t.Fatalf("alias reuse = %+v, %v", next, err)
	}
}

func TestMountedStartFailureRecovery(t *testing.T) {
	cause := errors.New("injected engine failure")
	cleanupCause := errors.New("cannot verify cleanup")
	for _, tt := range []struct {
		name     string
		setup    func(*lifecycleRuntime)
		retained bool
	}{
		{name: "create failed before side effect", setup: func(f *lifecycleRuntime) { f.failAt = "create" }},
		{name: "create failed after side effect", setup: func(f *lifecycleRuntime) { f.failAt = "create"; f.createSideEffect = true }},
		{name: "start failed with workload running", setup: func(f *lifecycleRuntime) { f.failAt = "start" }},
		{name: "invalid create output", setup: func(f *lifecycleRuntime) { f.returnID = "invalid" }},
		{name: "pending ID is not a create result", setup: func(f *lifecycleRuntime) { f.returnID = PendingContainerID }},
		{name: "invalid start observation", setup: func(f *lifecycleRuntime) { f.startObservation = &runtime.Inspection{} }},
		{name: "wrong start identity", setup: func(f *lifecycleRuntime) {
			f.startObservation = &runtime.Inspection{State: runtime.Running, ContainerID: strings.Repeat("c", 64)}
		}},
		{name: "container missing during start", setup: func(f *lifecycleRuntime) { f.startObservation = &runtime.Inspection{State: runtime.Missing} }},
		{name: "rollback failure retains state", setup: func(f *lifecycleRuntime) { f.failAt = "start"; f.rollbackErr = cleanupCause }, retained: true},
		{name: "uncertain final absence retains state", setup: func(f *lifecycleRuntime) { f.failAt = "inspect"; f.startObservation = &runtime.Inspection{} }, retained: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			manager, engine, request := mountedManager(t)
			engine.cause = cause
			tt.setup(engine)
			view, err := manager.Start(context.Background(), request)
			var startup *StartError
			if view != (View{}) || !errors.As(err, &startup) || startup.RecoveryRequired != tt.retained {
				t.Fatalf("Start = %+v, %v, want StartError retained=%t", view, err, tt.retained)
			}
			if engine.failAt == "start" || engine.failAt == "create" {
				if !errors.Is(err, cause) {
					t.Fatalf("original failure was lost: %v", err)
				}
			}
			if engine.rollbackErr != nil && !errors.Is(err, cleanupCause) {
				t.Fatalf("rollback failure was lost: %v", err)
			}
			_, getErr := manager.Store.Get(context.Background(), startup.ID)
			if tt.retained && getErr != nil || !tt.retained && !errors.Is(getErr, ErrNotFound) {
				t.Fatalf("recovery record = %v, retained=%t", getErr, tt.retained)
			}
			if !engine.cleanupWasActive {
				t.Fatal("rollback did not get an active context")
			}
			if _, err := os.Stat(filepath.Join(request.WorkspacePath, "source.txt")); err != nil {
				t.Fatalf("rollback removed source: %v", err)
			}
		})
	}
}

func TestMountedStartCancellation(t *testing.T) {
	for _, stage := range []string{"create", "start"} {
		t.Run(stage, func(t *testing.T) {
			manager, engine, request := mountedManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if stage == "create" {
				engine.afterCreate = cancel
			} else {
				engine.afterStart = cancel
			}
			_, err := manager.Start(ctx, request)
			var startup *StartError
			if !errors.Is(err, context.Canceled) || !errors.As(err, &startup) || startup.RecoveryRequired || !engine.cleanupWasActive {
				t.Fatalf("cancellation recovery = %v", err)
			}
			if engine.state != runtime.Missing {
				t.Fatal("cancelled startup left a workload running")
			}
		})
	}
}

func TestMountedLifecycleRefusesUncertainCleanup(t *testing.T) {
	for _, stage := range []string{"remove failure", "still present", "inspect failure", "copy record", "ownership mismatch"} {
		t.Run(stage, func(t *testing.T) {
			manager, engine, request := mountedManager(t)
			view, err := manager.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			engine.state = runtime.Stopped
			switch stage {
			case "remove failure":
				engine.failAt, engine.cause = "remove", errors.New("remove failed")
			case "still present":
				engine.keepAfterRemove = true
			case "inspect failure":
				engine.failAt, engine.cause = "inspect", errors.New("inspect failed")
			case "ownership mismatch":
				engine.target.SessionID = "different-session"
			case "copy record":
				record := view.Record
				record.WorkspaceMode = workspace.Copy
				data, err := encodeStoreJSON(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := manager.Store.sessions.WriteFile(recordPath(record.ID), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := manager.Remove(context.Background(), view.Record.ID); err == nil {
				t.Fatal("uncertain cleanup succeeded")
			}
			if _, err := manager.Store.Get(context.Background(), view.Record.ID); err != nil {
				t.Fatalf("cleanup discarded recovery metadata: %v", err)
			}
		})
	}
}

func TestMountedStartPreconditions(t *testing.T) {
	for _, stage := range []string{"invalid name", "missing directory", "private state", "state ancestor", "empty command", "cancelled", "preflight", "image", "name collision"} {
		t.Run(stage, func(t *testing.T) {
			manager, engine, request := mountedManager(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch stage {
			case "invalid name":
				request.Name = "bad/name"
			case "missing directory":
				request.WorkspacePath = filepath.Join(request.WorkspacePath, "missing")
			case "private state":
				request.WorkspacePath = manager.Store.root.Name()
			case "state ancestor":
				request.WorkspacePath = filepath.Dir(manager.Store.root.Name())
			case "empty command":
				request.Command = nil
			case "cancelled":
				cancel()
			case "preflight", "image":
				engine.failAt, engine.cause = stage, fmt.Errorf("%s unavailable", stage)
			case "name collision":
				existing := validRecord()
				existing.Name = request.Name
				createRecord(t, manager.Store, existing)
			}
			if _, err := manager.Start(ctx, request); err == nil {
				t.Fatal("invalid startup succeeded")
			}
			if slices.Contains(engine.calls, "create") || slices.Contains(engine.calls, "rollback") {
				t.Fatalf("precondition failure touched a container: %v", engine.calls)
			}
			if stage == "name collision" {
				if _, err := manager.Store.Resolve(context.Background(), request.Name); err != nil {
					t.Fatalf("collision released another session: %v", err)
				}
			}
		})
	}
}

func TestMountedBindingFailureRetainsRecovery(t *testing.T) {
	for _, kind := range []string{"corrupt record", "changed binding", "corrupt alias"} {
		t.Run(kind, func(t *testing.T) {
			manager, engine, request := mountedManager(t)
			engine.afterCreate = func() {
				record, err := manager.Store.Get(context.Background(), ID(engine.target.SessionID))
				if err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "corrupt record":
					err = manager.Store.sessions.WriteFile(recordPath(record.ID), []byte("{"), 0o600)
				case "changed binding":
					record.ContainerID = strings.Repeat("c", 64)
					err = manager.Store.Update(context.Background(), record)
				case "corrupt alias":
					err = manager.Store.names.WriteFile(namePath(record.Name), []byte("{"), 0o600)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			view, err := manager.Start(context.Background(), request)
			var startup *StartError
			if view != (View{}) || !errors.As(err, &startup) || !startup.RecoveryRequired {
				t.Fatalf("binding recovery = %+v, %v", view, err)
			}
			if _, err := manager.Store.sessions.Lstat(recordPath(startup.ID)); err != nil {
				t.Fatalf("binding failure removed record: %v", err)
			}
			if slices.Contains(engine.calls, "start") {
				t.Fatal("workload started without a confirmed saved binding")
			}
		})
	}
}

func TestMountedStopFailureKeepsMetadata(t *testing.T) {
	for _, state := range []runtime.State{runtime.Paused, runtime.Missing} {
		t.Run(string(state), func(t *testing.T) {
			manager, engine, request := mountedManager(t)
			started, err := manager.Start(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			engine.state = state
			view, err := manager.Stop(context.Background(), started.Record.ID)
			if view != (View{}) || !errors.Is(err, runtime.ErrInvalidState) {
				t.Fatalf("Stop = %+v, %v", view, err)
			}
			if _, err := manager.Store.Resolve(context.Background(), request.Name); err != nil {
				t.Fatalf("failed stop discarded metadata: %v", err)
			}
		})
	}
}

func TestMountedShortLivedSession(t *testing.T) {
	manager, engine, request := mountedManager(t)
	engine.startState = runtime.Exited
	view, err := manager.Start(context.Background(), request)
	if err != nil || view.Inspection.State != runtime.Exited || view.Inspection.ExitCode == nil {
		t.Fatalf("short-lived result = %+v, %v", view, err)
	}
}
