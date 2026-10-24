package session

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

var (
	// ErrNotFound means the requested metadata file is absent, not that a container is absent.
	ErrNotFound = errors.New("session metadata not found")
	// ErrAlreadyExists means an ID or name is reserved. Existing data is not overwritten.
	ErrAlreadyExists = errors.New("session identity already reserved")
	// ErrInvalidRecord means caller-supplied record fields failed validation.
	ErrInvalidRecord = errors.New("invalid session record")
	// ErrCorruptRecord means saved metadata cannot safely identify the requested session.
	ErrCorruptRecord = errors.New("corrupt session metadata")
	// ErrUnsafeStore means a filesystem type, owner, permission, or identity check failed.
	ErrUnsafeStore = errors.New("unsafe session store")
)

// Store persists session metadata in an explicitly selected private directory.
// Create reserves identities but does not create containers or own workspaces.
// Call Close to release its directory handles. Create and Update share a metadata
// lock. Container lifecycle locking and metadata removal are not implemented.
type Store struct {
	root     *os.Root
	sessions *os.Root
	names    *os.Root
}

type nameReservation struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	ID      ID     `json:"id"`
}

// OpenStore opens or creates an explicitly selected absolute store directory.
// Existing directories must be user-owned, private, and not symbolic links.
// It does not select an XDG default or import TypeScript state automatically.
func OpenStore(path string) (*Store, error) {
	root, err := openStoreRoot(path)
	if err != nil {
		return nil, err
	}
	sessions, err := openStoreDirectory(root, "sessions")
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	names, err := openStoreDirectory(root, "names")
	if err != nil {
		return nil, errors.Join(err, sessions.Close(), root.Close())
	}
	return &Store{root: root, sessions: sessions, names: names}, nil
}

// Close releases directory handles. It does not remove files or session resources.
func (s *Store) Close() error {
	return errors.Join(s.names.Close(), s.sessions.Close(), s.root.Close())
}

// Create validates and saves a new record and its name reservation. Existing
// identities are never overwritten. The two files are published separately;
// an interrupted create can leave a reservation for explicit recovery.
func (s *Store) Create(ctx context.Context, record Record) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	// Encode before reserving a name, so encoding failures allocate no state.
	data, err := encodeStoreJSON(record)
	if err != nil {
		return err
	}
	if err := s.checkDirectories(); err != nil {
		return err
	}
	unlock, err := s.lockMetadata(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	path := recordPath(record.ID)
	if _, statErr := s.sessions.Lstat(path); statErr == nil {
		return fmt.Errorf("%w: session ID %s", ErrAlreadyExists, record.ID)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("check session ID: %w", statErr)
	}

	reservation := nameReservation{Version: RecordVersion, Name: record.Name, ID: record.ID}
	nameData, err := encodeStoreJSON(reservation)
	if err != nil {
		return err
	}
	reserved, err := publishStoreFile(ctx, s.names, namePath(record.Name), nameData)
	if err != nil {
		if reserved {
			return s.failedCreate(record, err)
		}
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: name %q", ErrAlreadyExists, record.Name)
		}
		return fmt.Errorf("reserve session name: %w", err)
	}

	_, err = publishStoreFile(ctx, s.sessions, path, data)
	if err != nil {
		return s.failedCreate(record, err)
	}
	return nil
}

// Update atomically replaces an existing session record while preserving its
// identity and name reservation. In the current schema, only a pending container
// ID can be bound to a real ID; all other saved facts stay fixed. Repeating the
// same update is safe. This method does not verify runtime ownership.
//
// Failure before publication leaves the old record intact. A sync or unlock error
// after publication does not prove that the update failed; load the record again.
func (s *Store) Update(ctx context.Context, record Record) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := record.Validate(); err != nil {
		return err
	}
	data, err := encodeStoreJSON(record)
	if err != nil {
		return err
	}
	if err := s.checkDirectories(); err != nil {
		return err
	}
	unlock, err := s.lockMetadata(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	currentData, info, err := readStoreFile(ctx, s.sessions, recordPath(record.ID))
	if err != nil {
		return metadataReadError("read session for update", err)
	}
	current, err := decodeRecord(currentData, record.ID)
	if err != nil {
		return err
	}
	reservation, err := s.readName(ctx, current.Name)
	if err != nil {
		return fmt.Errorf("%w: cannot verify update reservation: %w", ErrCorruptRecord, err)
	}
	if reservation.ID != current.ID {
		return fmt.Errorf("%w: update reservation points to another session", ErrCorruptRecord)
	}
	if err := validateRecordUpdate(current, record); err != nil {
		return err
	}
	if err := replaceStoreFile(ctx, s.sessions, recordPath(record.ID), data, info); err != nil {
		return fmt.Errorf("update session: %w", err)
	}
	return nil
}

// Get loads a record by its immutable ID. Missing metadata does not establish
// runtime absence. Every read checks file safety, JSON shape, and record identity.
func (s *Store) Get(ctx context.Context, id ID) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	if err := id.Validate(); err != nil {
		return Record{}, err
	}
	if err := s.checkDirectories(); err != nil {
		return Record{}, err
	}
	data, _, err := readStoreFile(ctx, s.sessions, recordPath(id))
	if err != nil {
		return Record{}, metadataReadError("read session", err)
	}

	return decodeRecord(data, id)
}

