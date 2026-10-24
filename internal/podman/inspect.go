package podman

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"go-sesh/internal/runtime"
)

const (
	ownerLabel   = "io.pi-sesh.owner"
	sessionLabel = "io.pi-sesh.session-id"
)

var containerIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{64}$`)

// Pointer fields distinguish missing/null engine data from valid zero values,
// such as exit code 0. Absence of required data must not imply a safe state.
type containerJSON struct {
	ID    string `json:"Id"`
	State *struct {
		Status   *string `json:"Status"`
		ExitCode *int    `json:"ExitCode"`
	} `json:"State"`
	Config *struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	Labels map[string]string `json:"Labels"`
}

// Inspect verifies the local engine, checks container presence, and validates
// identity, ownership labels, and state. It never starts, stops, or removes a
// container. Missing is returned only for Podman's documented exists status 1.
func (c *Client) Inspect(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	if err := validateTarget(target); err != nil {
		return runtime.Inspection{}, err
	}
	if _, err := c.Preflight(ctx, runtime.NetworkNone); err != nil {
		return runtime.Inspection{}, err
	}
	// Prefer immutable identity. A name can be reused, so name lookup is only
	// for the recovery case where no container ID has been saved yet.
	selector := target.ContainerID
	if selector == "" {
		selector = target.ContainerName
	}
	exists, err := c.containerExists(ctx, selector)
	if err != nil {
		return runtime.Inspection{}, err
	}
	if !exists {
		return runtime.Inspection{State: runtime.Missing}, nil
	}
	// The container can disappear between exists and inspect. An inspect failure
	// remains uncertainty; it is not a new, verified absence observation.
	result, err := c.checked(ctx, "container", "inspect", "--format", "json", selector)
	if err != nil {
		return runtime.Inspection{}, err
	}
	return parseInspection(result.Stdout, target)
}

func validateTarget(target runtime.Target) error {
	if target.SessionID == "" || strings.ContainsAny(target.SessionID, "\x00\r\n") || target.ContainerName != "pi-sesh-"+target.SessionID {
		return fmt.Errorf("invalid runtime target: session ID and container name must agree")
	}
	// The all-zero storage marker is not a real selector. Querying it could
	// report absence while the session's named container is actually present.
	if target.ContainerID != "" && (!containerIDPattern.MatchString(target.ContainerID) || target.ContainerID == strings.Repeat("0", 64)) {
		return fmt.Errorf("invalid runtime target: use a real container ID or an empty ID for name lookup")
	}
	return nil
}

func (c *Client) containerExists(ctx context.Context, selector string) (bool, error) {
	result, err := c.invoke(ctx, "container", "exists", selector)
	if err != nil {
		return false, err
	}
	// Only status 1 proves absence. Treating every nonzero status as missing
	// would turn permission, storage, or engine failures into unsafe conclusions.
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, fmt.Errorf("%w: container exists exited with status %d", ErrCommandFailed, result.ExitCode)
	}
}

func parseInspection(output string, target runtime.Target) (runtime.Inspection, error) {
	var item containerJSON
	// CLI inspection normally returns an array. Support the reference's object
	// form too, but require one unambiguous container rather than picking among many.
	if strings.HasPrefix(strings.TrimSpace(output), "[") {
		var items []containerJSON
		if err := decodeResponse(output, &items); err != nil {
			return runtime.Inspection{}, fmt.Errorf("%w: %w", runtime.ErrInvalidInspection, err)
		}
		if len(items) != 1 {
			return runtime.Inspection{}, fmt.Errorf("%w: expected one container", runtime.ErrInvalidInspection)
		}
		item = items[0]
	} else if err := decodeResponse(output, &item); err != nil {
		return runtime.Inspection{}, fmt.Errorf("%w: %w", runtime.ErrInvalidInspection, err)
	}
	if !containerIDPattern.MatchString(item.ID) || item.ID == strings.Repeat("0", 64) || item.State == nil || item.State.Status == nil || item.State.ExitCode == nil {
		return runtime.Inspection{}, fmt.Errorf("%w: incomplete container identity or state", runtime.ErrInvalidInspection)
	}
	// Support both engine label locations. A provided Config.Labels map takes
	// precedence, even if empty; do not fall back to different ownership claims.
	labels := item.Labels
	if item.Config != nil && item.Config.Labels != nil {
		labels = item.Config.Labels
	}
	// Existence does not establish ownership. Check both the tool marker and
	// the specific session label before returning state. A known ID must also match.
	if labels[ownerLabel] != "pi-sesh" || labels[sessionLabel] != target.SessionID || (target.ContainerID != "" && item.ID != target.ContainerID) {
		return runtime.Inspection{}, runtime.ErrOwnership
	}
	// Normalize engine vocabulary at this boundary. Unknown states must be
	// errors, not guesses that could later authorize cleanup.
	var state runtime.State
	switch strings.ToLower(*item.State.Status) {
	case "running":
		state = runtime.Running
	case "exited":
		state = runtime.Exited
	case "stopped":
		state = runtime.Stopped
	case "created", "configured":
		state = runtime.Created
	case "paused":
		state = runtime.Paused
	default:
		return runtime.Inspection{}, fmt.Errorf("%w: unsupported state %q", runtime.ErrInvalidInspection, *item.State.Status)
	}
	inspection := runtime.Inspection{ContainerID: item.ID, State: state}
	// Engines can report a placeholder exit code for a live container. Expose
	// it only after exit so callers do not confuse zero with completed success.
	if state == runtime.Exited || state == runtime.Stopped {
		inspection.ExitCode = item.State.ExitCode
	}
	return inspection, nil
}
