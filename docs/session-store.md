# Session store

The store saves session metadata. It does not start containers, inspect runtime state, or own source directories.

## Implemented path

`OpenStore(path)` opens an explicitly selected absolute directory. `Store.Create(ctx, record)` reserves a name and publishes a record. `Store.Get(ctx, id)` loads and validates that record. Close the store, open it again, and the record remains available.

The test `TestStoreCreateReopenGet` exercises this path with actual files, not a fake filesystem:

```sh
go test -v ./internal/session -run '^TestStoreCreateReopenGet$'
```

`Store.Resolve(ctx, name)` loads the name reservation, loads its referenced record, and checks that the record has the requested name. Read errors retain their categories. A failed lookup does not delete or repair saved data. This converts a human-readable alias into verified saved identity; it does not inspect the container.

`Store.Update(ctx, record)` atomically binds a pending record to a container ID. Other saved facts and the name reservation stay fixed. Repeating the same binding is safe; replacing a bound ID or resetting it to pending is rejected. Update never creates a missing record or repairs an unsafe existing record.

`Store.List(ctx)` loads every candidate through `Get` in filename order. It fails with a nil result on any error and leaves staging files and reservations untouched. It returns metadata, not live container state or an atomic multi-record snapshot.

`Store.Delete(ctx, expected)` removes only a mount-session record whose saved facts still match the expected record and whose alias still points to that ID. The caller must hold the session lock and freshly verify container absence. Delete checks private files, ownership, and inode identity. It removes the record before the alias; an interrupted removal can leave an orphaned reservation for explicit recovery. Copy records are refused until owned-copy cleanup is implemented. It never deletes source directories.

## Record schema

- Go records use version 2. TypeScript version 1 is rejected; there is no automatic migration or import.
- Fields include identity, name, container identity, image ID, source/workspace paths, modes, TTY selection, and creation time.
- Go uses `time.Time` for creation time and saves it as a JSON timestamp.
- A pending container ID uses the existing 64-zero marker before container creation. It does not prove that no container exists.
- Records do not include live runtime state or Git provenance.
- Resource ownership metadata is not implemented yet. Unknown fields are rejected, including a resource array, so loading cannot silently discard cleanup obligations.
- File names must agree with the record ID. Name reservations must agree with the requested name and contain a valid session ID.
- A syntactically valid record is not proof of container or workspace ownership.

## Layout and permissions

```text
<explicit store root>/
  .create.lock
  .session-<session UUID>.lock
  sessions/<session UUID>.json
  names/<base64url name>.json
```

New directories use mode `0700`. New metadata and lock files use mode `0600`. Existing files and directories must belong to the invoking user and have no group or other permissions. The store rejects unsafe permissions rather than changing existing data automatically.

Directory handles anchor operations with Go's `os.Root`. Reads reject symlinks and non-regular files, check opened file identity and ownership, and bound metadata to 1 MiB per file. JSON decoding rejects unknown fields, malformed data, and trailing JSON values. Required TTY data must be a boolean, not missing or null.

## Creation, interruption, and concurrency

Creation validates and encodes the record, takes a filesystem metadata lock, checks ID availability, reserves the name, and publishes the record. Each file is written privately and synced before atomic create-if-absent publication. Create never overwrites an existing file.

The alias and record are two separate files, not an atomic two-file transaction. A process crash can leave a reservation without a record. Keep that reservation for explicit recovery. Do not automatically reuse a reserved name because metadata is missing.

On an observed creation failure, the store releases its new reservation only if it proves that no record was published and the reservation still matches the attempted session. Uncertain or published data remains reserved for recovery. Cleanup checks file identity before unlinking temporary files or the matching reservation.

Updates take the same lock, validate the current record and name binding, and stage a complete private replacement. A checked rename publishes the replacement atomically. An already-open reader retains the old complete file. Failure before publication leaves the old record intact. A sync or unlock error after publication does not prove the update was rolled back; callers must reload the record rather than assume its contents.

The metadata lock serializes Create, Update, and Delete across cooperating store handles and processes. Its filename remains `.create.lock` so existing handles share the same lock. Manager Start/Stop/Remove also use `.session-<UUID>.lock` files. Lock order is session, then metadata. Session lock files remain after removal; deleting a lock file could allow two callers to lock different inodes for the same identity.

Lock waits observe context cancellation and check lock-file identity after acquisition. Other filesystem calls check cancellation between operations; context does not interrupt a blocked filesystem syscall. Inspect and List do not acquire mutation locks.

## Boundaries still to implement

- Resource ownership metadata and copy ownership records.
- Explicit legacy-state import/compatibility. The CLI already selects a separate default `go-sesh` state directory.
- Resource-aware removal and safe release of owned copy workspaces.

Metadata absence is never proof of container absence. The manager verifies runtime ownership and absence before requesting metadata removal. See the [mounted-session lifecycle](mounted-session.md) for startup ordering, cancellation recovery, and the current adapter limitations.
