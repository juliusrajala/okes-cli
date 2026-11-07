package podman

import (
	"context"
	"fmt"
	"slices"

	"go-sesh/internal/process"
	"go-sesh/internal/runtime"
)

// Exec verifies ownership and running state before invoking literal container
// argv. It pins the observed container ID. The manager must stop and verify the
// container after cancellation or transport failure; killing this host client
// alone does not prove that the workload stopped.
func (c *Client) Exec(ctx context.Context, request runtime.ExecRequest) (runtime.ExecResult, error) {
	unknown := runtime.ExecResult{ExitCode: -1}
	if err := request.Validate(); err != nil {
		return unknown, err
	}
	observed, err := c.Inspect(ctx, request.Target)
	if err != nil {
		return unknown, err
	}
	if observed.State != runtime.Running {
		return unknown, fmt.Errorf("%w: exec requires a running container", runtime.ErrInvalidState)
	}
	workdir := request.Workdir
	if workdir == "" {
		workdir = "/workspace"
	}
	if err := ctx.Err(); err != nil {
		return unknown, err
	}
	args := []string{"--remote=false", "exec", "--interactive", "--workdir", workdir, "--", observed.ContainerID}
	result, err := c.runner(ctx, process.Command{
		Path: c.executable, Env: slices.Clone(c.env),
		Args: append(args, request.Command...), Stdin: request.Stdin,
		Stdout: request.Stdout, Stderr: request.Stderr,
	})
	output := runtime.ExecResult{ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr}
	if err != nil {
		return output, fmt.Errorf("invoke Podman exec: %w", err)
	}
	if result.ExitCode < 0 || result.ExitCode > 255 {
		return output, fmt.Errorf("%w: exec returned no valid exit status", ErrInvalidResponse)
	}
	return output, nil
}

// CancelExec requests an immediate stop of a verified owned container and checks
// the result. Missing is acceptable here, unlike normal user Stop. It does not
// remove the container or metadata. Paused and uncertain states fail closed.
func (c *Client) CancelExec(ctx context.Context, target runtime.Target) (runtime.Inspection, error) {
	before, err := c.Inspect(ctx, target)
	if err != nil {
		return runtime.Inspection{}, err
	}
	if before.State == runtime.Missing {
		return before, nil
	}
	target.ContainerID = before.ContainerID
	if before.State == runtime.Running {
		if _, err := c.checked(ctx, "container", "stop", "--time", "0", target.ContainerID); err != nil {
			return runtime.Inspection{}, err
		}
	} else if !before.State.StoppedOrMissing() {
		return runtime.Inspection{}, fmt.Errorf("%w: cannot verify exec cancellation in state %s", runtime.ErrInvalidState, before.State)
	}
	after, err := c.Inspect(ctx, target)
	if err != nil {
		return runtime.Inspection{}, err
	}
	if !after.State.StoppedOrMissing() {
		return runtime.Inspection{}, fmt.Errorf("%w: exec cancellation did not stop the container", runtime.ErrInvalidState)
	}
	return after, nil
}
