package runtime

import "go-sesh/internal/workspace"

// CreateRequest describes a container to create, not a complete session start.
// The caller must reserve session identity and save recovery metadata before
// creation. Target.ContainerID must be empty because no container is bound yet.
// SessionID remains opaque here; the session layer validates its UUID format.
//
// WorkspacePath is a host path selected by the caller, not project configuration.
// Before invocation, workspace preparation must resolve it to an existing
// canonical directory. A copy workspace requires separately verified ownership.
// This request does not prepare directories or grant permission to delete them.
//
// Command is literal in-container argv. Environment contains only explicitly
// selected container variables, not the host process environment. Network and
// WorkspaceMode require explicit values; their zero values are invalid.
// UID/GID, SELinux settings, and terminal environment come from trusted host
// settings, not this request. No arbitrary runtime flags or extra mounts are allowed.
// Callers must not modify the slices or map while an operation uses the request.
// The request has no persistence schema or CLI/JSON representation.
type CreateRequest struct {
	Target        Target
	Name          string
	ImageID       string
	WorkspacePath string
	WorkspaceMode workspace.Mode
	Network       NetworkMode
	Command       []string
	Environment   map[string]string
	TTY           bool
	Interactive   bool
}
