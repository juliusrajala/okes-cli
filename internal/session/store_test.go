package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func testStore(t *testing.T, path string) *Store {
	t.Helper()
	// The test harness can create child temp directories with mode 0755.
	// Set up a private fixture; production OpenStore must not repair permissions.
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	return store
}

func createRecord(t *testing.T, store *Store, record Record) {
	t.Helper()
	if err := store.Create(context.Background(), record); err != nil {
		t.Fatal(err)
	}
}

func TestOpenStoreCreatesPrivateRoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "state")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("root mode = %o, want 700", info.Mode().Perm())
	}
}

func TestStoreCreateReopenGet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	record := validRecord()
	t.Run("create and close", func(t *testing.T) {
		store := testStore(t, path)
		createRecord(t, store, record)
	})
	t.Run("reopen and load", func(t *testing.T) {
		store := testStore(t, path)
		got, err := store.Get(context.Background(), record.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got != record {
			t.Fatalf("loaded record = %+v, want %+v", got, record)
		}
		reservation, err := store.readName(context.Background(), record.Name)
		if err != nil {
			t.Fatal(err)
		}
		if reservation.ID != record.ID || reservation.Name != record.Name {
			t.Fatalf("name reservation = %+v, want %s/%s", reservation, record.Name, record.ID)
		}
	})
}

func TestStorePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	store := testStore(t, path)
	record := validRecord()
	createRecord(t, store, record)
	for _, directory := range []string{path, filepath.Join(path, "sessions"), filepath.Join(path, "names")} {
		info, err := os.Lstat(directory)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s permissions = %o, want 700", directory, info.Mode().Perm())
		}
	}
	for _, file := range []string{filepath.Join(path, "sessions", recordPath(record.ID)), filepath.Join(path, "names", namePath(record.Name)), filepath.Join(path, ".create.lock")} {
		info, err := os.Lstat(file)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("%s permissions = %o, want 600", file, info.Mode().Perm())
		}
	}
	for _, directory := range []string{"sessions", "names"} {
		entries, err := os.ReadDir(filepath.Join(path, directory))
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".json") {
			t.Fatalf("unexpected files after creation in %s: %v", directory, entries)
		}
	}
}

func TestStoreCreateCollisions(t *testing.T) {
	for _, collision := range []string{"name", "ID"} {
		t.Run(collision, func(t *testing.T) {
			path := t.TempDir()
			store := testStore(t, path)
			original := validRecord()
			createRecord(t, store, original)
			other := original
			if collision == "name" {
				other.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
				other.ContainerName = "pi-sesh-" + string(other.ID)
			} else {
				other.Name = "other-name"
			}
			if err := store.Create(context.Background(), other); !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("Create error = %v, want ErrAlreadyExists", err)
			}
			got, err := store.Get(context.Background(), original.ID)
			if err != nil || got != original {
				t.Fatalf("original changed: record = %+v, error = %v", got, err)
			}
			entries, err := os.ReadDir(filepath.Join(path, "names"))
			if err != nil || len(entries) != 1 {
				t.Fatalf("name reservations = %v, error = %v, want only the original", entries, err)
			}
		})
	}
}

func TestStoreConcurrentCreate(t *testing.T) {
	for _, collision := range []string{"name", "ID"} {
		t.Run(collision, func(t *testing.T) {
			path := t.TempDir()
			stores := []*Store{testStore(t, path), testStore(t, path)}
			start := make(chan struct{})
			results := make(chan error, 8)
			var workers sync.WaitGroup
			for i := 0; i < 8; i++ {
				workers.Go(func() {
					<-start
					record := validRecord()
					if collision == "name" {
						record.ID = ID(fmt.Sprintf("%08x-6067-4dc4-a072-54c94d7735de", i+1))
						record.ContainerName = "pi-sesh-" + string(record.ID)
					} else {
						record.Name = fmt.Sprintf("name-%d", i)
					}
					results <- stores[i%len(stores)].Create(context.Background(), record)
				})
			}
			close(start)
			workers.Wait()
			close(results)
			successes := 0
			for err := range results {
				if err == nil {
					successes++
				} else if !errors.Is(err, ErrAlreadyExists) {
					t.Errorf("unexpected create error: %v", err)
				}
			}
			if successes != 1 {
				t.Fatalf("successful creates = %d, want 1", successes)
			}
			for _, directory := range []string{"sessions", "names"} {
				entries, err := os.ReadDir(filepath.Join(path, directory))
				if err != nil || len(entries) != 1 {
					t.Fatalf("%s entries = %v, error = %v, want one committed identity", directory, entries, err)
				}
			}
		})
	}
}

