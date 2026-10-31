package session

import (
	"context"
	"fmt"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

// Stop locks the session, verifies its saved identity, and requests a verified
// container stop. It retains the container, record, alias, and source directory.
// Resource fields are not supported by the current record schema.
func (m *Manager) Stop(ctx context.Context, id ID) (View, error) {
	var view View
	err := m.Store.withSessionLock(ctx, id, func() error {
		record, err := m.Store.Get(ctx, id)
		if err != nil {
			return err
		}
		observed, err := m.Runtime.Stop(ctx, targetForRecord(record))
		if err != nil {
			return err
		}
		if err := checkObservation(record, observed); err != nil {
			return err
		}
		if !observed.State.StoppedOrMissing() {
			return fmt.Errorf("%w: stop did not establish a stopped or missing container", runtime.ErrInvalidState)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		view = View{Record: record, Inspection: observed}
		return nil
	})
	if err != nil {
		return View{}, err
	}
	return view, nil
}

// Remove removes a stopped/missing mounted session after ownership and absence
// checks. It never stops a live container implicitly or deletes the source.
// Copy sessions are refused until their owned-workspace cleanup is implemented.
func (m *Manager) Remove(ctx context.Context, id ID) error {
	return m.Store.withSessionLock(ctx, id, func() error {
		record, err := m.Store.Get(ctx, id)
		if err != nil {
			return err
		}
		if record.WorkspaceMode != workspace.Mount {
			return fmt.Errorf("%w: copy-session cleanup is not implemented", ErrUnsafeStore)
		}
		if err := m.Runtime.Remove(ctx, targetForRecord(record)); err != nil {
			return err
		}
		if err := m.verifyAbsent(ctx, record); err != nil {
			return err
		}
		return m.Store.Delete(ctx, record)
	})
}

func (m *Manager) verifyAbsent(ctx context.Context, record Record) error {
	observed, err := m.Runtime.Inspect(ctx, targetForRecord(record))
	if err != nil {
		return err
	}
	if err := checkObservation(record, observed); err != nil {
		return err
	}
	if observed.State != runtime.Missing {
		return fmt.Errorf("%w: refusing metadata deletion without verified container absence", runtime.ErrInvalidState)
	}
	return nil
}

func targetForRecord(record Record) runtime.Target {
	target := runtime.Target{SessionID: string(record.ID), ContainerName: record.ContainerName}
	if record.ContainerID != PendingContainerID {
		target.ContainerID = record.ContainerID
	}
	return target
}

// Engine implementations own label verification. Lifecycle code also refuses
// malformed observations so a bad adapter cannot authorize metadata deletion.
func checkObservation(record Record, observed runtime.Inspection) error {
	if !observed.State.Valid() {
		return fmt.Errorf("%w: unknown observed state", runtime.ErrInvalidInspection)
	}
	if observed.State == runtime.Missing {
		if observed.ContainerID != "" || observed.ExitCode != nil {
			return fmt.Errorf("%w: missing container includes identity or exit data", runtime.ErrInvalidInspection)
		}
		return nil
	}
	if !immutableIDPattern.MatchString(observed.ContainerID) || observed.ContainerID == PendingContainerID {
		return fmt.Errorf("%w: observed container ID is invalid", runtime.ErrInvalidInspection)
	}
	if record.ContainerID != PendingContainerID && observed.ContainerID != record.ContainerID {
		return runtime.ErrOwnership
	}
	ended := observed.State == runtime.Exited || observed.State == runtime.Stopped
	if ended != (observed.ExitCode != nil) {
		return fmt.Errorf("%w: observed exit code does not match state", runtime.ErrInvalidInspection)
	}
	return nil
}
