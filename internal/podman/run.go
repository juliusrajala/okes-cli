package podman

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go-sesh/internal/runtime"
	"go-sesh/internal/workspace"
)

// prepareCreate validates before invoking the engine. Workspace preparation must
// not silently change the path recorded by the caller. Copy ownership is not
// implemented yet, so live creation accepts mounted workspaces only.
func (c *Client) prepareCreate(ctx context.Context, request runtime.CreateRequest) ([]string, error) {
	if _, err := validateCreateRequest(request, c.creationHost(runtime.HostInfo{})); err != nil {
		return nil, err
	}
	if request.WorkspaceMode != workspace.Mount {
		return nil, fmt.Errorf("container creation currently supports mount workspaces only")
	}
	path, err := workspace.PrepareMount(ctx, request.WorkspacePath)
	if err != nil {
		return nil, err
	}
	if path != request.WorkspacePath {
		return nil, fmt.Errorf("workspace identity changed after preparation")
	}
	host, err := c.Preflight(ctx, request.Network)
	if err != nil {
		return nil, err
	}
	return buildCreateArgs(request, c.creationHost(host))
}

func (c *Client) creationHost(info runtime.HostInfo) createHost {
	host := createHost{UID: os.Getuid(), GID: os.Getgid(), SELinuxEnabled: info.SELinuxEnabled}
	for _, entry := range c.env {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "TERM":
			host.Term = value
		case "COLORTERM":
			host.ColorTerm = value
		}
	}
	return host
}

// Create creates a container from a prepared request and returns its immutable
// ID. The caller must save a pending recovery record before calling this method.
// An error does not prove that no container was created. The session manager
// owns rollback; this method must not guess absence or delete recovery state.
func (c *Client) Create(ctx context.Context, request runtime.CreateRequest) (string, error) {
	prepared, err := c.prepareCreate(ctx, request)
	if err != nil {
		return "", err
	}

	result, err := c.checked(ctx, prepared...)
	if err != nil {
		return "", err
	}

	containerID, err := parseContainerID(result.Stdout)
	if err != nil {
		return "", err
	}

	return containerID, nil
}

// StartDetached starts a verified, newly created container without attaching.
// It never restarts an exited/stopped container. It returns the verified observed
// state, which can already be exited for a short-lived workload. On failure it
// returns a zero inspection and an error. The manager owns cancellation recovery.
func (c *Client) StartDetached(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	before, err := c.Inspect(ctx, target)
	if err != nil {
		return runtime.Inspection{}, err
	}
	if before.State != runtime.Created {
		return runtime.Inspection{}, fmt.Errorf("%w: only a newly created container can be started", runtime.ErrInvalidState)
	}
	target.ContainerID = before.ContainerID
	if _, err := c.checked(ctx, "container", "start", target.ContainerID); err != nil {
		return runtime.Inspection{}, err
	}
	after, err := c.Inspect(ctx, target)
	if err != nil {
		return runtime.Inspection{}, err
	}
	if after.State == runtime.Missing {
		return runtime.Inspection{}, fmt.Errorf("%w: container disappeared during startup", runtime.ErrInvalidState)
	}
	return after, nil
}
