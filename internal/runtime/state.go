// Package runtime defines container-runtime types without Podman or CLI details.
package runtime

// State describes a normalized container status observed from the runtime.
// The zero value is unknown, not proof that a container is missing or stopped.
type State string

const (
	// Running means the container's workload is running.
	Running State = "running"
	// Exited means the container's workload has exited.
	Exited State = "exited"
	// Created means the container exists but has not started.
	Created State = "created"
	// Stopped means the runtime reports the container as stopped.
	Stopped State = "stopped"
	// Paused means the workload is suspended, not stopped.
	Paused State = "paused"
	// Missing means the runtime has verified that the container is absent.
	Missing State = "missing"
)

// Valid reports whether s is a supported normalized state. Runtime adapters must
// reject unsupported states rather than converting them to Missing.
func (s State) Valid() bool {
	switch s {
	case Running, Exited, Created, Stopped, Paused, Missing:
		return true
	default:
		return false
	}
}

// StoppedOrMissing reports whether s is Exited, Stopped, or Missing.
// It does not establish ownership or permit deleting retained session data;
// full removal requires verified container absence and ownership checks.
func (s State) StoppedOrMissing() bool {
	return s == Exited || s == Stopped || s == Missing
}