func TestStoreMissingAndCancelled(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	if _, err := store.Get(context.Background(), record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get error = %v, want ErrNotFound", err)
	}
	if _, err := store.readName(context.Background(), record.Name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("name lookup error = %v, want ErrNotFound", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := store.Create(ctx, record); !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want context.Canceled", err)
	}
	if _, err := store.Get(ctx, record.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("Get error = %v, want context.Canceled", err)
	}
	if _, err := store.Get(context.Background(), record.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cancelled create published a record: %v", err)
	}
}

func TestStoreInvalidCreateAllocatesNothing(t *testing.T) {
	path := t.TempDir()
	store := testStore(t, path)
	record := validRecord()
	record.WorkspacePath = "relative"
	if err := store.Create(context.Background(), record); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("Create error = %v, want ErrInvalidRecord", err)
	}
	for _, directory := range []string{"sessions", "names"} {
		entries, err := os.ReadDir(filepath.Join(path, directory))
		if err != nil || len(entries) != 0 {
			t.Fatalf("invalid create left %s state: %v, %v", directory, entries, err)
		}
	}
}

func TestStoreRejectsCorruptRecords(t *testing.T) {
	tests := []struct {
		name string
		edit func(map[string]json.RawMessage)
	}{
		{name: "legacy version", edit: func(m map[string]json.RawMessage) { m["version"] = json.RawMessage(`1`) }},
		{name: "unknown version", edit: func(m map[string]json.RawMessage) { m["version"] = json.RawMessage(`99`) }},
		{name: "missing tty", edit: func(m map[string]json.RawMessage) { delete(m, "tty") }},
		{name: "null tty", edit: func(m map[string]json.RawMessage) { m["tty"] = json.RawMessage(`null`) }},
		{name: "wrong tty type", edit: func(m map[string]json.RawMessage) { m["tty"] = json.RawMessage(`"false"`) }},
		{name: "relative path", edit: func(m map[string]json.RawMessage) { m["workspacePath"] = json.RawMessage(`"relative"`) }},
		{name: "clone mode", edit: func(m map[string]json.RawMessage) { m["workspaceMode"] = json.RawMessage(`"clone"`) }},
		{name: "unknown fields", edit: func(m map[string]json.RawMessage) { m["unexpected"] = json.RawMessage(`true`) }},
		{name: "resource metadata cannot be dropped", edit: func(m map[string]json.RawMessage) { m["resources"] = json.RawMessage(`[]`) }},
		{name: "Git provenance forbidden", edit: func(m map[string]json.RawMessage) { m["sourceRevision"] = json.RawMessage(`"abc"`) }},
		{name: "mismatched filename ID", edit: func(m map[string]json.RawMessage) {
			m["id"] = json.RawMessage(`"8ad20f51-6067-4dc4-a072-54c94d7735de"`)
			m["containerName"] = json.RawMessage(`"pi-sesh-8ad20f51-6067-4dc4-a072-54c94d7735de"`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := t.TempDir()
			store := testStore(t, path)
			record := validRecord()
			data, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			tt.edit(fields)
			data, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "sessions", recordPath(record.ID)), data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(context.Background(), record.ID); !errors.Is(err, ErrCorruptRecord) {
				t.Fatalf("Get error = %v, want ErrCorruptRecord", err)
			}
		})
	}
}

