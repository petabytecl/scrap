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

func TestLocalNonProductionStorageRequiresExplicitEnableAndDataDir(t *testing.T) {
	tests := map[string]Config{
		"enabled without data dir": {
			PublicListenAddress:             DefaultPublicListenAddress,
			AdminListenAddress:              DefaultAdminListenAddress,
			EnableLocalNonProductionStorage: true,
		},
		"data dir without enable": {
			PublicListenAddress: DefaultPublicListenAddress,
			AdminListenAddress:  DefaultAdminListenAddress,
			LocalDataDir:        "/tmp/scrap",
		},
	}

	for name, cfg := range tests {
		t.Run(name, func(t *testing.T) {
			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}

	cfg := Default()
	cfg.EnableLocalNonProductionStorage = true
	cfg.LocalDataDir = t.TempDir()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("local non-production config did not validate: %v", err)
	}
}
