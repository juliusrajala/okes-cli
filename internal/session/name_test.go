package session

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "a"},
		{name: "1-session"},
		{name: "Work_2.test"},
		{name: strings.Repeat("a", 64)},
		{name: "", wantErr: true},
		{name: strings.Repeat("a", 65), wantErr: true},
		{name: "-session", wantErr: true},
		{name: "_session", wantErr: true},
		{name: ".session", wantErr: true},
		{name: "../session", wantErr: true},
		{name: "two words", wantErr: true},
		{name: "café", wantErr: true},
		{name: "session\n", wantErr: true},
		{name: "session\x00", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateName(tt.name); (err != nil) != tt.wantErr {
				t.Errorf("ValidateName(%q) error = %v, want error = %t", tt.name, err, tt.wantErr)
			}
		})
	}
}
