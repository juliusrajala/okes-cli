# Podman control-flow guide

Use this guide beside the source. The trees show execution order, branches, and interface calls. They are a control-flow map, not one literal Go stack trace.

Inspection, manager listing, local image availability checks, and immutable image-ID resolution are implemented. The CLI is not implemented yet, so there is no CLI entry point to follow.

## Source reading order

| Step | Source | Look for |
| --- | --- | --- |
| 1 | [session/manager.go](../internal/session/manager.go) | `Manager`, `Inspector`, `View`, `Inspect`, `List` |
| 2 | [runtime/inspection.go](../internal/runtime/inspection.go) | `Target`, `Inspection`, `HostInfo` |
| 3 | [podman/client.go](../internal/podman/client.go) | `New`, `invoke`, `checked` |
| 4 | [podman/environment.go](../internal/podman/environment.go) | `buildHostEnvironment`, `resolveExecutable`, `within` |
| 5 | [podman/preflight.go](../internal/podman/preflight.go) | `Preflight`, `validateHost`, `decodeResponse` |
| 6 | [podman/inspect.go](../internal/podman/inspect.go) | `Inspect`, `containerExists`, `parseInspection` |
| 7 | [process/run.go](../internal/process/run.go) | `Run`, `exec.CommandContext`, `cmd.Run` |
| 8 | [process/capture.go](../internal/process/capture.go) | `capture.Write` and output-limit cancellation |
| 9 | [podman/image.go](../internal/podman/image.go) | `ImageExists`, `parseImageID`, `ImageID` |

## 1. Construct the client once

```text
podman.New(options)
  choose source environment
    SourceEnv == nil -> read os.Environ()
    otherwise        -> use the supplied entries
  buildHostEnvironment(source, projectDir)
    reject remote settings and CONTAINERS_* overrides
    resolve the project path through symlinks
    copy only allowlisted host variables
    require absolute HOME/XDG/temp paths
    filter executable search paths
    return sorted environment, search path, canonical project
  choose executable
    explicit absolute executable -> trusted caller selection
    otherwise -> resolveExecutable(filtered PATH, project)
      reject executable symlinks that point into the project
  choose Runner
    supplied Runner -> caller's implementation, usually a test fake
    nil Runner      -> process.Run
  return &Client{...}
```

`New` prepares data. It does not run Podman or check the engine's capabilities.

The caller can place this `*podman.Client` in `Manager.Runtime`. The client satisfies the `session.Inspector` interface because its method signature matches. Go needs no `implements` declaration.

The host environment belongs to the Podman subprocess. It is not a container environment or a host-home mount.

## 2. Inspect one session

Start at `Manager.Inspect` in [manager.go](../internal/session/manager.go).

```text
session.Manager.Inspect(ctx, sessionID)
  Store.Get(ctx, sessionID)
    validate and load the saved record
    failure -> return View{}, error; do not call the runtime
  build runtime.Target
    SessionID     = saved session UUID
    ContainerName = saved stable container name
    ContainerID   = saved container ID, unless it is pending
                    pending becomes an empty string
  m.Runtime.Inspect(ctx, target)             [interface call]
    podman.Client.Inspect(ctx, target)       [when wired to Podman]
      validateTarget(target)
      Client.Preflight(ctx, NetworkNone)     [see section 3]
      choose selector
        known ContainerID -> use that ID
        empty ContainerID -> use ContainerName
      Client.containerExists(ctx, selector)
        Client.invoke("container", "exists", selector)
        execution error -> return error
        status 1        -> return Inspection{State: Missing}
        status 0        -> continue
        other status    -> return ErrCommandFailed
      Client.checked("container", "inspect", "--format", "json", selector)
        execution error or nonzero status -> return error, not Missing
      parseInspection(stdout, target)       [see section 4]
  runtime error -> return View{}, error
  return View{Record, Inspection}, nil
```

### The two IDs have different jobs

| Value | Example shape | Job |
| --- | --- | --- |
| Session ID | UUID such as `7ad20f51-6067-4dc4-a072-54c94d7735de` | Check the session ownership label |
| Container ID | 64 hexadecimal characters | Select and verify one immutable container identity |
| Container name | `pi-sesh-<session UUID>` | Find a container when its ID has not been saved yet |

The all-zero pending marker belongs to storage. It is not a real Podman selector. The manager converts it to `""` before calling the adapter.

Inspection returns an observation. It does not rewrite the record, restart a container, or remove a missing session's metadata.

## 3. Preflight the host

Follow `Preflight` in [preflight.go](../internal/podman/preflight.go).

```text
Client.Preflight(ctx, requestedNetwork)
  validate network mode and supported Go host platform
  Client.checked("info", "--format", "json")
    Client.invoke(...) -> Runner(...)       [see section 5]
    nonzero status -> ErrCommandFailed
  decodeResponse(stdout, &hostJSON)
    malformed JSON or trailing data -> ErrInvalidResponse
  validateHost(info, requestedNetwork)
    require Podman >= 6.1.1
    require local rootless operation
    require cgroup v2, memory controller, PID controller
    require pasta backend only when pasta was requested
    unmet requirement -> ErrUnsupportedHost
  return runtime.HostInfo
```

Inspection requests network `none`. It does not need pasta merely to read container state, even if that container was started with pasta networking.

## 4. Turn engine JSON into a verified observation

Follow `parseInspection` in [inspect.go](../internal/podman/inspect.go).

