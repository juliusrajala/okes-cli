package podman

import (
	"context"
	"fmt"

	"go-sesh/internal/runtime"
)

// Stop verifies container ownership and requests a stop with a 10-second grace
// period when the container is running. It returns a fresh verified observation.
// On failure it returns runtime.Inspection{} and an error. It does not delete
// the container, session metadata, workspace files, or resources. An initially
// missing or paused container, or an unacceptable final state, wraps
// runtime.ErrInvalidState.
func (c *Client) Stop(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	inspection, err := c.Inspect(ctx, target)
	if err != nil {
		return runtime.Inspection{}, err
	}

	if inspection.State == runtime.Missing || inspection.State == runtime.Paused {
		return runtime.Inspection{}, fmt.Errorf("%w: cannot stop a missing or paused container", runtime.ErrInvalidState)
	}
	target.ContainerID = inspection.ContainerID

	if inspection.State == runtime.Running {
		_, err := c.checked(ctx, "container", "stop", "--time", "10", target.ContainerID)
		if err != nil {
			return runtime.Inspection{}, err
		}
	}

	after, err := c.Inspect(ctx, target)
	if err != nil {
		return runtime.Inspection{}, err
	}

	if !after.State.StoppedOrMissing() {
		return runtime.Inspection{}, fmt.Errorf("%w: container did not stop as expected", runtime.ErrInvalidState)
	}

	return after, nil
}

// Remove verifies container ownership, removes a non-running container when
// present, and verifies absence twice. It never stops or force-removes a live
// container. It does not delete session metadata, workspace files, or resources.
// An initially missing container still needs another absence check. A refused
// state or a container that remains present wraps runtime.ErrInvalidState.
func (c *Client) Remove(ctx context.Context, target runtime.Target) error {
	return c.removeContainer(ctx, target, false)
}

// RollbackCreate force-removes only a verified session-owned container after a
// failed startup. It is not normal session removal. Uncertainty preserves state.
func (c *Client) RollbackCreate(ctx context.Context, target runtime.Target) error {
	return c.removeContainer(ctx, target, true)
}

func (c *Client) removeContainer(ctx context.Context, target runtime.Target, rollback bool) error {
	inspection, err := c.Inspect(ctx, target)
	if err != nil {
		return err
	}

	if !rollback && (inspection.State == runtime.Running || inspection.State == runtime.Paused) {
		return fmt.Errorf("%w: cannot remove a running or paused container", runtime.ErrInvalidState)
	}

	if inspection.State != runtime.Missing {
		target.ContainerID = inspection.ContainerID
		// As in the reference, a nonzero rm status can be followed by proven
		// absence. Invocation errors still fail; exit status alone is not proof.
		args := []string{"container", "rm"}
		if rollback {
			args = append(args, "--force")
		}
		args = append(args, target.ContainerID)
		_, err := c.invoke(ctx, args...)
		if err != nil {
			return err
		}

		after, err := c.Inspect(ctx, target)
		if err != nil {
			return err
		}

		if after.State != runtime.Missing {
			return fmt.Errorf("%w: container did not remove as expected", runtime.ErrInvalidState)
		}
	}

	final, err := c.Inspect(ctx, target)
	if err != nil {
		return err
	}

	if final.State != runtime.Missing {
		return fmt.Errorf("%w: container still present after removal", runtime.ErrInvalidState)
	}

	return nil
}
