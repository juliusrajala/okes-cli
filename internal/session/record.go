package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

// RecordVersion distinguishes the Go directory-workspace schema from TypeScript
// version 1. Existing TypeScript records require an explicit compatibility path.
const RecordVersion = 2

// PendingContainerID marks recovery metadata saved before container creation.
// It is not evidence that a container is absent. Runtime inspection must resolve
// the session's container name and verify ownership.
const PendingContainerID = "0000000000000000000000000000000000000000000000000000000000000000"

// Record holds persisted session facts, not live container state. JSON tags keep
// the on-disk field names explicit. Workspace ownership needs separate proof;
// a valid path in this record is never permission to delete that directory.
// Resource metadata will be added with the resource subsystem. Unknown fields
// are rejected on load, so this store cannot silently discard such metadata.
type Record struct {
	Version       int                 `json:"version"`
	ID            ID                  `json:"id"`
	Name          string              `json:"name"`
	ContainerName string              `json:"containerName"`
	ContainerID   string              `json:"containerId"`
	ProjectDir    string              `json:"projectDir"`
	WorkspaceMode workspace.Mode      `json:"workspaceMode"`
	WorkspacePath string              `json:"workspacePath"`
	ImageID       string              `json:"imageId"`
	Network       runtime.NetworkMode `json:"network"`
	TTY           bool                `json:"tty"`
	CreatedAt     time.Time           `json:"createdAt"`
}

var immutableIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)

// Validate checks the supported record schema without accessing files or Podman.
// It does not establish container ownership, workspace ownership, or existence.
func (r Record) Validate() error {
	if r.Version != RecordVersion {
		return fmt.Errorf("%w: unsupported record version %d, expected %d", ErrInvalidRecord, r.Version, RecordVersion)
	}
	if err := r.ID.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}
	if err := ValidateName(r.Name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}
	if r.ContainerName != "pi-sesh-"+string(r.ID) {
		return fmt.Errorf("%w: container name does not match session ID", ErrInvalidRecord)
	}
	if !immutableIDPattern.MatchString(r.ContainerID) {
		return fmt.Errorf("%w: container ID must contain 64 hexadecimal digits", ErrInvalidRecord)
	}
	if !immutableIDPattern.MatchString(r.ImageID) {
		return fmt.Errorf("%w: image ID must contain 64 hexadecimal digits", ErrInvalidRecord)
	}
	for _, path := range []string{r.ProjectDir, r.WorkspacePath} {
		if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
			return fmt.Errorf("%w: project and workspace paths must be absolute and contain no NUL bytes", ErrInvalidRecord)
		}
	}
	if err := r.WorkspaceMode.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}
	if err := r.Network.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRecord, err)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: creation time is required", ErrInvalidRecord)
	}
	return nil
}
