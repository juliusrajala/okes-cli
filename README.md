# go-sesh

A Go rewrite of pi-sesh for local rootless Podman development sessions. The CLI is not implemented yet.

## Current implementation

`internal/process` provides host subprocess execution with literal argv, explicit environments, working-directory and stdin selection, bounded stdout and stderr capture, and context cancellation. It uses the Go standard library.

The default capture limit is 16 MiB per output stream. An omitted environment means an empty child environment, not inheritance from the host. Callers must select the environment values to forward.

Cancellation stops the direct host process. It does not prove that a container workload or descendant process has stopped. A supplied stdin reader must not block indefinitely.

`internal/session` provides session ID/name validation and a file-backed store. It can reserve a name, save a version-2 record, reopen the store, and load the record by ID or its case-sensitive name. It can atomically bind a pending record to a container ID without changing the other saved session facts. It can list validated saved records without changing files. See the [session store notes](docs/session-store.md) for safety rules and current limits.

`internal/runtime` defines neutral inspection types and supported state values. `internal/podman` implements restricted local CLI invocation, rootless host preflight, and ownership-checked container inspection. The session manager combines saved records with runtime observations for inspection and listing. Local-image probing and immutable image-ID resolution are implemented and tested with a fake process runner. See [runtime inspection](docs/runtime-inspection.md) for the contract and [Podman control-flow guide](docs/podman-control-flow.md) to trace calls through the code. Container-only stop/remove verify ownership and the required final state. Container creation, session-level stop/remove, and the CLI are still pending.

`internal/workspace` validates the `mount` and `copy` mode names and rejects the removed `clone` mode. It does not prepare directories yet. `internal/runtime` also validates the supported network modes, `none` and `pasta`; it does not enable networking.

## Planned workspace boundary

The Go target accepts a local directory through `mount` or filesystem `copy`. It does not provision Git checkouts. Copy replaces the TypeScript Git clone mode; it is not implemented yet.

Supplied source directories remain externally owned. Go will own only its session copies. Future sandboxd providers can acquire checkouts, but sandboxd and Git publishing workflows are outside this repository's implementation scope. Git commands can still run inside a session as ordinary workload commands.

## Development

Use Go 1.26 or later. From the repository root, run:

```sh
go test ./...
go vet ./...
go test -race ./...
```

Use `go test -v ./...` to show individual test results. Unit tests do not require Podman or network access.

To exercise saving a session record and loading it after reopening the store:

```sh
go test -v ./internal/session -run '^TestStoreCreateReopenGet$'
```

## References

- [TypeScript behavior and approved Go changes](docs/behavior.md)
- [Go release scope](docs/scope.md)
- [Updated rewrite plan](docs/pi-sesh-go-rewrite-plan-updated.pdf)
- TypeScript reference implementation: `../pi-sesh/`
