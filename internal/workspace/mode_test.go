package workspace

import (
	"strings"
	"testing"
)

func TestModeValidate(t *testing.T) {
	tests := []struct {
		name    string
		mode    Mode
		wantErr bool
	}{
		{name: "mount", mode: Mount},
		{name: "copy", mode: Copy},
		{name: "zero value", wantErr: true},
		{name: "removed Git clone", mode: Mode("clone"), wantErr: true},
		{name: "unknown", mode: Mode("snapshot"), wantErr: true},
		{name: "uppercase", mode: Mode("COPY"), wantErr: true},
		{name: "leading space", mode: Mode(" mount"), wantErr: true},
		{name: "trailing space", mode: Mode("copy "), wantErr: true},
		{name: "newline", mode: Mode("copy\n"), wantErr: true},
		{name: "NUL byte", mode: Mode("mount\x00"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mode.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Mode(%q).Validate() error = %v, want error = %t", tt.mode, err, tt.wantErr)
			}
		})
	}
}

func TestModeValidateExplainsCloneRemoval(t *testing.T) {
	err := Mode("clone").Validate()
	if err == nil {
		t.Fatal("clone must not be accepted as copy")
	}
	if !strings.Contains(err.Error(), "no longer supported") || !strings.Contains(err.Error(), "Git") {
		t.Errorf("error = %q, want an explanation of Git clone removal", err)
	}
}
