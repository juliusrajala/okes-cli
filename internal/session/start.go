package session

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

// StartRequest selects a detached mounted session. Image must already be local.
// WorkspacePath is a caller-selected host directory, never a project-config
// mount directive. Empty Network defaults to none. No config file is loaded.
// Copy workspaces, resources, interactive attachment, and TTY are not supported
// by this request. Command and Environment are literal container inputs.
type StartRequest struct {
	Name          string
	Image         string
	WorkspacePath string
	Command       []string
	Environment   map[string]string
	Network       runtime.NetworkMode
}

// StartError identifies an allocated startup attempt. RecoveryRequired means
// metadata may remain reserved; inspect the ID before retrying. Unwrap preserves
// both the original failure and any rollback failure for errors.Is/errors.As.
type StartError struct {
	ID               ID
	Name             string
	RecoveryRequired bool
	Err              error
}

// Error describes the startup failure and whether recovery may be required.
func (e *StartError) Error() string {
	if e.RecoveryRequired {
		return fmt.Sprintf("start session %q (%s) failed; state may remain reserved for recovery: %v", e.Name, e.ID, e.Err)
	}
	return fmt.Sprintf("start session %q (%s): %v", e.Name, e.ID, e.Err)
}

// Unwrap returns the startup and recovery error chain.
func (e *StartError) Unwrap() error { return e.Err }

// Start creates and starts one detached mounted session. Recovery metadata is
// durable before container creation. Failures roll back only after ownership and
// absence checks; uncertain cleanup preserves the reservation. The caller owns
// Store and Runtime. Start never removes the supplied workspace directory.
func (m *Manager) Start(ctx context.Context, request StartRequest) (View, error) {
	if err := validateStartRequest(ctx, request); err != nil {
		return View{}, err
	}
	if request.Network == "" {
		request.Network = runtime.NetworkNone
	}
	request.Command = slices.Clone(request.Command)
	request.Environment = maps.Clone(request.Environment)
	path, err := workspace.PrepareMount(ctx, request.WorkspacePath)
	if err != nil {
		return View{}, err
	}
	statePath, err := filepath.EvalSymlinks(m.Store.root.Name())
	if err != nil {
		return View{}, fmt.Errorf("resolve private session state: %w", err)
	}
	if pathsOverlap(path, statePath) {
		return View{}, fmt.Errorf("workspace must not expose private session state")
	}
	if _, err := m.Runtime.Preflight(ctx, request.Network); err != nil {
		return View{}, err
	}
	imageID, err := m.Runtime.ImageID(ctx, request.Image)
	if err != nil {
		return View{}, err
	}
	id, err := newID()
	if err != nil {
		return View{}, err
	}
	record := Record{
		Version: RecordVersion, ID: id, Name: request.Name,
		ContainerName: "pi-sesh-" + string(id), ContainerID: PendingContainerID,
		ProjectDir: path, WorkspacePath: path, WorkspaceMode: workspace.Mount,
		ImageID: imageID, Network: request.Network, TTY: false, CreatedAt: time.Now().UTC(),
	}
	if err := record.Validate(); err != nil {
		return View{}, err
	}
	var view View
	err = m.Store.withSessionLock(ctx, id, func() error {
		var err error
		view, err = m.startMounted(ctx, record, request)
		return err
	})
	if err != nil {
		var startup *StartError
		if errors.As(err, &startup) {
			return View{}, err
		}
		return View{}, &StartError{ID: id, Name: request.Name, RecoveryRequired: view.Record.ID != "", Err: err}
	}
	return view, nil
}

func validateStartRequest(ctx context.Context, request StartRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateName(request.Name); err != nil {
		return err
	}
	if request.Image == "" || strings.ContainsRune(request.Image, '\x00') {
		return fmt.Errorf("a local image reference is required")
	}
	if request.Network != "" {
		if err := request.Network.Validate(); err != nil {
			return err
		}
	}
	if len(request.Command) == 0 {
		return fmt.Errorf("a nonempty literal container command is required")
	}
	for _, arg := range request.Command {
		if arg == "" || strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("container command arguments must be nonempty and contain no NUL bytes")
		}
	}
	return nil
}

func (m *Manager) startMounted(ctx context.Context, record Record, request StartRequest) (view View, err error) {
	if err := m.Store.Create(ctx, record); err != nil {
		// A publication/sync failure is uncertain. No engine mutation has run,
		// but another identity must never be deleted after a collision.
		return View{}, &StartError{ID: record.ID, Name: record.Name, RecoveryRequired: !errors.Is(err, ErrAlreadyExists), Err: err}
	}
	defer func() {
		if err == nil {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		cleanupErr := m.rollbackStart(cleanupCtx, record)
		view = View{}
		err = &StartError{ID: record.ID, Name: record.Name, RecoveryRequired: cleanupErr != nil, Err: errors.Join(err, cleanupErr)}
	}()

	containerID, err := m.Runtime.Create(ctx, runtime.CreateRequest{
		Target: targetForRecord(record), Name: record.Name, ImageID: record.ImageID,
		WorkspacePath: record.WorkspacePath, WorkspaceMode: workspace.Mount,
		Network: record.Network, Command: request.Command, Environment: request.Environment,
	})
	if err != nil {
		return View{}, err
	}
	if !immutableIDPattern.MatchString(containerID) || containerID == PendingContainerID {
		return View{}, fmt.Errorf("%w: create returned no real container ID", runtime.ErrInvalidInspection)
	}
	record.ContainerID = containerID
	if err := m.Store.Update(ctx, record); err != nil {
		return View{}, err
	}
	observed, err := m.Runtime.StartDetached(ctx, targetForRecord(record))
	if err != nil {
		return View{}, err
	}
	if err := checkObservation(record, observed); err != nil {
		return View{}, err
	}
	if observed.State == runtime.Missing {
		return View{}, fmt.Errorf("%w: container disappeared during startup", runtime.ErrInvalidState)
	}
	if err := ctx.Err(); err != nil {
		return View{}, err
	}
	return View{Record: record, Inspection: observed}, nil
}

func (m *Manager) rollbackStart(ctx context.Context, attempt Record) error {
	current, err := m.Store.Get(ctx, attempt.ID)
	if err != nil {
		return fmt.Errorf("verify startup recovery record: %w", err)
	}
	fixed := attempt
	fixed.ContainerID = current.ContainerID
	if !sameRecord(current, fixed) || (current.ContainerID != PendingContainerID && current.ContainerID != attempt.ContainerID) {
		return fmt.Errorf("%w: startup recovery record changed", ErrUnsafeStore)
	}
	target := targetForRecord(attempt)
	if err := m.Runtime.RollbackCreate(ctx, target); err != nil {
		return fmt.Errorf("startup container rollback: %w", err)
	}
	if err := m.verifyAbsent(ctx, attempt); err != nil {
		return err
	}
	return m.Store.Delete(ctx, current)
}

func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

func newID() (ID, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return ID(fmt.Sprintf("%x-%x-%x-%x-%x", bytes[:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:])), nil
}
