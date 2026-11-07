package session

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go-sesh/internal/runtime"
)

// ExecutionOutcome describes the durable state of one execution attempt.
type ExecutionOutcome string

const (
	// ExecutionRunning means no final receipt has been committed.
	ExecutionRunning ExecutionOutcome = "running"
	// ExecutionCompleted includes ordinary nonzero workload exits.
	ExecutionCompleted ExecutionOutcome = "completed"
	// ExecutionCancelled means the caller cancelled and cleanup was verified.
	ExecutionCancelled ExecutionOutcome = "cancelled"
	// ExecutionTimeout means the deadline expired and cleanup was verified.
	ExecutionTimeout ExecutionOutcome = "timeout"
	// ExecutionError includes transport failures and unverified cleanup.
	ExecutionError ExecutionOutcome = "error"
)

// ExecutionReceipt records execution facts without argv, input, or output.
// Its version is independent of the session-record version. A nil Status means
// no ordinary exit code was observed; a pointer to zero records a known success.
type ExecutionReceipt struct {
	Version   int              `json:"version"`
	ID        ID               `json:"id"`
	SessionID ID               `json:"sessionId"`
	StartedAt time.Time        `json:"startedAt"`
	EndedAt   *time.Time       `json:"endedAt,omitempty"`
	Status    *int             `json:"status,omitempty"`
	Outcome   ExecutionOutcome `json:"outcome"`
}

// Validate checks identity and state consistency before storage or cleanup.
func (r ExecutionReceipt) Validate() error {
	if r.Version != 1 {
		return fmt.Errorf("%w: unsupported execution receipt version", ErrInvalidRecord)
	}
	if err := r.ID.Validate(); err != nil {
		return fmt.Errorf("%w: execution ID: %w", ErrInvalidRecord, err)
	}
	if err := r.SessionID.Validate(); err != nil {
		return fmt.Errorf("%w: execution session ID: %w", ErrInvalidRecord, err)
	}
	if r.StartedAt.IsZero() || (r.Status != nil && (*r.Status < 0 || *r.Status > 255)) {
		return fmt.Errorf("%w: invalid execution time or status", ErrInvalidRecord)
	}
	if r.Outcome == ExecutionRunning {
		if r.EndedAt != nil || r.Status != nil {
			return fmt.Errorf("%w: running receipt has final facts", ErrInvalidRecord)
		}
		return nil
	}
	switch r.Outcome {
	case ExecutionCompleted:
		if r.Status == nil {
			return fmt.Errorf("%w: completed execution has no status", ErrInvalidRecord)
		}
	case ExecutionCancelled, ExecutionTimeout, ExecutionError:
	default:
		return fmt.Errorf("%w: invalid execution outcome", ErrInvalidRecord)
	}
	if r.EndedAt == nil || r.EndedAt.IsZero() || r.EndedAt.Before(r.StartedAt) {
		return fmt.Errorf("%w: invalid execution end time", ErrInvalidRecord)
	}
	return nil
}

// executionCompletion separates execution failure from cleanup failure. Cause
// includes the workload context error when cancellation or a timeout occurred.
// CleanupErr is non-nil if container stop/absence could not be verified.
type executionCompletion struct {
	Result     runtime.ExecResult
	Cause      error
	CleanupErr error
	EndedAt    time.Time
}

// finish changes a running receipt into a validated final receipt. It preserves
// identity and start time. Cleanup failure takes precedence over cancellation.
// Invalid input must leave the receiver unchanged.
func (r *ExecutionReceipt) finish(completion executionCompletion) error {
	if err := r.Validate(); err != nil {
		return err
	}

	if r.Outcome != ExecutionRunning {
		return fmt.Errorf("%w: execution receipt is not running", ErrInvalidRecord)
	}

	// Every final receipt records when this execution attempt ended, regardless
	// of whether it completed, timed out, or failed.
	if completion.EndedAt.IsZero() {
		return fmt.Errorf("%w: execution end time is required", ErrInvalidRecord)
	}

	// Build the final state on a copy. Validation errors leave the caller's
	// running receipt unchanged.
	receipt := *r

	receipt.EndedAt = &completion.EndedAt

	// Cleanup failure takes precedence because the container's final state is
	// uncertain. errors.Is also recognizes wrapped and joined context errors.
	if completion.CleanupErr != nil {
		receipt.Outcome = ExecutionError
	} else if errors.Is(completion.Cause, context.DeadlineExceeded) {
		receipt.Outcome = ExecutionTimeout
	} else if errors.Is(completion.Cause, context.Canceled) {
		receipt.Outcome = ExecutionCancelled
	} else if completion.Cause != nil {
		receipt.Outcome = ExecutionError
	} else {
		receipt.Outcome = ExecutionCompleted
	}

	if completion.Result.ExitCode < -1 || completion.Result.ExitCode > 255 {
		return fmt.Errorf("%w: execution exit code %d is out of range", ErrInvalidRecord, completion.Result.ExitCode)
	}

	// A cancelled command can still return an ordinary exit code. Keep that
	// fact when known. Nil represents unknown, while a pointer preserves zero.
	if completion.Result.ExitCode >= 0 {
		receipt.Status = &completion.Result.ExitCode
	}

	// Commit only after the complete receipt passes its state invariants.
	if err := receipt.Validate(); err != nil {
		return err
	}

	*r = receipt
	return nil
}

// newExecution creates the initial in-memory receipt. The transaction must
// publish it before invoking Exec; this function does not write any files.
func newExecution(id ID) (ExecutionReceipt, error) {
	executionID, err := newID()
	if err != nil {
		return ExecutionReceipt{}, err
	}
	return ExecutionReceipt{
		Version: 1, ID: executionID, SessionID: id,
		StartedAt: time.Now().UTC(), Outcome: ExecutionRunning,
	}, nil
}
