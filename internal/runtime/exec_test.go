package runtime

import (
	"slices"
	"testing"
)

func TestExecRequestValidate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		request ExecRequest
		valid   bool
	}{
		{name: "defaults", request: ExecRequest{Command: []string{"true"}}, valid: true},
		{name: "literal empty trailing argument", request: ExecRequest{Command: []string{"printf", "$HOME; literal", ""}}, valid: true},
		{name: "maximum arguments", request: ExecRequest{Command: append([]string{"true"}, make([]string, 255)...)}, valid: true},
		{name: "absolute container path", request: ExecRequest{Command: []string{"true"}, Workdir: "/tmp/path with spaces"}, valid: true},
		{name: "missing program"},
		{name: "empty program", request: ExecRequest{Command: []string{"", "arg"}}},
		{name: "too many arguments", request: ExecRequest{Command: append([]string{"true"}, make([]string, 256)...)}},
		{name: "NUL", request: ExecRequest{Command: []string{"true", "bad\x00"}}},
		{name: "relative path", request: ExecRequest{Command: []string{"true"}, Workdir: "relative"}},
		{name: "NUL path", request: ExecRequest{Command: []string{"true"}, Workdir: "/tmp\x00"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := slices.Clone(tt.request.Command)
			if err := tt.request.Validate(); (err == nil) != tt.valid {
				t.Fatalf("Validate = %v, valid=%v", err, tt.valid)
			}
			if !slices.Equal(before, tt.request.Command) {
				t.Fatal("validation changed argv")
			}
		})
	}
}
