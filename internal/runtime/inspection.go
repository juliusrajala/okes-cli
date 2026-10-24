package runtime

import "errors"

var (
	// ErrOwnership means the container does not match the expected session identity.
	ErrOwnership = errors.New("container ownership does not match session")
	// ErrInvalidInspection means runtime data cannot establish a supported state.
	ErrInvalidInspection = errors.New("invalid runtime inspection")
)

// Target identifies a container for a session operation. SessionID is an opaque
// label value here; the session layer validates its UUID format. An empty
// ContainerID requests name-based recovery before the container ID is known.
// A storage-specific pending marker must not be passed as ContainerID.
type Target struct {
	SessionID     string
	ContainerName string
	ContainerID   string
}

// Inspection is a verified runtime observation, not persisted session metadata.
// ContainerID is empty only for Missing. ExitCode is available only for stopped
// or exited containers. A failed engine request must return an error, not Missing.
type Inspection struct {
	ContainerID string
	State       State
	ExitCode    *int
}

// HostInfo describes the checked local engine and host capabilities. The engine
// adapter keeps its raw JSON types private and returns only these neutral values.
type HostInfo struct {
	Version        string
	ImageArch      string
	SELinuxEnabled bool
}
