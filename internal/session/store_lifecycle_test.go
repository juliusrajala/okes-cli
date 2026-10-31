package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go-sesh/internal/workspace"
)

func TestStoreDelete(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	createRecord(t, store, record)
	if err := store.withSessionLock(context.Background(), record.ID, func() error {
		return store.Delete(context.Background(), record)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("record still present: %v", err)
	}
	if _, err := store.Resolve(context.Background(), record.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("alias still present: %v", err)
	}
}

func TestStoreDeleteRefusesChangedOrUnsafeMetadata(t *testing.T) {
	for _, kind := range []string{"changed record", "wrong alias", "symlink record", "symlink alias", "corrupt record", "copy", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			store := testStore(t, path)
			record := validRecord()
			createRecord(t, store, record)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			expected := record
			switch kind {
			case "changed record":
				expected.ImageID = record.ContainerID
				if expected.ImageID == record.ImageID {
					expected.ImageID = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
				}
			case "wrong alias":
				data, err := encodeStoreJSON(nameReservation{Version: RecordVersion, ID: "8ad20f51-6067-4dc4-a072-54c94d7735de", Name: record.Name})
				if err != nil {
					t.Fatal(err)
				}
				if err := store.names.WriteFile(namePath(record.Name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink record", "symlink alias":
				root, file := store.sessions, recordPath(record.ID)
				if kind == "symlink alias" {
					root, file = store.names, namePath(record.Name)
				}
				if err := root.Rename(file, "original.json"); err != nil {
					t.Fatal(err)
				}
				if err := root.Symlink("original.json", file); err != nil {
					t.Fatal(err)
				}
			case "corrupt record":
				if err := store.sessions.WriteFile(recordPath(record.ID), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "copy":
				expected.WorkspaceMode = workspace.Copy
			case "cancelled":
				cancel()
			}
			if err := store.Delete(ctx, expected); err == nil {
				t.Fatal("unsafe deletion succeeded")
			}
			for _, file := range []string{filepath.Join(path, "sessions", recordPath(record.ID)), filepath.Join(path, "names", namePath(record.Name))} {
				if _, err := os.Lstat(file); err != nil {
					t.Fatalf("unsafe deletion removed %s: %v", file, err)
				}
			}
		})
	}
}

func TestSessionLocks(t *testing.T) {
	path := t.TempDir()
	first := testStore(t, path)
	second := testStore(t, path)
	id := validRecord().ID
	other := ID("8ad20f51-6067-4dc4-a072-54c94d7735de")
	if err := first.withSessionLock(context.Background(), id, func() error {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := second.withSessionLock(ctx, id, func() error {
			t.Fatal("cancelled lock ran its operation")
			return nil
		}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled lock = %v", err)
		}
		// Different sessions do not share the lifecycle lock.
		ctx, cancel = context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return second.withSessionLock(ctx, other, func() error { return nil })
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.withSessionLock(context.Background(), id, func() error { return nil }); err != nil {
		t.Fatalf("lock was not released: %v", err)
	}
}

func TestSessionLockWaitCancellation(t *testing.T) {
	store := testStore(t, t.TempDir())
	id := validRecord().ID
	unlock, err := store.lockFile(context.Background(), ".session-"+string(id)+".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(entered)
		done <- store.withSessionLock(ctx, id, func() error {
			return errors.New("operation must not run while lock is held")
		})
	}()
	<-entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting lock = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lock wait ignored cancellation")
	}
}
