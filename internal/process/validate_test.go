package process

import "testing"

func TestValidateCommand(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		args    []string
		wantErr bool
	}{
		{
			name: "absolute executable", path: "/usr/bin/git",
			args: []string{"status"},
		},
		{
			name: "no arguments", path: "/usr/bin/git",
		},
		{
			name: "empty arguments", path: "/usr/bin/printf",
			args: []string{"", ""},
		},
		{
			name: "literal shell characters", path: "/usr/bin/printf",
			args: []string{"hello world", "$(whoami)", "; rm -rf /", "*", "'quoted'"},
		},
		{
			name: "spaces in executable path", path: "/some directory/program",
		},
		{
			name: "no existence check", path: "/nonexistent-pi-sesh-test/program",
		},
		{
			name: "unicode arguments", path: "/usr/bin/printf",
			args: []string{"café", "日本語"},
		},
		{
			name: "empty executable", wantErr: true,
		},
		{
			name: "bare executable name", path: "git", wantErr: true,
		},
		{
			name: "relative executable path", path: "./bin/git", wantErr: true,
		},
		{
			name: "home expansion is not absolute", path: "~/bin/git", wantErr: true,
		},
		{
			name: "NUL in executable path", path: "/usr/bin/gi\x00t", wantErr: true,
		},
		{
			name: "NUL in first argument", path: "/usr/bin/git",
			args: []string{"sta\x00tus"}, wantErr: true,
		},
		{
			name: "NUL in later argument", path: "/usr/bin/git",
			args: []string{"status", "bad\x00"}, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateCommand(tt.path, tt.args)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCommand(%q, %q) error = %v, want error = %t", tt.path, tt.args, err, tt.wantErr)
			}
		})
	}
}
