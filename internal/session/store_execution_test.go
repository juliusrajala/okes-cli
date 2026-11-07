package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func runningReceipt(t *testing.T) ExecutionReceipt {
	t.Helper()
	receipt, err := newExecution(validRecord().ID)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func completedReceipt(initial ExecutionReceipt) ExecutionReceipt {
	finished := initial
	end := initial.StartedAt.Add(time.Second)
	status := 23
	finished.EndedAt, finished.Status, finished.Outcome = &end, &status, ExecutionCompleted
	return finished
}

func TestStoreExecutionLifecycle(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	store := testStore(t, root)
	initial := runningReceipt(t)
	ctx := context.Background()
	if err := store.createExecution(ctx, initial); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "executions", string(initial.SessionID), recordPath(initial.ID))
	for _, path := range []string{filepath.Dir(filepath.Dir(path)), filepath.Dir(path), path} {
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("private execution path %q: %v, %v", path, info, err)
		}
	}
	if err := store.createExecution(ctx, initial); !errors.Is(err, os.ErrExist) {
		t.Fatalf("duplicate execution = %v", err)
	}
	got, err := store.GetExecution(ctx, initial.SessionID, initial.ID)
	if err != nil || got.Outcome != ExecutionRunning || got.ID != initial.ID {
		t.Fatalf("initial receipt = %+v, %v", got, err)
	}
	finished := completedReceipt(initial)
	if err := store.updateExecution(ctx, initial, finished); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetExecution(ctx, initial.SessionID, initial.ID)
	if err != nil || got.Outcome != ExecutionCompleted || got.Status == nil || *got.Status != 23 || got.EndedAt == nil || !got.EndedAt.Equal(*finished.EndedAt) {
		t.Fatalf("final receipt = %+v, %v", got, err)
	}
	if err := store.updateExecution(ctx, initial, finished); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("overwriting terminal receipt = %v", err)
	}
}

func TestExecutionReadDoesNotCreateDirectories(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	store := testStore(t, root)
	receipt := runningReceipt(t)
	if _, err := store.GetExecution(context.Background(), receipt.SessionID, receipt.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing receipt = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "executions")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created execution directories: %v", err)
	}
}

func TestExecutionStoreRefusesUnsafeData(t *testing.T) {
	for _, kind := range []string{"symlink root", "symlink file", "public file", "wrong session", "unknown field", "invalid transition"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			store := testStore(t, root)
			receipt := runningReceipt(t)
			ctx := context.Background()
			if kind == "symlink root" {
				external := t.TempDir()
				if err := os.Symlink(external, filepath.Join(root, "executions")); err != nil {
					t.Fatal(err)
				}
				if err := store.createExecution(ctx, receipt); !errors.Is(err, ErrUnsafeStore) {
					t.Fatalf("symlink execution root = %v", err)
				}
				return
			}
			if err := store.createExecution(ctx, receipt); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "executions", string(receipt.SessionID), recordPath(receipt.ID))
			write := func(data []byte) {
				t.Helper()
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			want := ErrCorruptRecord
			switch kind {
			case "symlink file":
				external := filepath.Join(t.TempDir(), "untouched")
				if err := os.WriteFile(external, []byte("caller data"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, path); err != nil {
					t.Fatal(err)
				}
				want = ErrUnsafeStore
			case "public file":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
				want = ErrUnsafeStore
			case "wrong session":
				changed := receipt
				changed.SessionID = "3b68db01-63c8-4be5-a565-850700bb00ae"
				data, err := encodeStoreJSON(changed)
				if err != nil {
					t.Fatal(err)
				}
				write(data)
			case "unknown field":
				data, err := encodeStoreJSON(receipt)
				if err != nil {
					t.Fatal(err)
				}
				write(append(append([]byte{}, data[:len(data)-2]...), []byte(",\"argv\":[\"secret\"]}\n")...))
			case "invalid transition":
				finished := completedReceipt(receipt)
				finished.StartedAt = finished.StartedAt.Add(time.Millisecond)
				if err := store.updateExecution(ctx, receipt, finished); !errors.Is(err, ErrInvalidRecord) {
					t.Fatalf("changed immutable start = %v", err)
				}
				return
			}
			if _, err := store.GetExecution(ctx, receipt.SessionID, receipt.ID); !errors.Is(err, want) {
				t.Fatalf("unsafe read = %v, want %v", err, want)
			}
		})
	}
}

func TestDeleteSessionReceipts(t *testing.T) {
	for _, unsafe := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove receipts", true: "retain unsafe receipts"}[unsafe], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "state")
			store := testStore(t, root)
			record := validRecord()
			createRecord(t, store, record)
			receipt := runningReceipt(t)
			if err := store.createExecution(context.Background(), receipt); err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(root, "executions", string(record.ID))
			if unsafe {
				if err := os.WriteFile(filepath.Join(directory, "unexpected.txt"), []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := store.Delete(context.Background(), record)
			if unsafe {
				if !errors.Is(err, ErrUnsafeStore) {
					t.Fatalf("unsafe delete = %v", err)
				}
				if _, err := store.Resolve(context.Background(), record.Name); err != nil {
					t.Fatalf("unsafe cleanup lost reservation: %v", err)
				}
				if _, err := store.GetExecution(context.Background(), record.ID, receipt.ID); err != nil {
					t.Fatalf("preflight deleted a receipt before refusing cleanup: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("receipt directory remains: %v", err)
				}
			}
		})
	}
}
