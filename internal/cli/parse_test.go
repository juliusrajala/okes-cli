package cli

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestParseStartRestrictions(t *testing.T) {
	base := []string{"start", "--detach", "--name", "demo", "--image", "localhost/worker:1"}
	for _, tt := range []struct {
		name  string
		flags []string
	}{
		{name: "copy is unavailable", flags: []string{"--workspace", "copy"}},
		{name: "clone is obsolete", flags: []string{"--workspace", "clone"}},
		{name: "host network", flags: []string{"--network", "host"}},
		{name: "invalid name", flags: []string{"--name", "bad/name"}},
		{name: "malformed environment", flags: []string{"--env", "no-equals-sign"}},
		{name: "duplicate environment", flags: []string{"--env", "MODE=a", "--env", "MODE=b"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := append(slices.Clone(base), tt.flags...)
			args = append(args, ".", "--", "worker")
			_, err := parse(args)
			var invalid *usageError
			if !errors.As(err, &invalid) {
				t.Fatalf("parse error = %v, want usage error", err)
			}
		})
	}
}

func TestDefaultStateDir(t *testing.T) {
	for _, tt := range []struct {
		name        string
		environment []string
		want        string
		wantErr     bool
	}{
		{name: "XDG", environment: []string{"HOME=/home/test", "XDG_DATA_HOME=/private/data"}, want: "/private/data/go-sesh"},
		{name: "HOME fallback", environment: []string{"HOME=/home/test"}, want: "/home/test/.local/share/go-sesh"},
		{name: "empty XDG falls back", environment: []string{"HOME=/home/test", "XDG_DATA_HOME="}, want: "/home/test/.local/share/go-sesh"},
		{name: "last entry wins", environment: []string{"HOME=/wrong", "HOME=/home/test"}, want: "/home/test/.local/share/go-sesh"},
		{name: "relative XDG is refused", environment: []string{"HOME=/home/test", "XDG_DATA_HOME=relative"}, wantErr: true},
		{name: "relative HOME is refused", environment: []string{"HOME=relative"}, wantErr: true},
		{name: "missing environment", wantErr: true},
		{name: "NUL path", environment: []string{"XDG_DATA_HOME=/private\x00/data"}, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := defaultStateDir(tt.environment)
			if tt.wantErr {
				if err == nil || got != "" {
					t.Fatalf("invalid state selection = %q, %v", got, err)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("state = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestParseEnvironment(t *testing.T) {
	for _, tt := range []struct {
		name      string
		entries   []string
		want      map[string]string
		wantErr   bool
		forbidden []string
	}{
		{name: "nil entries", want: map[string]string{}},
		{name: "empty entries", entries: []string{}, want: map[string]string{}},
		{name: "multiple entries", entries: []string{"MODE=test", "PROJECT=value"}, want: map[string]string{"MODE": "test", "PROJECT": "value"}},
		{name: "empty value", entries: []string{"NAME="}, want: map[string]string{"NAME": ""}},
		{name: "embedded equals", entries: []string{"VALUE=a=b=c"}, want: map[string]string{"VALUE": "a=b=c"}},
		{name: "literal shell text", entries: []string{"VALUE=$HOME; $(whoami) `id` & | > < ' \""}, want: map[string]string{"VALUE": "$HOME; $(whoami) `id` & | > < ' \""}},
		{name: "missing separator", entries: []string{"missing-separator-secret"}, wantErr: true, forbidden: []string{"missing-separator-secret"}},
		{name: "empty name", entries: []string{"=empty-name-secret"}, wantErr: true, forbidden: []string{"empty-name-secret"}},
		{name: "failure after valid entry", entries: []string{"MODE=earlier-secret", "=later-secret"}, wantErr: true, forbidden: []string{"earlier-secret", "later-secret"}},
		{name: "duplicate name", entries: []string{"MODE=first-secret", "MODE=second-secret"}, wantErr: true, forbidden: []string{"first-secret", "second-secret"}},
		{name: "duplicate empty value", entries: []string{"MODE=", "MODE=duplicate-secret"}, wantErr: true, forbidden: []string{"duplicate-secret"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseEnvironment(tt.entries)
			if tt.wantErr {
				if err == nil || got != nil {
					t.Fatalf("invalid environment = %v, %v; want nil map and error", got, err)
				}
				for _, value := range tt.forbidden {
					if strings.Contains(err.Error(), value) {
						t.Fatal("error exposes an environment value")
					}
				}
				return
			}
			if err != nil || got == nil || !maps.Equal(got, tt.want) {
				t.Fatalf("environment = %#v, %v; want %#v", got, err, tt.want)
			}
		})
	}
}
