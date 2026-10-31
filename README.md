# go-sesh

A Go rewrite of pi-sesh for local rootless Podman development sessions. The CLI is not implemented yet.

## Current implementation

`internal/process` provides host subprocess execution with literal argv, explicit environments, working-directory and stdin selection, bounded stdout and stderr capture, and context cancellation. It uses the Go standard library.

The default capture limit is 16 MiB per output stream. An omitted environment means an empty child environment, not inheritance from the host. Callers must select the environment values to forward.

Cancellation stops the direct host process. It does not prove that a container workload or descendant process has stopped. A supplied stdin reader must not block indefinitely.

`internal/session` provides session ID/name validation and a file-backed store. It can reserve a name, save a version-2 record, reopen the store, and load the record by ID or its case-sensitive name. It can atomically bind a pending record to a container ID without changing the other saved session facts. It can list validated saved records without changing files. Matching mount-session records and aliases can be removed after verified container absence. See the [session store notes](docs/session-store.md) for safety rules and current limits.

`internal/runtime` defines neutral inspection/creation request types and supported state values. `internal/podman` implements restricted local CLI invocation, rootless host preflight, and ownership-checked container inspection. The session manager combines saved records with runtime observations for inspection and listing. Local-image probing and immutable image-ID resolution are implemented and tested with a fake process runner. See [runtime inspection](docs/runtime-inspection.md) for the contract and [Podman control-flow guide](docs/podman-control-flow.md) to trace calls through the code. Container-only stop/remove verify ownership and the required final state. The manager's detached mounted-session Start/Stop/Remove workflow is implemented and tested with a fake runtime, including recovery records, rollback, and per-session locks. The Podman adapter now implements `Create` and `StartDetached` and satisfies the full manager runtime interface. The approved live mounted-session smoke test passed on rootless Podman 6.1.4 with `localhost/go-sesh-smoke:1`. See the [mounted-session lifecycle](docs/mounted-session.md). The CLI is still pending. See the [container creation boundary](docs/container-creation.md).

`internal/workspace` prepares a canonical existing mounted directory without taking ownership. It refuses broad host roots, the host home, known credential/engine-state trees, and unsafe volume syntax. Copy preparation is not implemented. Mode validation accepts `mount`/`copy` and rejects obsolete `clone`. `internal/runtime` validates `none` and `pasta`; the initial manager defaults to `none`.

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

To run the complete mounted-session workflow with a fake engine:

```sh
go test -v ./internal/session -run '^TestMountedSessionWorkflow$'
```

Live Podman tests require explicit opt-in and a prepared local image. See the [mounted-session lifecycle](docs/mounted-session.md#verification) before running them.

## References

- [TypeScript behavior and approved Go changes](docs/behavior.md)
- [Go release scope](docs/scope.md)
- [Updated rewrite plan](docs/pi-sesh-go-rewrite-plan-updated.pdf)
- TypeScript reference implementation: `../pi-sesh/`
