# Detached mounted-session lifecycle

## Status

The session manager implements Start, Inspect, List, Stop, and Remove for detached mounted sessions without resources. Tests run the complete lifecycle with a fake engine and real private metadata files. Workspace validation, per-session locking, metadata deletion, and startup recovery are implemented.

The concrete Podman adapter implements `Client.Create` and `Client.StartDetached`. A compile-time assertion checks the complete `session.Runtime` interface. The approved live integration test passed on rootless Podman 6.1.4 using `localhost/go-sesh-smoke:1`. No CLI, copy provisioning, resource staging, exec, logs, or terminal attachment is implemented by this path.

## API and ownership

`session.Manager` consumes `session.Runtime` and a caller-owned `*session.Store`. The caller opens the store and closes it after all operations finish. The manager does not own the process environment, CLI flags, output formatting, or scheduling.

`StartRequest` contains `Name`, `Image`, `WorkspacePath`, literal `Command`, explicit `Environment`, and optional `Network`. Empty network defaults to `none`. The image must already exist locally. The request always uses a mounted workspace, detached start, and no TTY. It does not load project configuration.

The caller owns the supplied directory before and after every operation. Neither successful removal nor startup rollback deletes that directory.

## Execution order

```text
Manager.Start(request)
  validate name, image input, network, and literal argv
  workspace.PrepareMount(path)
    resolve to an existing canonical directory
    reject unsafe volume syntax and sensitive host paths
  refuse overlap between workspace and private session state
  Runtime.Preflight(network)
  Runtime.ImageID(image reference)
  generate a new UUID and construct a pending record
  acquire per-session filesystem lock
    Store.Create(pending record and name reservation)
    Runtime.Create(resolved request)
    bind returned container ID with Store.Update
    Runtime.StartDetached(pinned target)
    validate final observation and caller context
    return View{Record, Inspection}
  release lock

Manager.Stop(id)
  acquire session lock
    load and validate saved record
    Runtime.Stop(target)
    require a valid exited/stopped/missing observation
    return saved record and verified observation
  release lock

Manager.Remove(id)
  acquire session lock
    load and validate saved record
    refuse copy records until owned-copy cleanup exists
    Runtime.Remove(target)
    Runtime.Inspect(target) -> require verified absence again
    Store.Delete(the exact observed record)
      remove record before releasing its matching alias
  release lock
```

A successful start can already be exited for a short-lived command. It does not guarantee that the workload remains running. Stop retains the record and alias. Normal remove never stops a running or paused container implicitly.

Inspect and List remain read-only. They do not acquire mutation locks, restart containers, or clean resources. Their results are observations, not atomic multi-session snapshots.

## Failure and recovery

A pending record is durable before the create command can run. A create error does not prove absence. Startup rollback uses the stable session name when no container ID was returned and a pinned ID when one is known.

Rollback uses `Runtime.RollbackCreate`, not normal Remove. As in TypeScript, it may force-remove a verified owned container after a failed startup. It must check both ownership labels before mutation and verify absence afterward. This permission does not apply to user-requested Remove.

The manager then performs another absence check before deleting metadata. Failed label checks, malformed observations, engine failures, changed records, or uncertain absence preserve recovery state.

Cancellation recovery uses a fresh context that retains caller values but not cancellation, with a 30-second deadline. It runs while the session lock is still held. A timeout remains uncertainty; it does not establish that a workload stopped.

`StartError` carries the generated ID, name, original/recovery error chain, and `RecoveryRequired`. If recovery is required, inspect the saved state before retrying. `errors.Is` and `errors.As` retain their normal meaning through the error chain. A metadata sync/publication error can leave a reservation even when startup did not reach the engine.

Metadata deletion removes the record before the alias. An interruption can leave an orphaned reservation for explicit recovery. Lock files remain intentionally and are not session records. No schema migration or automatic orphan cleanup occurs.

## Verification

The ordinary end-to-end test does not require Podman:

```sh
go test -v ./internal/session -run '^TestMountedSessionWorkflow$'
go test ./...
go vet ./...
go test -race ./...
```

Other tests cover startup side effects followed by errors, cancellation after create/start, rollback failures, uncertain absence, ownership mismatches, invalid records, alias collisions, unsupported copy cleanup, filesystem locks, and source preservation.

The live test is opt-in. It requires an existing local image with `/bin/sh` and `sleep`. It never pulls or builds images. Do not run it without operator approval:

```sh
PI_SESH_PODMAN_TESTS=1 PI_SESH_TEST_IMAGE=localhost/go-sesh-smoke:1 \
  go test -v ./integration -run '^TestMountedSession$' -count=1
```

The local test image was built from the already-present `docker.io/library/node:22-alpine` image by immutable ID. Its entrypoint was cleared and its default command set to `/bin/sh`. The build used `--pull=never --network=none`; it did not download images or packages. The dedicated image remains available for reruns.

The verified run completed create, detached start, workspace output, stop, and removal. No test containers or new temporary state directories remained afterward. The existing session container IDs were unchanged.

The test starts a workload in a disposable mounted directory, waits for its output file, stops/removes the session, and checks that source output remains. It uses a private temporary state tree without automatic test-directory deletion. If cleanup cannot be verified, it preserves the tree and reports its path for recovery.
