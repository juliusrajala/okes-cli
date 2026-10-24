package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStoreUpdateReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	record := validRecord()
	updated := record
	updated.ContainerID = strings.Repeat("b", 64)
	t.Run("create and bind container", func(t *testing.T) {
		store := testStore(t, path)
		createRecord(t, store, record)
		before, err := store.names.ReadFile(namePath(record.Name))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Update(context.Background(), updated); err != nil {
			t.Fatal(err)
		}
		after, err := store.names.ReadFile(namePath(record.Name))
		if err != nil || string(after) != string(before) {
			t.Fatalf("update changed the name reservation: %q, %v", after, err)
		}
	})
	t.Run("reopen and resolve", func(t *testing.T) {
		store := testStore(t, path)
		got, err := store.Resolve(context.Background(), record.Name)
		if err != nil || got != updated {
			t.Fatalf("resolved record = %+v, error = %v, want %+v", got, err, updated)
		}
		info, err := store.sessions.Stat(recordPath(record.ID))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("updated file permissions = %o, want 600", info.Mode().Perm())
		}
		entries, err := os.ReadDir(filepath.Join(path, "sessions"))
		if err != nil || len(entries) != 1 {
			t.Fatalf("update left unexpected files: %v, %v", entries, err)
		}
	})
}

func TestStoreUpdateRejectsChangedFacts(t *testing.T) {
	tests := []struct {
		name    string
		change  func(*Record)
		wantErr error
	}{
		{name: "ID", change: func(r *Record) {
			r.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
			r.ContainerName = "pi-sesh-" + string(r.ID)
		}, wantErr: ErrNotFound},
		{name: "name", change: func(r *Record) { r.Name = "other-session" }, wantErr: ErrInvalidRecord},
		{name: "container name", change: func(r *Record) { r.ContainerName = "foreign" }, wantErr: ErrInvalidRecord},
		{name: "source", change: func(r *Record) { r.ProjectDir = "/other/source" }, wantErr: ErrInvalidRecord},
		{name: "workspace mode", change: func(r *Record) { r.WorkspaceMode = "copy" }, wantErr: ErrInvalidRecord},
		{name: "workspace path", change: func(r *Record) { r.WorkspacePath = "/other/workspace" }, wantErr: ErrInvalidRecord},
		{name: "image", change: func(r *Record) { r.ImageID = strings.Repeat("c", 64) }, wantErr: ErrInvalidRecord},
		{name: "network", change: func(r *Record) { r.Network = "pasta" }, wantErr: ErrInvalidRecord},
		{name: "tty", change: func(r *Record) { r.TTY = true }, wantErr: ErrInvalidRecord},
		{name: "creation time", change: func(r *Record) { r.CreatedAt = r.CreatedAt.Add(time.Second) }, wantErr: ErrInvalidRecord},
		{name: "version", change: func(r *Record) { r.Version = 1 }, wantErr: ErrInvalidRecord},
		{name: "invalid container ID", change: func(r *Record) { r.ContainerID = "not-an-id" }, wantErr: ErrInvalidRecord},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t, t.TempDir())
			original := validRecord()
			createRecord(t, store, original)
			updated := original
			updated.ContainerID = strings.Repeat("b", 64)
			tt.change(&updated)
			if err := store.Update(context.Background(), updated); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Update error = %v, want %v", err, tt.wantErr)
			}
			got, err := store.Resolve(context.Background(), original.Name)
			if err != nil || got != original {
				t.Fatalf("rejected update changed original: %+v, %v", got, err)
			}
		})
	}
}

func TestStoreUpdateContainerBinding(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	createRecord(t, store, record)
	if err := store.Update(context.Background(), record); err != nil {
		t.Fatalf("unchanged pending record: %v", err)
	}
	record.ContainerID = strings.Repeat("b", 64)
	for i := 0; i < 2; i++ {
		if err := store.Update(context.Background(), record); err != nil {
			t.Fatalf("idempotent binding: %v", err)
		}
	}
	for _, other := range []string{PendingContainerID, strings.Repeat("c", 64)} {
		next := record
		next.ContainerID = other
		if err := store.Update(context.Background(), next); !errors.Is(err, ErrInvalidRecord) {
			t.Fatalf("container rebind error = %v, want ErrInvalidRecord", err)
		}
	}
	got, err := store.Get(context.Background(), record.ID)
	if err != nil || got != record {
		t.Fatalf("bound identity changed: %+v, %v", got, err)
	}
}

