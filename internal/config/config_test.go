package config

import "testing"

func TestDefaultConfigValidates(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config did not validate: %v", err)
	}
}

func TestConfigRejectsMissingAndDuplicateAddresses(t *testing.T) {
	tests := map[string]Config{
		"missing public": {
			PublicListenAddress: "",
			AdminListenAddress:  DefaultAdminListenAddress,
		},
		"missing admin": {
			PublicListenAddress: DefaultPublicListenAddress,
			AdminListenAddress:  "",
		},
		"duplicate": {
			PublicListenAddress: "127.0.0.1:1",
			AdminListenAddress:  "127.0.0.1:1",
		},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
