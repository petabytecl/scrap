package encryption_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
)

func TestTransitErrorClassSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want encryption.Class
	}{
		{name: "unavailable", err: encryption.ErrUnavailable, want: encryption.ClassUnavailable},
		{name: "auth denied", err: encryption.ErrAuthDenied, want: encryption.ClassAuthDenied},
		{name: "missing key", err: encryption.ErrMissingKey, want: encryption.ClassMissingKey},
		{name: "minimum version", err: encryption.ErrMinimumVersion, want: encryption.ClassMinimumVersion},
		{name: "invalid config", err: encryption.ErrInvalidConfig, want: encryption.ClassInvalidConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := errors.Join(errors.New("provider detail"), tt.err)
			if !errors.Is(err, tt.err) {
				t.Fatalf("joined error is not %v", tt.err)
			}
			if got := encryption.ErrorClass(err); got != tt.want {
				t.Fatalf("ErrorClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTransitRejectsUnsupportedDataKeySizes(t *testing.T) {
	transit := encryption.NewFakeTransit(encryption.FakeConfig{})
	_, err := transit.GenerateDataKey(context.Background(), encryption.GenerateDataKeyRequest{Bits: 192})
	if !errors.Is(err, encryption.ErrInvalidRequest) {
		t.Fatalf("GenerateDataKey error = %v, want invalid request", err)
	}
	if got := encryption.ErrorClass(err); got != encryption.ClassInvalidRequest {
		t.Fatalf("ErrorClass() = %q, want %q", got, encryption.ClassInvalidRequest)
	}
}

func TestOpenBaoConfigValidationRedactsToken(t *testing.T) {
	tests := []struct {
		name string
		cfg  encryption.OpenBaoConfig
	}{
		{
			name: "bad address",
			cfg: encryption.OpenBaoConfig{
				Address:   "://bad-url",
				MountPath: "transit",
				KeyName:   "scrap-documents",
				Token:     "super-secret-token",
			},
		},
		{
			name: "bad key path",
			cfg: encryption.OpenBaoConfig{
				Address:   "https://openbao.example.invalid",
				MountPath: "transit",
				KeyName:   "documents//active",
				Token:     "super-secret-token",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encryption.NewOpenBaoTransit(tt.cfg)
			if err == nil {
				t.Fatal("NewOpenBaoTransit succeeded, want config error")
			}
			if !errors.Is(err, encryption.ErrInvalidConfig) {
				t.Fatalf("error = %v, want invalid config", err)
			}
			if msg := err.Error(); containsAny(msg, tt.cfg.Token, "bad-url", "documents//active") {
				t.Fatalf("config error leaked sensitive value: %q", msg)
			}
		})
	}
}

func containsAny(s string, values ...string) bool {
	for _, value := range values {
		if value != "" && strings.Contains(s, value) {
			return true
		}
	}
	return false
}