func TestStoreUpdateEquivalentTimestamp(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	createRecord(t, store, record)
	record.CreatedAt = record.CreatedAt.In(time.FixedZone("offset", 3600))
	record.ContainerID = strings.Repeat("b", 64)
	if err := store.Update(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(context.Background(), record.ID)
	if err != nil || !got.CreatedAt.Equal(record.CreatedAt) || got.ContainerID != record.ContainerID {
		t.Fatalf("updated record = %+v, error = %v", got, err)
	}
}

func TestStoreUpdateRejectsMissingRecord(t *testing.T) {
	path := t.TempDir()
	store := testStore(t, path)
	if err := store.Update(context.Background(), validRecord()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Update error = %v, want ErrNotFound", err)
	}
	for _, directory := range []string{"sessions", "names"} {
		entries, err := os.ReadDir(filepath.Join(path, directory))
		if err != nil || len(entries) != 0 {
			t.Fatalf("Update created %s entries: %v, %v", directory, entries, err)
		}
	}
}

func TestStoreUpdateRejectsUnsafeSavedState(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*testing.T, *Store, Record)
		wantErr error
	}{
		{name: "corrupt record", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.WriteFile(recordPath(r.ID), []byte(`{`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "public record", wantErr: ErrUnsafeStore, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.Chmod(recordPath(r.ID), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlink record", wantErr: ErrUnsafeStore, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.Rename(recordPath(r.ID), ".original.tmp"); err != nil {
				t.Fatal(err)
			}
			if err := s.sessions.Symlink(".original.tmp", recordPath(r.ID)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing reservation", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.names.Remove(namePath(r.Name)); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt reservation", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.names.WriteFile(namePath(r.Name), []byte(`{`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "reservation points elsewhere", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			data, err := encodeStoreJSON(nameReservation{Version: RecordVersion, Name: r.Name, ID: "8ad20f51-6067-4dc4-a072-54c94d7735de"})
			if err != nil {
				t.Fatal(err)
			}
			if err := s.names.WriteFile(namePath(r.Name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t, t.TempDir())
			record := validRecord()
			createRecord(t, store, record)
			tt.setup(t, store, record)
			before, err := store.sessions.Lstat(recordPath(record.ID))
			if err != nil {
				t.Fatal(err)
			}
			record.ContainerID = strings.Repeat("b", 64)
			if err := store.Update(context.Background(), record); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Update error = %v, want %v", err, tt.wantErr)
			}
			after, err := store.sessions.Lstat(recordPath(record.ID))
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("Update replaced unsafe or unverified saved state: %v", err)
			}
		})
	}
}

func TestStoreUpdateCancelledBeforePublication(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	createRecord(t, store, record)
	updated := record
	updated.ContainerID = strings.Repeat("b", 64)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Update(ctx, updated); !errors.Is(err, context.Canceled) {
		t.Fatalf("Update error = %v, want context.Canceled", err)
	}
	got, err := store.Get(context.Background(), record.ID)
	if err != nil || got != record {
		t.Fatalf("cancelled update changed record: %+v, %v", got, err)
	}
}

func TestStoreUpdateLockCancellation(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	createRecord(t, store, record)
	unlock, err := store.lockMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	updated := record
	updated.ContainerID = strings.Repeat("b", 64)
	if err := store.Update(ctx, updated); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Update error = %v, want context.DeadlineExceeded", err)
	}
	got, err := store.Get(context.Background(), record.ID)
	if err != nil || got != record {
		t.Fatalf("blocked update changed record: %+v, %v", got, err)
	}
}

func TestStoreUpdateEncodingFailure(t *testing.T) {
	path := t.TempDir()
	store := testStore(t, path)
	record := validRecord()
	createRecord(t, store, record)
	updated := record
	updated.CreatedAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Update(context.Background(), updated); err == nil {
		t.Fatal("Update accepted a timestamp that cannot be encoded as JSON")
	}
	got, err := store.Get(context.Background(), record.ID)
	if err != nil || got != record {
		t.Fatalf("encoding failure changed record: %+v, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "sessions"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("encoding failure left staging data: %v, %v", entries, err)
	}
}

func TestStoreConcurrentContainerBinding(t *testing.T) {
	path := t.TempDir()
	stores := []*Store{testStore(t, path), testStore(t, path)}
	record := validRecord()
	createRecord(t, stores[0], record)
	start := make(chan struct{})
	results := make(chan error, 8)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Go(func() {
			<-start
			next := record
			next.ContainerID = fmt.Sprintf("%064x", i+1)
			results <- stores[i%len(stores)].Update(context.Background(), next)
		})
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("unexpected binding error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful distinct bindings = %d, want 1", successes)
	}
	got, err := stores[0].Resolve(context.Background(), record.Name)
	if err != nil || got.ContainerID == PendingContainerID {
		t.Fatalf("record not bound after concurrent updates: %+v, %v", got, err)
	}
}
