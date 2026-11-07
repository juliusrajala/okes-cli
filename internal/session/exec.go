package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"go-sesh/internal/runtime"
)

// ExecRequest binds a human name to the immutable session selected by a caller.
// Timeout is optional; zero disables it, and positive values cannot exceed 24h.
// Workdir is a container path. Streams are borrowed for the duration of Exec.
type ExecRequest struct {
	Name      string
	SessionID ID
	Command   []string
	Workdir   string
	Timeout   time.Duration
	Stdin     io.Reader
	Stdout    io.Writer
	Stderr    io.Writer
}

func (r ExecRequest) validate() error {
	if err := ValidateName(r.Name); err != nil {
		return err
	}
	if err := r.SessionID.Validate(); err != nil {
		return err
	}
	if r.Timeout < 0 || r.Timeout > 24*time.Hour {
		return fmt.Errorf("execution timeout must be positive and no more than 86400 seconds, or omitted")
	}
	return (runtime.ExecRequest{Command: r.Command, Workdir: r.Workdir}).Validate()
}

func (r ExecRequest) workload(record Record) runtime.ExecRequest {
	return runtime.ExecRequest{
		Target: targetForRecord(record), Command: r.Command, Workdir: r.Workdir,
		Stdin: r.Stdin, Stdout: r.Stdout, Stderr: r.Stderr,
	}
}

// ExecResult keeps workload output separate from its durable execution receipt.
// Partial output and a receipt can remain available when Exec returns an error.
type ExecResult struct {
	runtime.ExecResult
	Receipt ExecutionReceipt
}

// Exec verifies the requested name/ID pair and serializes the execution with
// session mutations. It requires a bound, owned, running container. Nonzero
// workload exits are results; transport and recovery failures are errors.
func (m *Manager) Exec(ctx context.Context, request ExecRequest) (ExecResult, error) {
	result := ExecResult{ExecResult: runtime.ExecResult{ExitCode: -1}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := request.validate(); err != nil {
		return result, err
	}
	request.Command = slices.Clone(request.Command)
	err := m.Store.withSessionLock(ctx, request.SessionID, func() error {
		record, err := m.Store.Resolve(ctx, request.Name)
		if err != nil {
			return err
		}
		if record.ID != request.SessionID {
			return fmt.Errorf("%w: session ID does not match the named session", runtime.ErrOwnership)
		}
		if record.ContainerID == PendingContainerID {
			return fmt.Errorf("%w: exec requires a bound container ID", runtime.ErrInvalidState)
		}
		observed, err := m.Runtime.Inspect(ctx, targetForRecord(record))
		if err != nil {
			return err
		}
		if err := checkObservation(record, observed); err != nil {
			return err
		}
		if observed.State != runtime.Running {
			return fmt.Errorf("%w: exec requires a running container", runtime.ErrInvalidState)
		}
		result, err = m.execLocked(ctx, record, request)
		return err
	})
	return result, err
}

// execLocked runs one durable execution transaction while the session lock is
// held. The record already matches the requested name/ID and a verified running
// container. It retains partial output and never removes session metadata.
func (m *Manager) execLocked(ctx context.Context, record Record, request ExecRequest) (result ExecResult, err error) {
	// No exit status exists before invocation.
	result = ExecResult{ExecResult: runtime.ExecResult{ExitCode: -1}}
	// Publish the recovery receipt before starting the workload.
	receipt, err := newExecution(record.ID)
	if err != nil {
		return result, err
	}
	err = m.Store.createExecution(ctx, receipt)
	if err != nil {
		return result, err
	}

	// Keep the saved running state for the storage update's identity check.
	receiptCopy := receipt
	workCtx := ctx

	if request.Timeout > 0 {
		cancelCtx, cancel := context.WithTimeout(workCtx, request.Timeout)
		// This belongs to execLocked, not to the if block. It runs when
		// execLocked exits and releases the workload timer's resources.
		defer cancel()
		workCtx = cancelCtx
	}

	execAttempted := false

	// Register this after the workload cancel so it runs first on return.
	// The body does not run yet. It reads the results after Runtime.Exec ends.
	// Named return values let it update the result and error sent to the caller.
	defer func() {
		// Read the context before our own deferred cancel changes its state.
		if workCtx.Err() != nil {
			err = errors.Join(err, workCtx.Err())
		}

		if execAttempted && err == nil && (result.ExitCode < 0 || result.ExitCode > 255) {
			err = fmt.Errorf("exec returned invalid exit status %d", result.ExitCode)
		}

		var cleanupErr error

		if execAttempted && err != nil {
			// A failed host client does not prove the container command stopped.
			// Ignore caller cancellation, but give container cleanup its own limit.
			cleanupCtx, cancelCleanup := context.WithTimeout(
				context.WithoutCancel(ctx),
				30*time.Second,
			)
			// This belongs to this deferred function, not to execLocked.
			// Leaving the if block does not call it.
			defer cancelCleanup()

			cleanupErr = m.cancelExecution(cleanupCtx, record)

			if cleanupErr != nil {
				err = errors.Join(err, cleanupErr)
			}

		}
		completion := executionCompletion{
			Result:     result.ExecResult,
			Cause:      err,
			CleanupErr: cleanupErr,
			EndedAt:    time.Now().UTC(),
		}

		finalizeErr := receipt.finish(completion)

		if finalizeErr != nil {
			err = errors.Join(err, finalizeErr)
			// Exit only this deferred function. Its registered cancels still
			// run, followed by execLocked's workload cancel, if present.
			return
		}

		result.Receipt = receipt

		// Save even if caller cancellation or container cleanup timed out.
		// This context has a separate budget from container cleanup.
		persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		// This cancel belongs to the deferred function. It runs before
		// cancelCleanup because it was registered later.
		defer cancel()

		execErr := m.Store.updateExecution(persistCtx, receiptCopy, receipt)

		if execErr != nil {
			err = errors.Join(err, execErr)
		}
	}()

	execAttempted = true
	result.ExecResult, err = m.Runtime.Exec(workCtx, request.workload(record))

	// Start returning, then run the finalizer and the workload cancel, if any.
	// The caller receives the named values only after those defers finish.
	return result, err
}

// cancelExecution uses an independently live context supplied by the transaction.
// The adapter checks ownership; the manager also checks the returned facts.
func (m *Manager) cancelExecution(ctx context.Context, record Record) error {
	observed, err := m.Runtime.CancelExec(ctx, targetForRecord(record))
	if err == nil {
		err = checkObservation(record, observed)
	}
	if err == nil && !observed.State.StoppedOrMissing() {
		err = runtime.ErrInvalidState
	}
	if err != nil {
		return fmt.Errorf("cancellation could not be verified; inspect and stop session %q (%s) before further use: %w", record.Name, record.ID, err)
	}
	return nil
}
