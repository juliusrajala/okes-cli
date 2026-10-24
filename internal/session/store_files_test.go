package session

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Simulate a foreign UID in stat data without changing host ownership or credentials.
type foreignFileInfo struct{ os.FileInfo }

func (info foreignFileInfo) Sys() any {
	stat := *info.FileInfo.Sys().(*syscall.Stat_t)
	stat.Uid++
	return &stat
}

func TestStoreRejectsForeignOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkStoreInfo(foreignFileInfo{info}, false); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("owner check error = %v, want ErrUnsafeStore", err)
	}
}

func TestStoreRefusesReplacedFileRemoval(t *testing.T) {
	store := testStore(t, t.TempDir())
	if err := store.names.WriteFile("reserved.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := store.names.Stat("reserved.json")
	if err != nil {
		t.Fatal(err)
	}
	// Keep the original inode allocated so the replacement cannot reuse it.
	if err := store.names.Rename("reserved.json", "original.json"); err != nil {
		t.Fatal(err)
	}
	if err := store.names.WriteFile("reserved.json", []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeStoreFile(store.names, "reserved.json", expected); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("removal error = %v, want ErrUnsafeStore", err)
	}
	data, err := store.names.ReadFile("reserved.json")
	if err != nil || string(data) != "replacement" {
		t.Fatalf("replacement changed: %q, %v", data, err)
	}
}

func TestReplaceStoreFileIsAtomic(t *testing.T) {
	path := t.TempDir()
	store := testStore(t, path)
	if err := store.sessions.WriteFile("atomic.json", []byte("old content"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := store.sessions.Open("atomic.json")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceStoreFile(context.Background(), store.sessions, "atomic.json", []byte("new content"), info); err != nil {
		t.Fatal(err)
	}
	// An already-open reader still has the old complete file, not truncated data.
	old, err := io.ReadAll(file)
	if err != nil || string(old) != "old content" {
		t.Fatalf("open reader saw %q, %v, want old content", old, err)
	}
	fresh, err := store.sessions.ReadFile("atomic.json")
	if err != nil || string(fresh) != "new content" {
		t.Fatalf("fresh reader saw %q, %v, want new content", fresh, err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "sessions"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("unexpected files after replacement: %v, %v", entries, err)
	}
}

func TestReplaceStoreFileRefusesChangedTarget(t *testing.T) {
	path := t.TempDir()
	store := testStore(t, path)
	if err := store.sessions.WriteFile("target.json", []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := store.sessions.Stat("target.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.sessions.Rename("target.json", ".original.tmp"); err != nil {
		t.Fatal(err)
	}
	if err := store.sessions.WriteFile("target.json", []byte("foreign replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := replaceStoreFile(context.Background(), store.sessions, "target.json", []byte("new"), info); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("replace error = %v, want ErrUnsafeStore", err)
	}
	data, err := store.sessions.ReadFile("target.json")
	if err != nil || string(data) != "foreign replacement" {
		t.Fatalf("replacement changed: %q, %v", data, err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "sessions"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("failed replacement left staging data: %v, %v", entries, err)
	}
}

func TestReplaceStoreFileCancellation(t *testing.T) {
	store := testStore(t, t.TempDir())
	if err := store.sessions.WriteFile("target.json", []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := store.sessions.Stat("target.json")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := replaceStoreFile(ctx, store.sessions, "target.json", []byte("new"), info); !errors.Is(err, context.Canceled) {
		t.Fatalf("replace error = %v, want context.Canceled", err)
	}
	data, err := store.sessions.ReadFile("target.json")
	if err != nil || string(data) != "original" {
		t.Fatalf("cancelled replace changed data: %q, %v", data, err)
	}
}

func TestStoreRecordIDs(t *testing.T) {
	store := testStore(t, t.TempDir())
	ids, err := store.recordIDs(context.Background())
	if err != nil || len(ids) != 0 {
		t.Fatalf("empty store IDs = %v, %v", ids, err)
	}
	first := validRecord()
	second := first
	second.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
	second.Name = "second-session"
	second.ContainerName = "pi-sesh-" + string(second.ID)
	createRecord(t, store, second)
	createRecord(t, store, first)
	for _, name := range []string{".unfinished.json.random.tmp", "notes.txt"} {
		if err := store.sessions.WriteFile(name, []byte("untouched"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids, err = store.recordIDs(context.Background())
	if err != nil || len(ids) != 2 || ids[0] != first.ID || ids[1] != second.ID {
		t.Fatalf("IDs = %v, %v, want [%s %s]", ids, err, first.ID, second.ID)
	}
	data, err := store.sessions.ReadFile(".unfinished.json.random.tmp")
	if err != nil || string(data) != "untouched" {
		t.Fatalf("listing modified staging data: %q, %v", data, err)
	}
}

func TestStoreRecordIDsRejectsInvalidFilename(t *testing.T) {
	store := testStore(t, t.TempDir())
	if err := store.sessions.WriteFile("not-a-uuid.json", []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ids, err := store.recordIDs(context.Background())
	if !errors.Is(err, ErrCorruptRecord) || ids != nil {
		t.Fatalf("IDs = %v, error = %v, want nil IDs and ErrCorruptRecord", ids, err)
	}
}

func TestStoreRecordIDsRequiresLaterFileValidation(t *testing.T) {
	store := testStore(t, t.TempDir())
	id := validRecord().ID
	if err := store.sessions.Mkdir(recordPath(id), 0o700); err != nil {
		t.Fatal(err)
	}
	ids, err := store.recordIDs(context.Background())
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("candidate IDs = %v, %v", ids, err)
	}
	if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("Get error = %v, want directory to fail record validation", err)
	}
}

func TestStoreRecordIDsCancellation(t *testing.T) {
	store := testStore(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ids, err := store.recordIDs(ctx)
	if !errors.Is(err, context.Canceled) || ids != nil {
		t.Fatalf("IDs = %v, error = %v, want context.Canceled", ids, err)
	}
}

func TestStoreRefusesUnsafeCreationLock(t *testing.T) {
	for _, kind := range []string{"symlink", "FIFO", "public"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			store := testStore(t, path)
			lock := filepath.Join(path, ".create.lock")
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "foreign.lock"), lock); err != nil {
					t.Fatal(err)
				}
			case "FIFO":
				if err := syscall.Mkfifo(lock, 0o600); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.WriteFile(lock, nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.Create(context.Background(), validRecord()); err == nil {
				t.Fatal("Create accepted an unsafe lock file")
			}
		})
	}
}
