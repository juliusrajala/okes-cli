package session

import (
	"context"
	"errors"
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
