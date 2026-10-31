package session

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxStoreFileBytes = 1024 * 1024

func openStoreRoot(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("session store path must be absolute")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if err := checkStoreInfo(before, true); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	if err := checkOpenedDirectory(root, before); err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func openStoreDirectory(parent *os.Root, name string) (*os.Root, error) {
	if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
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

func checkOpenedDirectory(root *os.Root, before os.FileInfo) error {
	opened, err := root.Stat(".")
	if err != nil {
		return err
	}
	if !os.SameFile(before, opened) {
		return fmt.Errorf("%w: directory changed while opening", ErrUnsafeStore)
	}
	return checkStoreInfo(opened, true)
}

func checkStoreInfo(info os.FileInfo, directory bool) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: unexpected owner or permissions for %q", ErrUnsafeStore, info.Name())
	}
	if (directory && !info.IsDir()) || (!directory && !info.Mode().IsRegular()) {
		return fmt.Errorf("%w: unexpected file type for %q", ErrUnsafeStore, info.Name())
	}
	return nil
}

func readStoreFile(ctx context.Context, root *os.Root, path string) (data []byte, info os.FileInfo, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	before, err := root.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if err := checkStoreInfo(before, false); err != nil {
		return nil, nil, err
	}
	// NOFOLLOW rejects a replaced symlink. NONBLOCK prevents a replaced FIFO
	// from blocking before its type can be checked on the opened descriptor.
	file, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err = file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(before, info) {
		return nil, nil, fmt.Errorf("%w: metadata file changed while opening", ErrUnsafeStore)
	}
	if err := checkStoreInfo(info, false); err != nil {
		return nil, nil, err
	}
	if info.Size() > maxStoreFileBytes {
		return nil, nil, fmt.Errorf("%w: metadata file exceeds size limit", ErrCorruptRecord)
	}
	data, err = io.ReadAll(io.LimitReader(file, maxStoreFileBytes+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > maxStoreFileBytes {
		return nil, nil, fmt.Errorf("%w: metadata file grew beyond size limit", ErrCorruptRecord)
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

// Publish a complete private file without replacing any existing destination.
// A same-directory hard link gives create-if-absent atomic publication. A rename
// alone would overwrite another identity reservation. os.Root confines the paths.
func publishStoreFile(ctx context.Context, root *os.Root, path string, data []byte) (published bool, err error) {
	temporary, info, err := stageStoreFile(ctx, root, path, data)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, removeStoreFile(root, temporary, info)) }()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := root.Link(temporary, path); err != nil {
		return false, err
	}
	return true, syncStoreDirectory(root)
}

// Replace only the file whose identity was checked while reading the current
// record. Cooperating writers must hold the metadata lock across read and replace.
func replaceStoreFile(ctx context.Context, root *os.Root, path string, data []byte, expected os.FileInfo) (err error) {
	temporary, info, err := stageStoreFile(ctx, root, path, data)
	if err != nil {
		return err
	}
	renamed := false
	defer func() {
		if !renamed {
			err = errors.Join(err, removeStoreFile(root, temporary, info))
		}
	}()
	current, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if err := checkStoreInfo(current, false); err != nil {
		return err
	}
	if !os.SameFile(current, expected) {
		return fmt.Errorf("%w: refusing to replace changed metadata %q", ErrUnsafeStore, path)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Rename(temporary, path); err != nil {
		return err
	}
	renamed = true
	return syncStoreDirectory(root)
}

// Both create and update stage a complete private file in the destination
// directory. Close and sync it before either publication operation uses it.
func stageStoreFile(ctx context.Context, root *os.Root, path string, data []byte) (temporary string, info os.FileInfo, err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	temporary = "." + path + "." + rand.Text() + ".tmp"
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return "", nil, err
	}
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil && info != nil {
			err = errors.Join(err, removeStoreFile(root, temporary, info))
		}
	}()
	info, err = file.Stat()
	if err != nil {
		// Without identity proof, leave the temporary path for recovery.
		return temporary, nil, err
	}
	if _, err := file.Write(data); err != nil {
		return temporary, info, err
	}
	if err := file.Sync(); err != nil {
		return temporary, info, err
	}
	if err := ctx.Err(); err != nil {
		return temporary, info, err
	}
	return temporary, info, nil
}

func removeStoreFile(root *os.Root, path string, expected os.FileInfo) error {
	current, err := root.Lstat(path)
	if err != nil {
		return err
	}
	if !os.SameFile(current, expected) {
		return fmt.Errorf("%w: refusing to remove replaced metadata %q", ErrUnsafeStore, path)
	}
	if err := checkStoreInfo(current, false); err != nil {
		return err
	}
	if err := root.Remove(path); err != nil {
		return err
	}
	return syncStoreDirectory(root)
}

func syncStoreDirectory(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

// Return IDs in filename order. Only published .json records are candidates;
// staging files remain untouched. Get must still validate each file and record.
func (s *Store) recordIDs(ctx context.Context) ([]ID, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkDirectories(); err != nil {
		return nil, err
	}
	entries, err := fs.ReadDir(s.sessions.FS(), ".")
	if err != nil {
		return nil, fmt.Errorf("read session directory: %w", err)
	}
	ids := make([]ID, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		id, err := ParseID(strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, fmt.Errorf("%w: invalid session filename %q: %w", ErrCorruptRecord, entry.Name(), err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func decodeStoreJSON(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected one JSON value: %w", errors.Join(err, ErrCorruptRecord))
	}
	return nil
}

// Serialize metadata creation and updates across store handles and CLI processes.
// Keep the existing filename so older store handles use the same lock. This is
// not the per-session lock needed around container lifecycle operations.
func (s *Store) lockMetadata(ctx context.Context) (func() error, error) {
	return s.lockFile(ctx, ".create.lock")
}

func (s *Store) lockFile(ctx context.Context, path string) (func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkDirectories(); err != nil {
		return nil, err
	}
	file, err := s.root.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil {
		err = checkStoreInfo(info, false)
	}
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			current, statErr := s.root.Lstat(path)
			if statErr == nil {
				statErr = checkStoreInfo(current, false)
			}
			if statErr == nil && !os.SameFile(current, info) {
				statErr = fmt.Errorf("%w: lock file identity changed", ErrUnsafeStore)
			}
			if statErr != nil {
				return nil, errors.Join(statErr, syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
			}
			return func() error {
				return errors.Join(syscall.Flock(int(file.Fd()), syscall.LOCK_UN), file.Close())
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, errors.Join(err, file.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), file.Close())
		case <-ticker.C:
		}
	}
}