func TestStoreRejectsCorruptReservations(t *testing.T) {
	const id = "7ad20f51-6067-4dc4-a072-54c94d7735de"
	for _, data := range []string{
		`{`,
		`null`,
		fmt.Sprintf(`{"version":1,"name":"my-session","id":%q}`, id),
		fmt.Sprintf(`{"version":2,"name":"other-session","id":%q}`, id),
		`{"version":2,"name":"my-session","id":"../other"}`,
		fmt.Sprintf(`{"version":2,"name":"my-session","id":%q,"extra":true}`, id),
	} {
		t.Run(data, func(t *testing.T) {
			store := testStore(t, t.TempDir())
			if err := store.names.WriteFile(namePath("my-session"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.readName(context.Background(), "my-session"); !errors.Is(err, ErrCorruptRecord) {
				t.Fatalf("readName error = %v, want ErrCorruptRecord", err)
			}
		})
	}
}

func TestStorePreservesTTY(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			store := testStore(t, t.TempDir())
			record := validRecord()
			record.TTY = tty
			createRecord(t, store, record)
			got, err := store.Get(context.Background(), record.ID)
			if err != nil || got.TTY != tty {
				t.Fatalf("loaded tty = %t, error = %v, want %t", got.TTY, err, tty)
			}
		})
	}
}

func TestStoreRejectsMalformedJSON(t *testing.T) {
	for _, data := range []string{`{`, `null`, `[]`, `{} {}`, `{} garbage`, strings.Repeat(" ", maxStoreFileBytes+1)} {
		t.Run(fmt.Sprintf("length-%d", len(data)), func(t *testing.T) {
			path := t.TempDir()
			store := testStore(t, path)
			id := validRecord().ID
			if err := os.WriteFile(filepath.Join(path, "sessions", recordPath(id)), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Get(context.Background(), id); !errors.Is(err, ErrCorruptRecord) {
				t.Fatalf("Get error = %v, want ErrCorruptRecord", err)
			}
		})
	}
}

func TestStoreRejectsUnsafeRecordFiles(t *testing.T) {
	for _, kind := range []string{"public file", "directory", "symlink", "FIFO"} {
		t.Run(kind, func(t *testing.T) {
			path := t.TempDir()
			store := testStore(t, path)
			record := validRecord()
			target := filepath.Join(path, "sessions", recordPath(record.ID))
			switch kind {
			case "public file":
				if err := os.WriteFile(target, []byte(`{}`), 0o644); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(t.TempDir(), "foreign.json"), target); err != nil {
					t.Fatal(err)
				}
			case "FIFO":
				if err := syscall.Mkfifo(target, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := store.Get(context.Background(), record.ID); !errors.Is(err, ErrUnsafeStore) {
				t.Fatalf("Get error = %v, want ErrUnsafeStore", err)
			}
		})
	}
}

func TestOpenStoreRejectsUnsafeDirectories(t *testing.T) {
	for _, location := range []string{"root", "sessions", "names"} {
		for _, kind := range []string{"public", "symlink"} {
			t.Run(location+"/"+kind, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state")
				target := path
				if location != "root" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
					target = filepath.Join(path, location)
				}
				if kind == "public" {
					if err := os.Mkdir(target, 0o755); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Symlink(t.TempDir(), target); err != nil {
					t.Fatal(err)
				}
				store, err := OpenStore(path)
				if store != nil {
					if closeErr := store.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				if !errors.Is(err, ErrUnsafeStore) {
					t.Fatalf("OpenStore error = %v, want ErrUnsafeStore", err)
				}
			})
		}
	}
}

func TestStoreDetectsChangedDirectoryPermissions(t *testing.T) {
	path := t.TempDir()
	store := testStore(t, path)
	record := validRecord()
	createRecord(t, store, record)
	target := filepath.Join(path, "sessions")
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(context.Background(), record.ID); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("Get error = %v, want ErrUnsafeStore", err)
	}
	if err := store.Create(context.Background(), record); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("Create error = %v, want ErrUnsafeStore", err)
	}
}

