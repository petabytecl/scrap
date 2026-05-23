package backend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestClassifyErrorNormalizesBackendErrors(t *testing.T) {
	tests := map[string]struct {
		err   error
		class ErrorClass
	}{
		"throttled": {
			err:   fmt.Errorf("provider said slow down: %w", ErrThrottled),
			class: ErrorClassThrottled,
		},
		"transient": {
			err:   context.DeadlineExceeded,
			class: ErrorClassTransient,
		},
		"auth": {
			err:   NewError(ErrorClassAuth, "put", "object", errors.New("denied")),
			class: ErrorClassAuth,
		},
		"not found": {
			err:   ErrNotFound,
			class: ErrorClassNotFound,
		},
		"conflict": {
			err:   ErrConflict,
			class: ErrorClassConflict,
		},
		"corrupt": {
			err:   ErrChecksumMismatch,
			class: ErrorClassCorrupt,
		},
		"permanent": {
			err:   ErrInvalidRange,
			class: ErrorClassPermanent,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := ClassifyError(test.err); got != test.class {
				t.Fatalf("class = %s, want %s", got, test.class)
			}
			if !IsErrorClass(test.err, test.class) {
				t.Fatalf("IsErrorClass(%v, %s) = false", test.err, test.class)
			}
		})
	}
	if err := NewError(ErrorClassThrottled, "put", "object", nil); !errors.Is(err, ErrThrottled) {
		t.Fatalf("errors.Is(%v, ErrThrottled) = false", err)
	}
}

func TestProductionCapacityProfileValidation(t *testing.T) {
	valid := testProductionCapacityProfile()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid production profile rejected: %v", err)
	}

	tests := map[string]struct {
		mutate func(*CapacityProfile)
		want   string
	}{
		"missing id": {
			mutate: func(p *CapacityProfile) { p.ID = "" },
			want:   "id is required",
		},
		"unsupported provider": {
			mutate: func(p *CapacityProfile) { p.Provider = Provider("swift") },
			want:   "unsupported provider",
		},
		"missing byte budget": {
			mutate: func(p *CapacityProfile) { p.ByteBudget.BackendWriteBytesPerSecond = 0 },
			want:   "backend write bytes/sec must be positive",
		},
		"unsafe drain budget": {
			mutate: func(p *CapacityProfile) {
				p.ByteBudget.BackendWriteBytesPerSecond = p.ByteBudget.SustainedIngressBytesPerSecond - 1
			},
			want: "cannot drain sustained ingress",
		},
		"missing lane": {
			mutate: func(p *CapacityProfile) { p.LaneBudgets = p.LaneBudgets[:len(p.LaneBudgets)-1] },
			want:   "missing required lane budget",
		},
		"invalid ramp": {
			mutate: func(p *CapacityProfile) {
				p.RampPolicy.MaxRequestsPerSecond = p.RampPolicy.InitialRequestsPerSecond - 1
			},
			want: "ramp max must cover initial requests",
		},
		"invalid circuit breaker": {
			mutate: func(p *CapacityProfile) { p.CircuitBreakers.ErrorRatePermille = 1001 },
			want:   "error rate must be 1..1000",
		},
		"unordered guard bands": {
			mutate: func(p *CapacityProfile) {
				p.DiskGuardBands.WarningMinFreeBytes = p.DiskGuardBands.AdmissionMinFreeBytes + 1
			},
			want: "guard bands must be admission >= warning",
		},
		"unsafe max object": {
			mutate: func(p *CapacityProfile) { p.ByteBudget.MaxObjectBytes = p.ByteBudget.LocalUsableBytes },
			want:   "max object size exceeds post-admission local runway",
		},
		"unsafe runway": {
			mutate: func(p *CapacityProfile) { p.Runway.MinimumSafeRunway = 30 * 24 * time.Hour },
			want:   "local runway bytes",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			profile := valid.Clone()
			test.mutate(&profile)
			err := profile.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestNonProductionFilesystemProfileIsExplicitAndFinite(t *testing.T) {
	profile := NonProductionFilesystemProfile("local-dev", 4*1024*1024*1024)
	if err := profile.Validate(); err != nil {
		t.Fatalf("non-production filesystem profile rejected: %v", err)
	}
	if profile.Mode != ProfileModeNonProduction || profile.Provider != ProviderFilesystem {
		t.Fatalf("profile mode/provider = %s/%s, want explicit non-production filesystem", profile.Mode, profile.Provider)
	}

	profile = NonProductionFilesystemProfile("local-dev", 0)
	if err := profile.Validate(); err == nil || !strings.Contains(err.Error(), "local usable bytes must be positive") {
		t.Fatalf("validation error = %v, want finite local capacity rejection", err)
	}

	profile = NonProductionFilesystemProfile("local-dev", 4*1024*1024*1024)
	profile.Provider = ProviderS3Like
	if err := profile.Validate(); err == nil || !strings.Contains(err.Error(), "non-production mode is only valid") {
		t.Fatalf("validation error = %v, want explicit filesystem rejection", err)
	}
}

func testProductionCapacityProfile() CapacityProfile {
	profile := NonProductionFilesystemProfile("prod-s3", 8*1024*1024*1024)
	profile.Provider = ProviderS3Like
	profile.Mode = ProfileModeProduction
	profile.ByteBudget.SustainedIngressBytesPerSecond = 32 * 1024 * 1024
	profile.ByteBudget.BurstIngressBytesPerSecond = 64 * 1024 * 1024
	profile.ByteBudget.BackendWriteBytesPerSecond = 64 * 1024 * 1024
	profile.ByteBudget.BackendReadBytesPerSecond = 64 * 1024 * 1024
	profile.ByteBudget.MaxObjectBytes = 128 * 1024 * 1024
	profile.Runway.EstimatedBytesPerDay = 512 * 1024 * 1024
	return profile
}
