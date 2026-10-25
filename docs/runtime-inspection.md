# Runtime inspection

For a source-by-source call sequence, see the [Podman control-flow guide](podman-control-flow.md).

## Current implementation

The Podman adapter implements host preflight and verified container inspection. The manager's `Inspect` and `List` methods combine saved records with runtime observations and preserve errors without changing stored data. Their tests use fake inspectors. The adapter's `ImageExists` method checks local availability without pulling an image. Its tests use a fake process runner.

No CLI, container creation, execution, stopping, or removal is implemented by this slice. Ordinary tests use fake process runners and do not require a live engine.

## Inspection call flow

```text
Manager.Inspect(session ID)
  Store.Get(session ID)
  Inspector.Inspect(runtime.Target)
    Podman host preflight
    Podman container exists
    Podman container inspect
    identity, ownership-label, and state validation
  View{Record, Inspection}

Manager.List()
  Store.List()
  Manager.Inspect() for each saved session
```

The manager consumes the small `session.Inspector` interface. Go matches implementations by their method signatures. `podman.Client` does not import the session package in production. The compile-time assertion in `internal/podman/contract_test.go` checks that it satisfies this interface.

`internal/runtime` owns neutral request and result types. Podman flags, JSON structs, and existence-command status interpretation remain inside `internal/podman`. We will add further runtime capabilities when their lifecycle consumers are implemented.

## Identity and state

- A manager request uses an immutable session ID. A human-readable name can be resolved through the store first.
- A pending saved container ID becomes an empty `runtime.Target.ContainerID`. The adapter then queries the stable container name and checks its session labels.
- A known container ID must match the inspection result exactly.
- Both the pi-sesh owner label and the session ID label must match.
- Only `container exists` status 1 establishes absence. Invocation failures, unsupported states, and invalid JSON return errors with no guessed state.
- The adapter converts supported engine states to the existing neutral state constants. `configured` becomes `created`. Exit codes are present only for exited or stopped containers.
- A view keeps the saved record separate from the current observation. Inspection does not bind or rewrite the saved record in this slice.
- Missing or uncertain containers do not trigger metadata or resource deletion.

## Host boundary

Every invocation uses an absolute executable and literal argv with `--remote=false`. The host environment forwards only the TypeScript allowlist. Remote settings and `CONTAINERS_*` overrides are rejected. Relative, unavailable, and project-contained PATH entries are excluded. Default executable resolution also rejects a symlink that points into the selected project.

`HOME` and XDG values support the host Podman process. They are not container mounts or automatic workload environment forwarding. Explicit executable selection is trusted host configuration, not a project setting.

Preflight requires Linux x86-64, Podman 6.1.1 or later, a local rootless engine, cgroup v2, and memory/PID controllers. Requested pasta networking also requires the pasta backend. Inspect requests preflight with network `none`, as in the TypeScript lifecycle lookup path.

The adapter keeps unknown engine JSON fields compatible with Podman additions. It rejects missing required identity/state data and trailing JSON values. This differs from saved-record parsing, which also rejects unknown fields to avoid discarding cleanup metadata.

## Local image probe

The future start path needs a local image check. `Client.ImageExists` validates its reference, preflights the host, and runs `image exists` through the existing invocation helper. Status 0 means present, status 1 means absent, and every other status or execution failure remains an error. This method never pulls an image.

Image availability is not immutable image resolution. `Client.ImageID` validates the reference, preflights with network `none`, and runs `image inspect --format {{.Id}} IMAGE` through `checked`. Every nonzero status is an error. It passes stdout to `parseImageID`, which trims surrounding whitespace and removes one optional `sha256:` prefix. The parser requires exactly 64 hexadecimal characters, preserves case, and wraps `ErrInvalidResponse` on failure. This matches the TypeScript `imageId` method and returns an ID compatible with the existing record schema.

Both functions are implemented and tested. They do not require a new runtime interface yet. Container creation with `--pull=never` remains later work.

## Still pending

- Add container creation/start capabilities using the resolved immutable image ID.
- Connect workspace preparation and the first mounted-session lifecycle.
- Add resource-aware inspection cleanup when the resource model exists.
- Add removal only with verified runtime absence and ownership checks.
- Add opt-in real Podman integration tests and the thin CLI.
