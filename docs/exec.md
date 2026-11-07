# Execution support

## Implementation status

The exec CLI parser, Podman adapter, bounded live output, identity checks, session transaction, and private receipt storage are implemented and unit-tested. Exec verifies cancellation and saves a final receipt through an independent cleanup path.

All transaction and receipt-finalization tests are active. No live Podman exec test has run yet.

## Command and identity

```text
sesh exec --session-id UUID [--cwd /workspace] [--timeout SECONDS] NAME -- PROGRAM [ARG ...]
```

The caller supplies both the name and its previously observed immutable UUID. The manager resolves the name under the session lock and checks that it still identifies that UUID. This preserves the TypeScript exec identity requirement. It does not silently execute in a replacement session after a name is reused.

The working directory is an absolute container path, not a host path. The default is `/workspace`. Timeout is optional and expressed in seconds. Explicit values must be greater than zero and at most 86400. The Go API uses `time.Duration` and represents an omitted timeout with zero.

Exec accepts 1 to 256 literal arguments. The program must be nonempty. Later arguments may be empty. No argument can contain a NUL byte. Workload arguments follow `--` and are not parsed as sesh flags.

TTY and shell construction are not part of this path. The process entry point supplies stdin. The runtime borrows all streams and never closes caller-owned descriptors. Plain output streams live to stdout/stderr while retaining bounded capture. The limit remains 16 MiB per stream. A destination write error or output overflow is an execution failure, not proof that the container workload stopped.

`--json` disables live output and encodes captured stdout/stderr with the session ID, execution ID, and exit code. Ordinary workload exits retain their status, including nonzero values. They do not produce an additional sesh diagnostic. Execution or persistence errors remain CLI failures.

Custom stdin readers and output writers must not block indefinitely. Cancellation cannot interrupt arbitrary Go `Read` or `Write` implementations. File stdin is inherited as a descriptor without a parent input-copy goroutine.

## Control flow

```text
cli.Run
  parse name, explicit session UUID, timeout, container cwd, literal argv
  construct borrowed streams; JSON selects capture-only output
  Manager.Exec
    validate request and clone argv
    acquire immutable session lock
    resolve name and compare UUID
    require a bound container ID
    inspect and require verified ownership and running state
    Manager.execLocked
      publish initial running receipt before workload execution
      derive optional workload timeout
      invoke Runtime.Exec with literal argv and pinned container ID
      finalize through an independently live cleanup path
        cancellation, timeout, or transport failure
          Runtime.CancelExec -> owned container stop --time 0
          verify stopped or missing
        ExecutionReceipt.finish
        atomically save final receipt, even after caller cancellation
    release lock
  return workload status or report execution/recovery failure
```

The session lock covers the transaction. Other lifecycle mutations wait for it. Cancellation recovery calls the runtime directly, not `Manager.Stop`, which would attempt to acquire the same lock again.

A lost host Podman client does not prove that an in-container command stopped. Cancellation therefore stops the whole session container and verifies its state. Normal user Stop retains its separate 10-second grace period. Unverified cancellation returns recovery guidance and must produce an error receipt when persistence remains possible.

## Receipts and removal

Receipts use version 1, independently of version-2 session records:

```text
executions/<session UUID>/<execution UUID>.json
  version, id, sessionId, startedAt, outcome
  optional endedAt and status
```

The directories are private and user-owned. Receipts use private regular files. Creation publishes a complete file without replacement. Finalization atomically replaces only the expected running receipt and preserves its identity and start time. Reads reject symlinks, unsafe ownership/permissions, unknown JSON fields, and inconsistent states.

The outcomes are running, completed, cancelled, timeout, and error. A known status zero is present in JSON. An unknown status is absent. Receipts never contain argv, stdin, stdout, or stderr.

Receipt removal is implemented. The manager first verifies container absence. Store deletion then verifies the exact session and name reservation. It validates every receipt before deleting any. Unexpected entries or unsafe metadata preserve the session record and reservation. Receipt cleanup precedes record and alias deletion. Workspace source files remain outside this cleanup.

## Source map

- [CLI parsing](../internal/cli/parse.go) and [dispatch](../internal/cli/run.go).
- [Request and result types](../internal/runtime/exec.go).
- [Podman exec and immediate cancellation](../internal/podman/exec.go).
- [Process live output](../internal/process/output.go).
- [Manager entry and transaction](../internal/session/exec.go).
- [Receipt types and final transition](../internal/session/execution.go).
- [Receipt persistence and cleanup](../internal/session/store_execution.go).

Active tests cover successful and nonzero exits, name/UUID mismatch, invalid requests, durable receipts before execution, cancellation, timeout, transport failure, invalid adapter results, unverified cleanup, unsafe receipt storage, and final receipt persistence failure. Ordinary tests do not require Podman. No live exec test has run yet.
