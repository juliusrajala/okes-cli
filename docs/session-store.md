# Session store: first persistence slice

The store saves session metadata. It does not start containers, inspect runtime state, or own source directories.

## Implemented path

`OpenStore(path)` opens an explicitly selected absolute directory. `Store.Create(ctx, record)` reserves a name and publishes a record. `Store.Get(ctx, id)` loads and validates that record. Close the store, open it again, and the record remains available.

The test `TestStoreCreateReopenGet` exercises this path with actual files, not a fake filesystem:

```sh
go test -v ./internal/session -run '^TestStoreCreateReopenGet$'
```

`Store.Resolve(ctx, name)` loads the name reservation, loads its referenced record, and checks that the record has the requested name. Read errors retain their categories. A failed lookup does not delete or repair saved data. This converts a human-readable alias into verified saved identity; it does not inspect the container.

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
  sessions/<session UUID>.json
  names/<base64url name>.json
```

New directories use mode `0700`. New metadata and lock files use mode `0600`. Existing files and directories must belong to the invoking user and have no group or other permissions. The store rejects unsafe permissions rather than changing existing data automatically.

Directory handles anchor operations with Go's `os.Root`. Reads reject symlinks and non-regular files, check opened file identity and ownership, and bound metadata to 1 MiB per file. JSON decoding rejects unknown fields, malformed data, and trailing JSON values. Required TTY data must be a boolean, not missing or null.

## Creation, interruption, and concurrency

Creation validates and encodes the record, takes a filesystem creation lock, checks ID availability, reserves the name, and publishes the record. Each file is written privately and synced before atomic create-if-absent publication. Existing files are never overwritten.

The alias and record are two separate files, not an atomic two-file transaction. A process crash can leave a reservation without a record. Keep that reservation for explicit recovery. Do not automatically reuse a reserved name because metadata is missing.

On an observed creation failure, the store releases its new reservation only if it proves that no record was published and the reservation still matches the attempted session. Uncertain or published data remains reserved for recovery. Cleanup checks file identity before unlinking temporary files or the matching reservation.

A filesystem lock serializes creation across cooperating store handles and processes. Lock waits observe context cancellation. This is not the later per-session mutation lock needed by exec, stop, update, and remove.

## Boundaries still to implement

- Record updates, listing, and identity release/removal.
- Resource ownership metadata and copy ownership records.
- Default state-directory selection and explicit legacy-state compatibility.
- Session lifecycle and per-session mutation locking.

Metadata absence is never proof of container absence. No public deletion operation exists in this slice. Later lifecycle code must verify runtime ownership and absence before requesting record or resource removal.
