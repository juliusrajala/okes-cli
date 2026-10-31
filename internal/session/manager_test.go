package session

import (
	"context"
	"errors"
	"testing"

	"go-sesh/internal/runtime"
)

type inspectorFunc func(context.Context, runtime.Target) (runtime.Inspection, error)

func (f inspectorFunc) Inspect(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	return f(ctx, target)
}

func savedManager(t *testing.T, record Record, inspector Runtime) *Manager {
	t.Helper()
	store := testStore(t, t.TempDir())
	createRecord(t, store, record)
	return &Manager{Store: store, Runtime: inspector}
}

func requireManagerError(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestManagerInspect(t *testing.T) {
	for _, known := range []bool{false, true} {
		name := "pending container ID"
		if known {
			name = "known container ID"
		}
		t.Run(name, func(t *testing.T) {
			record := validRecord()
			if known {
				record.ContainerID = record.ImageID
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			observed := runtime.Inspection{ContainerID: record.ImageID, State: runtime.Running}
			manager := savedManager(t, record, inspectorFunc(func(gotCtx context.Context, target runtime.Target) (runtime.Inspection, error) {
				calls++
				want := runtime.Target{SessionID: string(record.ID), ContainerName: record.ContainerName}
				if known {
					want.ContainerID = record.ContainerID
				}
				if target != want || gotCtx != ctx {
					t.Fatalf("target = %+v, want %+v with the original context", target, want)
				}
				return observed, nil
			}))
			view, err := manager.Inspect(ctx, record.ID)
			if err != nil || view.Record != record || view.Inspection != observed || calls != 1 {
				t.Fatalf("view = %+v, calls = %d, error = %v", view, calls, err)
			}
			after, err := manager.Store.Get(context.Background(), record.ID)
			if err != nil || after != record {
				t.Fatalf("inspection rewrote saved metadata: %+v, %v", after, err)
			}
		})
	}
	t.Run("missing container retains record", func(t *testing.T) {
		record := validRecord()
		manager := savedManager(t, record, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			return runtime.Inspection{State: runtime.Missing}, nil
		}))
		view, err := manager.Inspect(context.Background(), record.ID)
		if err != nil || view.Record != record || view.Inspection.State != runtime.Missing {
			t.Fatalf("view = %+v, error = %v", view, err)
		}
		if _, err := manager.Store.Resolve(context.Background(), record.Name); err != nil {
			t.Fatalf("missing container caused metadata cleanup: %v", err)
		}
	})
	for _, cause := range []error{runtime.ErrOwnership, runtime.ErrInvalidInspection, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			record := validRecord()
			manager := savedManager(t, record, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
				return runtime.Inspection{}, cause
			}))
			view, err := manager.Inspect(context.Background(), record.ID)
			requireManagerError(t, err, cause)
			if view != (View{}) {
				t.Fatalf("failure returned a usable view: %+v", view)
			}
			if _, err := manager.Store.Resolve(context.Background(), record.Name); err != nil {
				t.Fatalf("failed inspection changed reservation: %v", err)
			}
		})
	}
	t.Run("bad metadata prevents runtime call", func(t *testing.T) {
		record := validRecord()
		manager := savedManager(t, record, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			t.Fatal("runtime must not be called for corrupt metadata")
			return runtime.Inspection{}, nil
		}))
		if err := manager.Store.sessions.WriteFile(recordPath(record.ID), []byte(`{`), 0o600); err != nil {
			t.Fatal(err)
		}
		view, err := manager.Inspect(context.Background(), record.ID)
		requireManagerError(t, err, ErrCorruptRecord)
		if view != (View{}) {
			t.Fatalf("corrupt metadata returned view: %+v", view)
		}
	})
	t.Run("unknown ID prevents runtime call", func(t *testing.T) {
		record := validRecord()
		manager := savedManager(t, record, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			t.Fatal("runtime must not be called for a missing record")
			return runtime.Inspection{}, nil
		}))
		view, err := manager.Inspect(context.Background(), ID("8ad20f51-6067-4dc4-a072-54c94d7735de"))
		requireManagerError(t, err, ErrNotFound)
		if view != (View{}) {
			t.Fatalf("missing record returned view: %+v", view)
		}
	})
	t.Run("cancelled before lookup", func(t *testing.T) {
		record := validRecord()
		manager := savedManager(t, record, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			t.Fatal("runtime must not be called after cancellation")
			return runtime.Inspection{}, nil
		}))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		view, err := manager.Inspect(ctx, record.ID)
		requireManagerError(t, err, context.Canceled)
		if view != (View{}) {
			t.Fatalf("cancellation returned view: %+v", view)
		}
	})
}

