package session

import (
	"fmt"
	"strings"
	"testing"
)

func TestIDValidate(t *testing.T) {
	const valid = "7ad20f51-6067-4dc4-a072-54c94d7735de"
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "lowercase UUID", value: valid},
		{name: "uppercase UUID", value: strings.ToUpper(valid)},
		{name: "empty", wantErr: true},
		{name: "human name is not an ID", value: "my-session", wantErr: true},
		{name: "nil UUID", value: "00000000-0000-0000-0000-000000000000", wantErr: true},
		{name: "short UUID", value: valid[:len(valid)-1], wantErr: true},
		{name: "long UUID", value: valid + "0", wantErr: true},
		{name: "missing separators", value: strings.ReplaceAll(valid, "-", ""), wantErr: true},
		{name: "invalid hex", value: "7ad20f5g-6067-4dc4-a072-54c94d7735de", wantErr: true},
		{name: "version zero", value: "7ad20f51-6067-0dc4-a072-54c94d7735de", wantErr: true},
		{name: "version nine", value: "7ad20f51-6067-9dc4-a072-54c94d7735de", wantErr: true},
		{name: "invalid variant", value: "7ad20f51-6067-4dc4-c072-54c94d7735de", wantErr: true},
		{name: "leading space", value: " " + valid, wantErr: true},
		{name: "trailing space", value: valid + " ", wantErr: true},
		{name: "trailing newline", value: valid + "\n", wantErr: true},
		{name: "NUL byte", value: valid + "\x00", wantErr: true},
		{name: "path traversal", value: "../" + valid, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ID(tt.value).Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("ID(%q).Validate() error = %v, want error = %t", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestIDAcceptedVersionsAndVariants(t *testing.T) {
	for version := 1; version <= 8; version++ {
		for _, variant := range []string{"8", "9", "a", "b"} {
			value := fmt.Sprintf("7ad20f51-6067-%ddc4-%s072-54c94d7735de", version, variant)
			t.Run(value, func(t *testing.T) {
				if err := ID(value).Validate(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestParseID(t *testing.T) {
	for _, value := range []string{
		"7ad20f51-6067-4dc4-a072-54c94d7735de",
		"7AD20F51-6067-4DC4-A072-54C94D7735DE",
	} {
		t.Run(value, func(t *testing.T) {
			id, err := ParseID(value)
			if err != nil {
				t.Fatal(err)
			}
			if string(id) != value {
				t.Errorf("ParseID changed identity: got %q, want %q", id, value)
			}
		})
	}
}

func TestParseIDRejectsInvalidInput(t *testing.T) {
	id, err := ParseID("not-a-uuid")
	if err == nil || id != "" {
		t.Fatalf("ParseID returned (%q, %v), want empty ID and an error", id, err)
	}
}
