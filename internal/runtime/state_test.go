package runtime

import "testing"

func TestStateValid(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  bool
	}{
		{name: "running", state: Running, want: true},
		{name: "exited", state: Exited, want: true},
		{name: "created", state: Created, want: true},
		{name: "stopped", state: Stopped, want: true},
		{name: "paused", state: Paused, want: true},
		{name: "missing", state: Missing, want: true},
		{name: "zero value"},
		{name: "unknown", state: State("removing")},
		{name: "engine alias needs adapter conversion", state: State("configured")},
		{name: "uppercase needs adapter conversion", state: State("RUNNING")},
		{name: "trailing whitespace", state: State("missing ")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.Valid(); got != tt.want {
				t.Errorf("State(%q).Valid() = %t, want %t", tt.state, got, tt.want)
			}
		})
	}
}

func TestStateStoppedOrMissing(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  bool
	}{
		{name: "running", state: Running},
		{name: "exited", state: Exited, want: true},
		{name: "created", state: Created},
		{name: "stopped", state: Stopped, want: true},
		{name: "paused", state: Paused},
		{name: "missing", state: Missing, want: true},
		{name: "zero value"},
		{name: "unknown", state: State("removing")},
		{name: "unsupported engine state", state: State("configured")},
		{name: "uppercase", state: State("EXITED")},
		{name: "trailing whitespace", state: State("missing ")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.state.StoppedOrMissing(); got != tt.want {
				t.Errorf("State(%q).StoppedOrMissing() = %t, want %t", tt.state, got, tt.want)
			}
		})
	}
}
