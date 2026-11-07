package session

import (
	"context"
	"errors"
	"fmt"

	"go-sesh/internal/workspace"
)

// withSessionLock serializes lifecycle mutations across processes and handles.
// Lock files remain after removal: unlinking one could let two callers lock
// different inodes for the same identity. Lock order is session, then metadata.
func (s *Store) withSessionLock(ctx context.Context, id ID, operation func() error) (err error) {
	if err := id.Validate(); err != nil {
		return err
	}
	unlock, err := s.lockFile(ctx, ".session-"+string(id)+".lock")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return operation()
}

// Delete removes a matching mount-session record and its alias. The caller must
// hold the session lock and freshly verify container absence. This method checks
// metadata identity only; it never removes workspace directories or containers.
// Copy records are refused until their owned-workspace cleanup is implemented.
//
// The record is removed before the alias. An interruption can leave an orphaned
// reservation for recovery, but cannot release a name while its record remains.
func (s *Store) Delete(ctx context.Context, expected Record) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := expected.Validate(); err != nil {
		return err
	}
	if expected.WorkspaceMode != workspace.Mount {
		return fmt.Errorf("%w: cannot discard copy ownership without verified workspace cleanup", ErrUnsafeStore)
	}
	unlock, err := s.lockMetadata(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	data, recordInfo, err := readStoreFile(ctx, s.sessions, recordPath(expected.ID))
	if err != nil {
		return metadataReadError("read session for removal", err)
	}
	current, err := decodeRecord(data, expected.ID)
	if err != nil {
		return err
	}
	if !sameRecord(current, expected) {
		return fmt.Errorf("%w: session changed before metadata removal", ErrUnsafeStore)
	}
	nameData, nameInfo, err := readStoreFile(ctx, s.names, namePath(expected.Name))
	if err != nil {
		return metadataReadError("read reservation for removal", err)
	}
	var reservation nameReservation
	if err := decodeStoreJSON(nameData, &reservation); err != nil {
		return fmt.Errorf("%w: invalid removal reservation: %w", ErrCorruptRecord, err)
	}
	if reservation.Version != RecordVersion || reservation.ID != expected.ID || reservation.Name != expected.Name {
		return fmt.Errorf("%w: removal reservation does not match session", ErrCorruptRecord)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.deleteExecutions(ctx, expected.ID); err != nil {
		return fmt.Errorf("remove execution receipts; session retained: %w", err)
	}
	if err := removeStoreFile(s.sessions, recordPath(expected.ID), recordInfo); err != nil {
		return fmt.Errorf("remove session record; reservation retained: %w", err)
	}
	// Once record removal commits, finish alias release under the metadata lock
	// even if the caller cancels. Any failure leaves the reservation untouched.
	if err := removeStoreFile(s.names, namePath(expected.Name), nameInfo); err != nil {
		return fmt.Errorf("record removed; name remains reserved for recovery: %w", err)
	}
	return nil
}

func sameRecord(a, b Record) bool {
	createdAt := b.CreatedAt
	b.CreatedAt = a.CreatedAt
	return a == b && a.CreatedAt.Equal(createdAt)
}