func decodeRecord(data []byte, id ID) (Record, error) {
	// The pointer distinguishes a required false value from missing or null tty.
	type recordJSON Record
	var record Record
	wire := struct {
		*recordJSON
		TTY *bool `json:"tty"`
	}{recordJSON: (*recordJSON)(&record)}
	if err := decodeStoreJSON(data, &wire); err != nil {
		return Record{}, fmt.Errorf("%w: decode session: %w", ErrCorruptRecord, err)
	}
	if wire.TTY == nil {
		return Record{}, fmt.Errorf("%w: tty must be a boolean", ErrCorruptRecord)
	}
	record.TTY = *wire.TTY
	if err := record.Validate(); err != nil {
		return Record{}, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	if record.ID != id {
		return Record{}, fmt.Errorf("%w: record ID does not match its filename", ErrCorruptRecord)
	}
	return record, nil
}

// List returns validated saved records in filename order for later runtime
// inspection. It reads metadata only, not container status. A bad or unreadable
// record fails the whole query with a nil result; lookup never repairs or deletes
// files. The result is not an atomic snapshot across multiple records.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	records, err := s.recordIDs(ctx)
	if err != nil {
		return nil, err
	}
	var result []Record
	for _, id := range records {
		record, err := s.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}

// Resolve loads a session by its case-sensitive name and verifies that its
// reservation and record agree. Lookup never repairs or releases a reservation.
// Saved metadata does not establish live container state or ownership.
func (s *Store) Resolve(ctx context.Context, name string) (Record, error) {
	reservation, err := s.readName(ctx, name)
	if err != nil {
		return Record{}, err
	}

	record, err := s.Get(ctx, reservation.ID)
	if err != nil {
		return Record{}, err
	}
	if record.Name != name {
		return Record{}, fmt.Errorf("%w: record name does not match the requested name", ErrCorruptRecord)
	}

	return record, err
}

func (s *Store) readName(ctx context.Context, name string) (nameReservation, error) {
	if err := ctx.Err(); err != nil {
		return nameReservation{}, err
	}
	if err := ValidateName(name); err != nil {
		return nameReservation{}, err
	}
	if err := s.checkDirectories(); err != nil {
		return nameReservation{}, err
	}
	data, _, err := readStoreFile(ctx, s.names, namePath(name))
	if err != nil {
		return nameReservation{}, metadataReadError("read name reservation", err)
	}
	var reservation nameReservation
	if err := decodeStoreJSON(data, &reservation); err != nil {
		return nameReservation{}, fmt.Errorf("%w: decode reservation: %w", ErrCorruptRecord, err)
	}
	if reservation.Version != RecordVersion || reservation.Name != name {
		return nameReservation{}, fmt.Errorf("%w: reservation version or name mismatch", ErrCorruptRecord)
	}
	if err := reservation.ID.Validate(); err != nil {
		return nameReservation{}, fmt.Errorf("%w: %w", ErrCorruptRecord, err)
	}
	return reservation, nil
}

// A failed Create may release only its own new alias, and only after proving that
// no record was published. It never removes an existing or uncertain record.
func (s *Store) failedCreate(record Record, cause error) error {
	if _, err := s.sessions.Lstat(recordPath(record.ID)); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("create failed; session %q remains reserved for recovery: %w", record.Name, errors.Join(cause, err))
	}
	// Cleanup still needs to run after the caller cancels the operation.
	if err := s.checkDirectories(); err != nil {
		return errors.Join(cause, err)
	}
	data, info, err := readStoreFile(context.Background(), s.names, namePath(record.Name))
	if err != nil {
		return errors.Join(cause, err)
	}
	var reservation nameReservation
	if err := decodeStoreJSON(data, &reservation); err != nil {
		return errors.Join(cause, ErrUnsafeStore, err)
	}
	if reservation.Version != RecordVersion || reservation.Name != record.Name || reservation.ID != record.ID {
		return fmt.Errorf("create failed; name ownership cannot be verified: %w", errors.Join(cause, ErrUnsafeStore))
	}
	err = removeStoreFile(s.names, namePath(record.Name), info)
	if err != nil {
		return fmt.Errorf("create failed; name remains reserved for recovery: %w", errors.Join(cause, err))
	}
	return fmt.Errorf("create session: %w", cause)
}

func (s *Store) checkDirectories() error {
	for _, root := range []*os.Root{s.root, s.sessions, s.names} {
		info, err := root.Stat(".")
		if err != nil {
			return err
		}
		if err := checkStoreInfo(info, true); err != nil {
			return err
		}
	}
	return nil
}

func recordPath(id ID) string { return string(id) + ".json" }

func namePath(name string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name)) + ".json"
}

func metadataReadError(operation string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w: %w", operation, ErrNotFound, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// Keep a typed encoder boundary at callers; this helper shares only JSON mechanics.
func encodeStoreJSON(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode session metadata: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxStoreFileBytes {
		return nil, fmt.Errorf("session metadata exceeds %d bytes", maxStoreFileBytes)
	}
	return data, nil
}
