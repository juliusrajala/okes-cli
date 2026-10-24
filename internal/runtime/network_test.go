package runtime

import "testing"

func TestNetworkModeValidate(t *testing.T) {
	tests := []struct {
		name    string
		mode    NetworkMode
		wantErr bool
	}{
		{name: "network disabled", mode: NetworkNone},
		{name: "rootless networking requested", mode: NetworkPasta},
		{name: "zero value", wantErr: true},
		{name: "host network forbidden", mode: NetworkMode("host"), wantErr: true},
		{name: "bridge unsupported", mode: NetworkMode("bridge"), wantErr: true},
		{name: "unknown", mode: NetworkMode("automatic"), wantErr: true},
		{name: "uppercase", mode: NetworkMode("NONE"), wantErr: true},
		{name: "leading space", mode: NetworkMode(" none"), wantErr: true},
		{name: "trailing space", mode: NetworkMode("pasta "), wantErr: true},
		{name: "newline", mode: NetworkMode("none\n"), wantErr: true},
		{name: "NUL byte", mode: NetworkMode("pasta\x00"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.mode.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("NetworkMode(%q).Validate() error = %v, want error = %t", tt.mode, err, tt.wantErr)
			}
		})
	}
}
