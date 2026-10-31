# Runtime inspection

For a source-by-source call sequence, see the [Podman control-flow guide](podman-control-flow.md).

## Current implementation

The Podman adapter implements host preflight and verified container inspection. The manager's `Inspect` and `List` methods combine saved records with runtime observations and preserve errors without changing stored data. Their tests use fake inspectors. The adapter's `ImageExists` method checks local availability without pulling an image. Its tests use a fake process runner.

Container-only `Client.Stop` and `Client.Remove` verify ownership and the required final state. No CLI, container creation, execution, or session-level stop/remove is implemented by this slice. Ordinary tests use fake process runners and do not require a live engine.

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

## Container teardown

`Client.Stop` and `Client.Remove` are implemented and tested with fake runners. The following contract distinguishes container-only operations from later session-level cleanup. `runtime.ErrInvalidState` identifies state refusals and unverified operation results.

Both methods must call the existing verified `Inspect` path before changing a container. When a container is observed, they must copy its immutable ID into the local target before subsequent commands or inspections. The initial name lookup supports recovery; it must not remain the mutation selector.

Stop refuses an initially missing or paused container. It requests a 10-second stop only for a running container, then inspects again. Exited, stopped, or missing is a successful final state. Created is not stopped. Other initially permitted states skip the stop command but still need the final inspection. On failure the method returns a zero inspection and an error.

Remove refuses a running or paused container and never stops it implicitly. For a present container, it invokes `container rm`, then verifies absence twice. An invocation error stops the operation. A nonzero rm status alone neither proves success nor prevents success if the following inspections prove absence, as in TypeScript. If the container was initially missing, remove still performs one more inspection.

These methods operate on containers only. They must not delete records, workspaces, or resources. A future session-level remove must serialize mutations and recheck absence before deleting owned host data. Neither a cancelled caller nor a successful mutation command alone establishes the final container state.

The [control-flow guide](podman-control-flow.md#8-container-teardown) shows the implemented Stop and Remove paths.

## Container creation inputs

`runtime.CreateRequest` and pure request/environment validation are implemented. They describe prepared host workspace paths, immutable image IDs, literal container argv, explicit environment values, network/workspace modes, and terminal selection. Trusted host UID/GID and SELinux/terminal values stay separate from project input.

Secure argv construction and create-output parsing are implemented and tested. `buildCreateArgs` validates once, then composes identity, policy, workspace, environment, terminal, and workload sections. `parseContainerID` normalizes valid output and rejects the pending marker. Create/start invocation remains unimplemented. These pure functions do not resolve filesystem paths, establish workspace ownership, or save recovery metadata. See the [container creation boundary](container-creation.md) for the validation contract and prerequisites before live creation.

## Still pending

- Add container creation/start capabilities using the resolved immutable image ID.
- Connect workspace preparation and the first mounted-session lifecycle.
- Add resource-aware inspection cleanup when the resource model exists.
- Add session-level removal only with verified runtime absence and host-data ownership checks.
- Add opt-in real Podman integration tests and the thin CLI.
