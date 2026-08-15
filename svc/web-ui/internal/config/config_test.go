package config

import "testing"

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		port    string
		want    string
		wantErr bool
	}{
		{name: "defaults", want: "0.0.0.0:8080"},
		{name: "configured", host: "127.0.0.1", port: "9090", want: "127.0.0.1:9090"},
		{name: "ephemeral port", host: "127.0.0.1", port: "0", want: "127.0.0.1:0"},
		{name: "invalid port", port: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("WEB_UI_BIND_HOST", tt.host)
			t.Setenv("WEB_UI_BIND_PORT", tt.port)
			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatal("Load() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := cfg.BindAddress(); got != tt.want {
				t.Fatalf("BindAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}
