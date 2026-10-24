package session

import (
	"context"

	"go-sesh/internal/runtime"
)

// Inspector is the runtime capability this manager currently consumes. The
// implementation must verify engine policy, container identity, and ownership
// before returning a state. Podman satisfies this interface without importing
// this package. Tests can supply another implementation with the same method.
type Inspector interface {
	Inspect(context.Context, runtime.Target) (runtime.Inspection, error)
}

// Manager coordinates saved session identity and runtime operations. Construct it
// with a non-nil Store and Runtime. It owns no CLI, JSON rendering, or scheduling.
type Manager struct {
	Store   *Store
	Runtime Inspector
}

// View keeps persisted facts separate from the runtime's current observation.
// A pending saved container ID can coexist with a verified observed ID. Neither
// inspecting nor listing rewrites records or releases reserved session names.
type View struct {
	Record     Record
	Inspection runtime.Inspection
}

// Inspect returns a session's saved record and verified current container state.
// Its input is the immutable session ID, not a human-readable alias. A caller can
// resolve an alias through the store first. It does not start, stop, remove, or
// repair anything. On failure it returns View{} and the original error.
func (m *Manager) Inspect(ctx context.Context, id ID) (View, error) {
	record, err := m.Store.Get(ctx, id)
	if err != nil {
		return View{}, err
	}

	containerID := ""
	if record.ContainerID != PendingContainerID {
		containerID = record.ContainerID
	}

	runtimeTarget := runtime.Target{
		SessionID:     string(record.ID),
		ContainerID:   containerID,
		ContainerName: record.ContainerName,
	}

	inspection, err := m.Runtime.Inspect(ctx, runtimeTarget)
	if err != nil {
		return View{}, err
	}

	return View{Record: record, Inspection: inspection}, err
}

// List returns inspected views of all saved sessions. Later CLI and API adapters
// can render these views without implementing lifecycle or engine policy. The
// operation reads metadata and runtime state only. Any error fails the entire
// query with a nil result, not a partial list. It is not a multi-session snapshot.
func (m *Manager) List(ctx context.Context) ([]View, error) {
	records, err := m.Store.List(ctx)
	if err != nil {
		return nil, err
	}

	var views []View
	for _, runtimeRecord := range records {
		view, err := m.Inspect(ctx, runtimeRecord.ID)
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	return views, nil
}