func TestManagerList(t *testing.T) {
	t.Run("empty store", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		manager := &Manager{Store: store, Runtime: inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			t.Fatal("empty store must not query the runtime")
			return runtime.Inspection{}, nil
		})}
		views, err := manager.List(context.Background())
		if err != nil || len(views) != 0 {
			t.Fatalf("views = %v, error = %v", views, err)
		}
	})
	t.Run("inspect every saved session", func(t *testing.T) {
		first := validRecord()
		calls := 0
		manager := savedManager(t, first, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			calls++
			return runtime.Inspection{State: runtime.Missing}, nil
		}))
		second := first
		second.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
		second.Name = "second-session"
		second.ContainerName = "pi-sesh-" + string(second.ID)
		createRecord(t, manager.Store, second)
		views, err := manager.List(context.Background())
		if err != nil || len(views) != 2 || calls != 2 {
			t.Fatalf("views = %v, calls = %d, error = %v", views, calls, err)
		}
		if views[0].Record.ID != first.ID || views[1].Record.ID != second.ID {
			t.Fatalf("unexpected view order: %+v", views)
		}
		for _, view := range views {
			if view.Inspection.State != runtime.Missing {
				t.Fatalf("missing live state: %+v", view)
			}
			if _, err := manager.Store.Resolve(context.Background(), view.Record.Name); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("runtime failure discards partial list", func(t *testing.T) {
		first := validRecord()
		calls := 0
		manager := savedManager(t, first, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			calls++
			if calls == 2 {
				return runtime.Inspection{}, runtime.ErrOwnership
			}
			return runtime.Inspection{State: runtime.Missing}, nil
		}))
		second := first
		second.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
		second.Name = "second-session"
		second.ContainerName = "pi-sesh-" + string(second.ID)
		createRecord(t, manager.Store, second)
		views, err := manager.List(context.Background())
		requireManagerError(t, err, runtime.ErrOwnership)
		if views != nil {
			t.Fatalf("failure returned partial views: %+v", views)
		}
	})
	t.Run("corrupt metadata prevents inspection", func(t *testing.T) {
		record := validRecord()
		manager := savedManager(t, record, inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			t.Fatal("corrupt list must not query the runtime")
			return runtime.Inspection{}, nil
		}))
		if err := manager.Store.sessions.WriteFile(recordPath(record.ID), []byte(`{`), 0o600); err != nil {
			t.Fatal(err)
		}
		views, err := manager.List(context.Background())
		requireManagerError(t, err, ErrCorruptRecord)
		if views != nil {
			t.Fatalf("corrupt store returned views: %+v", views)
		}
	})
	t.Run("cancelled list", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		manager := &Manager{Store: store, Runtime: inspectorFunc(func(context.Context, runtime.Target) (runtime.Inspection, error) {
			t.Fatal("cancelled list must not query the runtime")
			return runtime.Inspection{}, nil
		})}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		views, err := manager.List(ctx)
		requireManagerError(t, err, context.Canceled)
		if views != nil {
			t.Fatalf("cancellation returned views: %+v", views)
		}
	})
}