```text
parseInspection(output, target)
  decode exactly one container from an array or object
  require container ID, status, and integer exit-code data
  choose Config.Labels, or the top-level Labels fallback
  verify ownership
    io.pi-sesh.owner      == "pi-sesh"
    io.pi-sesh.session-id == target.SessionID
    known container ID    == inspected ID
    mismatch -> ErrOwnership
  normalize engine state
    configured -> Created
    supported states -> their neutral runtime.State values
    unknown state -> ErrInvalidInspection
  include ExitCode only for Exited or Stopped
  return runtime.Inspection
```

Pointer fields in `containerJSON` distinguish missing/null values from valid zero values. An exit code of `0` is valid data; an omitted exit code is not the same thing.

A successful `exists` query proves presence, not ownership. Ownership checks must pass before the adapter returns container state.

## 5. The shared subprocess path

Every actual engine command uses this path from [client.go](../internal/podman/client.go) into [run.go](../internal/process/run.go).

```text
Client.invoke(ctx, args...)
  reject an already-cancelled context
  build process.Command
    Path = selected absolute executable
    Args = ["--remote=false", ...literal args]
    Env  = a copy of the restricted environment
  c.runner(ctx, command)
    process.Run(ctx, command)               [production default]
      validateCommand(path, args)
      validate working directory, environment entries, output limit
      create a child context and cancellation function
      create separate bounded stdout/stderr captures
      exec.CommandContext(childContext, path, args...)
      attach explicit Env, stdin, stdout, stderr
      cmd.Run()                            [os/exec starts and waits]
        output copying calls capture.Write
          overflow -> cancel child process
        input copying calls inputReader.Read
          non-EOF read error -> cancel child process
      collect exit code and captured output
      return Result, error
  wrap execution errors; otherwise return Result
```

No shell interprets the argv. An ordinary nonzero program exit is a `process.Result`, not automatically a Go error. The Podman operation decides what that status means.

| Interpreter | Status `0` | Status `1` | Other nonzero status |
| --- | --- | --- | --- |
| `Client.checked` | Success | Error | Error |
| `Client.containerExists` | Present | Absent | Error |
| `Client.ImageExists` | Present | Absent | Error |

A context cancellation kills the direct host process. It does not prove that descendants or a future in-container exec workload stopped. Lifecycle code must verify those outcomes separately.

## 6. List sessions

```text
session.Manager.List(ctx)
  Store.List(ctx)
    enumerate saved IDs and validate every record
    any failure -> return nil, error
  for each saved record
    Manager.Inspect(ctx, record.ID)
      re-read the record and use the same inspection path as section 2
    any failure -> discard partial views; return nil, error
    append the view
  return views, nil
```

Reusing `Inspect` keeps the identity and error behavior in one place. It currently re-reads each record and repeats preflight for each session. There is no batch query or cached preflight. The result is not an atomic multi-session snapshot.

With Podman wired in, each successful existing-container inspection runs three host commands: `info`, `container exists`, and `container inspect`. A verified missing container needs only the first two.

## 7. Local image probe and ID resolution

Follow [image.go](../internal/podman/image.go). The availability probe is implemented:

```text
Client.ImageExists(ctx, image)
  validateImageReference(image)
  Client.Preflight(ctx, NetworkNone)
  Client.invoke("image", "exists", image)
  interpret status 0 / 1 / error
```

It uses `invoke`, not `checked`, because image absence is an expected result.

Immutable image resolution follows this path:

```text
Client.ImageID(ctx, image)
  validateImageReference(image)
  Client.Preflight(ctx, NetworkNone)
  Client.checked("image", "inspect", "--format", "{{.Id}}", image)
    execution failure or nonzero status -> return empty ID and error
  parseImageID(stdout)
    trim surrounding whitespace
    remove one optional "sha256:" prefix
    require exactly 64 hexadecimal characters; preserve case
    invalid data -> return empty ID and ErrInvalidResponse
  return immutable image ID
```

The parser handles data only. The method handles command ordering and propagates failures. `ImageID` does not need to call `ImageExists`; a failed inspection is already an error.

Neither path pulls an image. A tag can be reassigned, so a later start must use the resolved immutable ID, not the original tag. Container creation remains future work.

Run the parser and adapter tests separately while following the code:

```sh
go test -v ./internal/podman -run '^TestParseImageID$'
go test -v ./internal/podman -run '^TestImageID$'
```

These tests are active. Parser tests cover both ID formats and malformed output. Adapter tests also cover exact argv, command failures, cancellation, and preflight rejection. They do not run a live Podman process.

## 8. What the tests replace

- [manager_test.go](../internal/session/manager_test.go) injects `inspectorFunc`. These tests stop at the runtime interface and do not invoke the Podman adapter.
- [client_test.go](../internal/podman/client_test.go) provides `scriptClient` with a fake `Runner`. Adapter tests check the argv and return scripted engine output without starting Podman.
- [contract_test.go](../internal/podman/contract_test.go) checks interface compatibility at compile time.
- [process tests](../internal/process/run_test.go) exercise actual host subprocesses using a helper test executable, not Podman.

Run the relevant layers while following the code:

```sh
go test -v ./internal/session -run '^TestManager(Inspect|List)$'
go test -v ./internal/podman
go test ./...
```

For the safety rules rather than the call sequence, see [runtime inspection](runtime-inspection.md).
