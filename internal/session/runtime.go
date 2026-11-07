package session

import (
	"context"

	"go-sesh/internal/runtime"
)

// Runtime is the engine boundary consumed by the mounted-session lifecycle.
// Implementations verify ownership before acting and never infer absence from
// failed commands. RollbackCreate is reserved for failed startup, not user remove.
// Exec errors do not prove that a workload stopped. CancelExec immediately stops
// a verified container and accepts verified absence. The manager owns receipts.
// Add further capabilities only when their lifecycle consumers exist.
type Runtime interface {
	Inspector
	Preflight(context.Context, runtime.NetworkMode) (runtime.HostInfo, error)
	ImageID(context.Context, string) (string, error)
	Create(context.Context, runtime.CreateRequest) (string, error)
	StartDetached(context.Context, runtime.Target) (runtime.Inspection, error)
	Stop(context.Context, runtime.Target) (runtime.Inspection, error)
	Remove(context.Context, runtime.Target) error
	RollbackCreate(context.Context, runtime.Target) error
	Exec(context.Context, runtime.ExecRequest) (runtime.ExecResult, error)
	CancelExec(context.Context, runtime.Target) (runtime.Inspection, error)
}
