package session

import (
	"context"
	"errors"

	"go-sesh/internal/runtime"
)

func (inspectorFunc) Exec(context.Context, runtime.ExecRequest) (runtime.ExecResult, error) {
	return runtime.ExecResult{ExitCode: -1}, errors.New("unexpected exec in read-only test")
}

func (inspectorFunc) CancelExec(context.Context, runtime.Target) (runtime.Inspection, error) {
	return runtime.Inspection{}, errors.New("unexpected exec cancellation in read-only test")
}

// Read-only fixtures must never invoke a lifecycle mutation or image probe.
func (inspectorFunc) Preflight(context.Context, runtime.NetworkMode) (runtime.HostInfo, error) {
	return runtime.HostInfo{}, errors.New("unexpected preflight in read-only test")
}

func (inspectorFunc) ImageID(context.Context, string) (string, error) {
	return "", errors.New("unexpected image query in read-only test")
}

func (inspectorFunc) Create(context.Context, runtime.CreateRequest) (string, error) {
	return "", errors.New("unexpected create in read-only test")
}

func (inspectorFunc) StartDetached(context.Context, runtime.Target) (runtime.Inspection, error) {
	return runtime.Inspection{}, errors.New("unexpected start in read-only test")
}

func (inspectorFunc) Stop(context.Context, runtime.Target) (runtime.Inspection, error) {
	return runtime.Inspection{}, errors.New("unexpected stop in read-only test")
}

func (inspectorFunc) Remove(context.Context, runtime.Target) error {
	return errors.New("unexpected remove in read-only test")
}

func (inspectorFunc) RollbackCreate(context.Context, runtime.Target) error {
	return errors.New("unexpected rollback in read-only test")
}
