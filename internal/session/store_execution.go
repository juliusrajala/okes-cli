package session

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// openExistingStoreDirectory never creates a missing directory or follows a
// symlink selected as the directory itself. Handles remain confined by os.Root.
func openExistingStoreDirectory(parent *os.Root, name string) (*os.Root, error) {
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if err := checkStoreInfo(before, true); err != nil {
		return nil, err
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	if err := checkOpenedDirectory(root, before); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func (s *Store) executionDirectory(ctx context.Context, id ID, create bool) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := id.Validate(); err != nil {
		return nil, err
	}
	if err := s.checkDirectories(); err != nil {
		return nil, err
	}
	open := openExistingStoreDirectory
	if create {
		open = openStoreDirectory
	}
	parent, err := open(s.root, "executions")
	if err != nil {
		return nil, err
	}
	directory, err := open(parent, string(id))
	if err != nil {
		return nil, errors.Join(err, parent.Close())
	}
	if create {
		// Sync both parents so their directory entries are durable before exec.
		err = errors.Join(syncStoreDirectory(parent), syncStoreDirectory(s.root))
	}
	err = errors.Join(err, parent.Close())
	if err != nil {
		return nil, errors.Join(err, directory.Close())
	}
	return directory, nil
}

// createExecution publishes a running receipt before any workload side effect.
// The caller holds the session lock. A publication failure never authorizes exec.
func (s *Store) createExecution(ctx context.Context, receipt ExecutionReceipt) (err error) {
	if err := receipt.Validate(); err != nil {
		return err
	}
	if receipt.Outcome != ExecutionRunning {
		return fmt.Errorf("%w: new execution must be running", ErrInvalidRecord)
	}
	data, err := encodeStoreJSON(receipt)
	if err != nil {
		return err
	}
	directory, err := s.executionDirectory(ctx, receipt.SessionID, true)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	if _, err := publishStoreFile(ctx, directory, recordPath(receipt.ID), data); err != nil {
		return fmt.Errorf("publish execution receipt: %w", err)
	}
	return nil
}

// GetExecution reads a receipt by both immutable identities. It validates the
// private directory, file type, JSON shape, and saved identities before returning.
func (s *Store) GetExecution(ctx context.Context, sessionID, executionID ID) (receipt ExecutionReceipt, err error) {
	if err := executionID.Validate(); err != nil {
		return ExecutionReceipt{}, err
	}
	directory, err := s.executionDirectory(ctx, sessionID, false)
	if err != nil {
		return ExecutionReceipt{}, metadataReadError("open execution directory", err)
	}
	defer func() {
		err = errors.Join(err, directory.Close())
		if err != nil {
			receipt = ExecutionReceipt{}
		}
	}()
	data, _, err := readStoreFile(ctx, directory, recordPath(executionID))
	if err != nil {
		return ExecutionReceipt{}, metadataReadError("read execution receipt", err)
	}
	return decodeExecution(data, sessionID, executionID)
}

func decodeExecution(data []byte, sessionID, executionID ID) (ExecutionReceipt, error) {
	var receipt ExecutionReceipt
	if err := decodeStoreJSON(data, &receipt); err != nil {
		return ExecutionReceipt{}, fmt.Errorf("%w: execution JSON: %w", ErrCorruptRecord, err)
	}
	if err := receipt.Validate(); err != nil {
		return ExecutionReceipt{}, fmt.Errorf("%w: execution receipt: %w", ErrCorruptRecord, err)
	}
	if receipt.SessionID != sessionID || receipt.ID != executionID {
		return ExecutionReceipt{}, fmt.Errorf("%w: execution identity does not match its path", ErrCorruptRecord)
	}
	return receipt, nil
}

// updateExecution replaces only the expected running receipt. The caller holds
// the session lock. Uncertain persistence leaves metadata available for recovery.
func (s *Store) updateExecution(ctx context.Context, initial, finished ExecutionReceipt) (err error) {
	if err := initial.Validate(); err != nil {
		return err
	}
	if err := finished.Validate(); err != nil {
		return err
	}
	if initial.Outcome != ExecutionRunning || finished.Outcome == ExecutionRunning ||
		initial.ID != finished.ID || initial.SessionID != finished.SessionID ||
		initial.Version != finished.Version || !initial.StartedAt.Equal(finished.StartedAt) {
		return fmt.Errorf("%w: invalid execution transition", ErrInvalidRecord)
	}
	data, err := encodeStoreJSON(finished)
	if err != nil {
		return err
	}
	directory, err := s.executionDirectory(ctx, initial.SessionID, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	currentData, info, err := readStoreFile(ctx, directory, recordPath(initial.ID))
	if err != nil {
		return err
	}
	current, err := decodeExecution(currentData, initial.SessionID, initial.ID)
	if err != nil {
		return err
	}
	if current.Outcome != ExecutionRunning || !current.StartedAt.Equal(initial.StartedAt) {
		return fmt.Errorf("%w: execution changed before finalization", ErrUnsafeStore)
	}
	return replaceStoreFile(ctx, directory, recordPath(initial.ID), data, info)
}

// deleteExecutions runs only after the caller verifies container absence and
// the session's exact saved identity. Preflight every entry before deleting any.
// Unexpected files or unsafe receipts preserve the session record and alias.
func (s *Store) deleteExecutions(ctx context.Context, sessionID ID) (err error) {
	directory, err := s.executionDirectory(ctx, sessionID, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	directoryInfo, err := directory.Stat(".")
	if err != nil {
		return err
	}
	entries, err := fs.ReadDir(directory.FS(), ".")
	if err != nil {
		return err
	}
	type entryIdentity struct {
		name string
		info os.FileInfo
	}
	files := make([]entryIdentity, 0, len(entries))
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			return fmt.Errorf("%w: unexpected execution entry", ErrUnsafeStore)
		}
		id, err := ParseID(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return fmt.Errorf("%w: unexpected execution filename: %w", ErrUnsafeStore, err)
		}
		data, info, err := readStoreFile(ctx, directory, entry.Name())
		if err != nil {
			return err
		}
		if _, err := decodeExecution(data, sessionID, id); err != nil {
			return err
		}
		files = append(files, entryIdentity{name: entry.Name(), info: info})
	}
	path := "executions/" + string(sessionID)
	current, err := s.root.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(current, directoryInfo) {
		return fmt.Errorf("%w: execution directory was replaced", ErrUnsafeStore)
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := removeStoreFile(directory, file.name, file.info); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err = s.root.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(current, directoryInfo) {
		return fmt.Errorf("%w: execution directory changed before removal", ErrUnsafeStore)
	}
	if err := s.root.Remove(path); err != nil {
		return err
	}
	parent, err := openExistingStoreDirectory(s.root, "executions")
	if err != nil {
		return err
	}
	return errors.Join(syncStoreDirectory(parent), parent.Close())
}