func TestStoreCreateLockCancellation(t *testing.T) {
	store := testStore(t, t.TempDir())
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
	if err := store.Create(ctx, validRecord()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Create error = %v, want context.DeadlineExceeded", err)
	}
}

func TestStoreFailedCreateRecovery(t *testing.T) {
	t.Run("release only an unpublished creation", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		record := validRecord()
		data, err := encodeStoreJSON(nameReservation{Version: RecordVersion, Name: record.Name, ID: record.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := publishStoreFile(context.Background(), store.names, namePath(record.Name), data); err != nil {
			t.Fatal(err)
		}
		if err := store.failedCreate(record, context.Canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := store.readName(context.Background(), record.Name); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unpublished name was not released: %v", err)
		}
	})
	t.Run("retain a published record and its name", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		record := validRecord()
		createRecord(t, store, record)
		if err := store.failedCreate(record, context.Canceled); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := store.Get(context.Background(), record.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.readName(context.Background(), record.Name); err != nil {
			t.Fatal(err)
		}
	})
}

func TestStoreDoesNotReleaseAnotherReservation(t *testing.T) {
	store := testStore(t, t.TempDir())
	record := validRecord()
	otherID := ID("8ad20f51-6067-4dc4-a072-54c94d7735de")
	data, err := encodeStoreJSON(nameReservation{Version: RecordVersion, Name: record.Name, ID: otherID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publishStoreFile(context.Background(), store.names, namePath(record.Name), data); err != nil {
		t.Fatal(err)
	}
	if err := store.failedCreate(record, context.Canceled); !errors.Is(err, ErrUnsafeStore) {
		t.Fatalf("cleanup error = %v, want ErrUnsafeStore", err)
	}
	reservation, err := store.readName(context.Background(), record.Name)
	if err != nil || reservation.ID != otherID {
		t.Fatalf("foreign reservation changed: %+v, %v", reservation, err)
	}
	if err := store.Create(context.Background(), record); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("Create error = %v, want reservation to stay reserved", err)
	}
}

func TestStoreList(t *testing.T) {
	t.Run("empty store", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		records, err := store.List(context.Background())
		if err != nil || len(records) != 0 {
			t.Fatalf("records = %v, error = %v, want empty result", records, err)
		}
	})
	t.Run("updated records in filename order", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		first := validRecord()
		second := first
		second.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
		second.Name = "second-session"
		second.ContainerName = "pi-sesh-" + string(second.ID)
		createRecord(t, store, second)
		createRecord(t, store, first)
		first.ContainerID = strings.Repeat("b", 64)
		if err := store.Update(context.Background(), first); err != nil {
			t.Fatal(err)
		}
		if err := store.sessions.WriteFile(".unfinished.json.random.tmp", []byte("untouched"), 0o600); err != nil {
			t.Fatal(err)
		}
		records, err := store.List(context.Background())
		if err != nil || len(records) != 2 {
			t.Fatalf("records = %v, error = %v, want two records", records, err)
		}
		if records[0] != first || records[1] != second {
			t.Fatalf("unexpected listed records: %+v", records)
		}
		data, err := store.sessions.ReadFile(".unfinished.json.random.tmp")
		if err != nil || string(data) != "untouched" {
			t.Fatalf("listing changed staging input: %q, %v", data, err)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		store := testStore(t, t.TempDir())
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		records, err := store.List(ctx)
		if !errors.Is(err, context.Canceled) || records != nil {
			t.Fatalf("records = %v, error = %v, want nil and context.Canceled", records, err)
		}
	})
	tests := []struct {
		name    string
		setup   func(*testing.T, *Store, Record)
		wantErr error
	}{
		{name: "corrupt last record", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.WriteFile(recordPath(r.ID), []byte(`{`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsafe last record", wantErr: ErrUnsafeStore, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.Chmod(recordPath(r.ID), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "directory instead of last record", wantErr: ErrUnsafeStore, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.Remove(recordPath(r.ID)); err != nil {
				t.Fatal(err)
			}
			if err := s.sessions.Mkdir(recordPath(r.ID), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "invalid JSON filename", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			if err := s.sessions.WriteFile("not-a-uuid.json", []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unknown fields in last record", wantErr: ErrCorruptRecord, setup: func(t *testing.T, s *Store, r Record) {
			data, err := encodeStoreJSON(r)
			if err != nil {
				t.Fatal(err)
			}
			data = append([]byte(`{"unknown":true,`), data[1:]...)
			if err := s.sessions.WriteFile(recordPath(r.ID), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t, t.TempDir())
			first := validRecord()
			last := first
			last.ID = "8ad20f51-6067-4dc4-a072-54c94d7735de"
			last.Name = "last-session"
			last.ContainerName = "pi-sesh-" + string(last.ID)
			createRecord(t, store, first)
			createRecord(t, store, last)
			tt.setup(t, store, last)
			records, err := store.List(context.Background())
			if !errors.Is(err, tt.wantErr) || records != nil {
				t.Fatalf("records = %v, error = %v, want nil and %v", records, err, tt.wantErr)
			}
			for _, record := range []Record{first, last} {
				if _, err := store.names.Lstat(namePath(record.Name)); err != nil {
					t.Fatalf("listing changed a reservation: %v", err)
				}
				if _, err := store.sessions.Lstat(recordPath(record.ID)); err != nil {
					t.Fatalf("listing removed invalid metadata: %v", err)
				}
			}
		})
	}
}

func TestStoreResolve(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(*testing.T, *Store, Record)
		lookup  string
		wantErr error
	}{
		{name: "saved name", lookup: "my-session"},
		{name: "missing name", lookup: "not-reserved", wantErr: ErrNotFound},
		{name: "case-sensitive name", lookup: "My-session", wantErr: ErrNotFound},
		{name: "corrupt reservation", lookup: "my-session", wantErr: ErrCorruptRecord,
			setup: func(t *testing.T, store *Store, record Record) {
				if err := store.names.WriteFile(namePath(record.Name), []byte(`{`), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "unsafe reservation", lookup: "my-session", wantErr: ErrUnsafeStore,
			setup: func(t *testing.T, store *Store, record Record) {
				if err := store.names.Chmod(namePath(record.Name), 0o644); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "corrupt record", lookup: "my-session", wantErr: ErrCorruptRecord,
			setup: func(t *testing.T, store *Store, record Record) {
				if err := store.sessions.WriteFile(recordPath(record.ID), []byte(`{`), 0o600); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "unsafe record", lookup: "my-session", wantErr: ErrUnsafeStore,
			setup: func(t *testing.T, store *Store, record Record) {
				if err := store.sessions.Chmod(recordPath(record.ID), 0o644); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "record name mismatch", lookup: "my-session", wantErr: ErrCorruptRecord,
			setup: func(t *testing.T, store *Store, record Record) {
				record.Name = "other-session"
				data, err := encodeStoreJSON(record)
				if err != nil {
					t.Fatal(err)
				}
				if err := store.sessions.WriteFile(recordPath(record.ID), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "reserved name without record", lookup: "my-session", wantErr: ErrNotFound,
			setup: func(t *testing.T, store *Store, record Record) {
				if err := store.sessions.Remove(recordPath(record.ID)); err != nil {
					t.Fatal(err)
				}
			}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := testStore(t, t.TempDir())
			record := validRecord()
			createRecord(t, store, record)
			if tt.setup != nil {
				tt.setup(t, store, record)
			}
			got, err := store.Resolve(context.Background(), tt.lookup)
			if _, statErr := store.names.Lstat(namePath(record.Name)); statErr != nil {
				t.Fatalf("lookup changed the original reservation: %v", statErr)
			}
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Resolve error = %v, want %v", err, tt.wantErr)
				}
				if got != (Record{}) {
					t.Fatalf("error returned a usable record: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != record {
				t.Fatalf("Resolve returned %+v, want %+v", got, record)
			}
		})
	}
}
