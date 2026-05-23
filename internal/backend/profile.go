package backend

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

type Provider string

const (
	ProviderS3Like     Provider = "s3_like"
	ProviderGCS        Provider = "gcs"
	ProviderAzureBlob  Provider = "azure_blob"
	ProviderFilesystem Provider = "filesystem"
)

type ProfileMode string

const (
	ProfileModeProduction    ProfileMode = "production"
	ProfileModeNonProduction ProfileMode = "non_production"
)

type Lane string

const (
	LaneIngestUpload Lane = "ingest_upload"
	LaneRepair       Lane = "repair"
	LaneRestore      Lane = "restore"
	LaneDRCopy       Lane = "dr_copy"
	LaneRewrap       Lane = "rewrap"
	LaneScrub        Lane = "scrub"
)

type Adapter interface {
	Store
	Provider() Provider
	CapacityProfile() CapacityProfile
}

type MutableAdapter interface {
	Adapter
	MutableStore
}

type CapacityProfile struct {
	ID              string
	Provider        Provider
	Mode            ProfileMode
	ByteBudget      ByteBudget
	RequestBudget   RequestBudget
	LaneBudgets     []LaneBudget
	RampPolicy      RampPolicy
	CircuitBreakers CircuitBreakerPolicy
	DiskGuardBands  DiskGuardBands
	Runway          RunwayThresholds
}

type ByteBudget struct {
	SustainedIngressBytesPerSecond uint64
	BurstIngressBytesPerSecond     uint64
	BackendWriteBytesPerSecond     uint64
	BackendReadBytesPerSecond      uint64
	LocalUsableBytes               uint64
	MaxObjectBytes                 uint64
}

type RequestBudget struct {
	PutRequestsPerSecond    uint64
	GetRequestsPerSecond    uint64
	HeadRequestsPerSecond   uint64
	DeleteRequestsPerSecond uint64
}

type LaneBudget struct {
	Lane              Lane
	ReservedWorkers   uint32
	MaxWorkers        uint32
	BytesPerSecond    uint64
	RequestsPerSecond uint64
}

type RampPolicy struct {
	InitialRequestsPerSecond uint64
	MaxRequestsPerSecond     uint64
	StepRequestsPerSecond    uint64
	StepInterval             time.Duration
}

type CircuitBreakerPolicy struct {
	ConsecutiveFailures uint32
	ErrorRatePermille   uint32
	MinimumSamples      uint32
	OpenInterval        time.Duration
	HalfOpenRequests    uint32
}

type DiskGuardBands struct {
	AdmissionMinFreeBytes  uint64
	WarningMinFreeBytes    uint64
	CriticalMinFreeBytes   uint64
	FailClosedMinFreeBytes uint64
}

type RunwayThresholds struct {
	MinimumSafeRunway    time.Duration
	EstimatedBytesPerDay uint64
}

func NonProductionFilesystemProfile(id string, localUsableBytes uint64) CapacityProfile {
	return CapacityProfile{
		ID:       id,
		Provider: ProviderFilesystem,
		Mode:     ProfileModeNonProduction,
		ByteBudget: ByteBudget{
			SustainedIngressBytesPerSecond: 16 * 1024 * 1024,
			BurstIngressBytesPerSecond:     32 * 1024 * 1024,
			BackendWriteBytesPerSecond:     16 * 1024 * 1024,
			BackendReadBytesPerSecond:      16 * 1024 * 1024,
			LocalUsableBytes:               localUsableBytes,
			MaxObjectBytes:                 64 * 1024 * 1024,
		},
		RequestBudget: RequestBudget{
			PutRequestsPerSecond:    128,
			GetRequestsPerSecond:    128,
			HeadRequestsPerSecond:   128,
			DeleteRequestsPerSecond: 32,
		},
		LaneBudgets: []LaneBudget{
			{Lane: LaneIngestUpload, ReservedWorkers: 1, MaxWorkers: 2, BytesPerSecond: 8 * 1024 * 1024, RequestsPerSecond: 64},
			{Lane: LaneRepair, ReservedWorkers: 1, MaxWorkers: 2, BytesPerSecond: 4 * 1024 * 1024, RequestsPerSecond: 32},
			{Lane: LaneRestore, ReservedWorkers: 1, MaxWorkers: 2, BytesPerSecond: 4 * 1024 * 1024, RequestsPerSecond: 32},
			{Lane: LaneDRCopy, ReservedWorkers: 1, MaxWorkers: 1, BytesPerSecond: 2 * 1024 * 1024, RequestsPerSecond: 16},
			{Lane: LaneRewrap, ReservedWorkers: 1, MaxWorkers: 1, BytesPerSecond: 1024 * 1024, RequestsPerSecond: 16},
			{Lane: LaneScrub, ReservedWorkers: 1, MaxWorkers: 1, BytesPerSecond: 1024 * 1024, RequestsPerSecond: 16},
		},
		RampPolicy: RampPolicy{
			InitialRequestsPerSecond: 16,
			MaxRequestsPerSecond:     128,
			StepRequestsPerSecond:    16,
			StepInterval:             time.Second,
		},
		CircuitBreakers: CircuitBreakerPolicy{
			ConsecutiveFailures: 8,
			ErrorRatePermille:   500,
			MinimumSamples:      16,
			OpenInterval:        30 * time.Second,
			HalfOpenRequests:    4,
		},
		DiskGuardBands: DiskGuardBands{
			AdmissionMinFreeBytes:  256 * 1024 * 1024,
			WarningMinFreeBytes:    128 * 1024 * 1024,
			CriticalMinFreeBytes:   64 * 1024 * 1024,
			FailClosedMinFreeBytes: 32 * 1024 * 1024,
		},
		Runway: RunwayThresholds{
			MinimumSafeRunway:    24 * time.Hour,
			EstimatedBytesPerDay: 128 * 1024 * 1024,
		},
	}
}

func (p CapacityProfile) Validate() error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("backend capacity profile id is required")
	}
	if !p.Provider.Valid() {
		return fmt.Errorf("backend capacity profile %s has unsupported provider %q", p.ID, p.Provider)
	}
	switch p.Mode {
	case ProfileModeProduction:
	case ProfileModeNonProduction:
		if p.Provider != ProviderFilesystem {
			return fmt.Errorf("backend capacity profile %s: non-production mode is only valid for explicit filesystem backends", p.ID)
		}
	default:
		return fmt.Errorf("backend capacity profile %s has unsupported mode %q", p.ID, p.Mode)
	}
	if err := p.validateBudgets(); err != nil {
		return err
	}
	return nil
}

func (p CapacityProfile) Clone() CapacityProfile {
	p.LaneBudgets = append([]LaneBudget(nil), p.LaneBudgets...)
	return p
}

func (p Provider) Valid() bool {
	switch p {
	case ProviderS3Like, ProviderGCS, ProviderAzureBlob, ProviderFilesystem:
		return true
	default:
		return false
	}
}

func (p CapacityProfile) validateBudgets() error {
	if err := validateByteBudget(p.ID, p.ByteBudget); err != nil {
		return err
	}
	if err := validateRequestBudget(p.ID, p.RequestBudget); err != nil {
		return err
	}
	if err := validateLaneBudgets(p.ID, p.LaneBudgets); err != nil {
		return err
	}
	if err := validateRampPolicy(p.ID, p.RampPolicy); err != nil {
		return err
	}
	if err := validateCircuitBreakers(p.ID, p.CircuitBreakers); err != nil {
		return err
	}
	if err := validateDiskGuardBands(p.ID, p.ByteBudget, p.DiskGuardBands); err != nil {
		return err
	}
	if err := validateRunway(p.ID, p.ByteBudget, p.DiskGuardBands, p.Runway); err != nil {
		return err
	}
	return nil
}

func validateByteBudget(profileID string, budget ByteBudget) error {
	for _, field := range []struct {
		name  string
		value uint64
	}{
		{name: "sustained ingress bytes/sec", value: budget.SustainedIngressBytesPerSecond},
		{name: "burst ingress bytes/sec", value: budget.BurstIngressBytesPerSecond},
		{name: "backend write bytes/sec", value: budget.BackendWriteBytesPerSecond},
		{name: "backend read bytes/sec", value: budget.BackendReadBytesPerSecond},
		{name: "local usable bytes", value: budget.LocalUsableBytes},
		{name: "max object bytes", value: budget.MaxObjectBytes},
	} {
		if err := validateFinitePositive(profileID, field.name, field.value); err != nil {
			return err
		}
	}
	if budget.BurstIngressBytesPerSecond < budget.SustainedIngressBytesPerSecond {
		return fmt.Errorf("backend capacity profile %s: burst ingress must be at least sustained ingress", profileID)
	}
	if budget.BackendWriteBytesPerSecond < budget.SustainedIngressBytesPerSecond {
		return fmt.Errorf("backend capacity profile %s: backend write budget cannot drain sustained ingress", profileID)
	}
	return nil
}

func validateRequestBudget(profileID string, budget RequestBudget) error {
	for _, field := range []struct {
		name  string
		value uint64
	}{
		{name: "put requests/sec", value: budget.PutRequestsPerSecond},
		{name: "get requests/sec", value: budget.GetRequestsPerSecond},
		{name: "head requests/sec", value: budget.HeadRequestsPerSecond},
		{name: "delete requests/sec", value: budget.DeleteRequestsPerSecond},
	} {
		if err := validateFinitePositive(profileID, field.name, field.value); err != nil {
			return err
		}
	}
	return nil
}

func validateLaneBudgets(profileID string, lanes []LaneBudget) error {
	if len(lanes) == 0 {
		return fmt.Errorf("backend capacity profile %s: lane budgets are required", profileID)
	}
	required := map[Lane]bool{
		LaneIngestUpload: false,
		LaneRepair:       false,
		LaneRestore:      false,
		LaneDRCopy:       false,
		LaneRewrap:       false,
		LaneScrub:        false,
	}
	seen := make(map[Lane]bool, len(lanes))
	for _, lane := range lanes {
		if lane.Lane == "" {
			return fmt.Errorf("backend capacity profile %s: lane name is required", profileID)
		}
		if seen[lane.Lane] {
			return fmt.Errorf("backend capacity profile %s: duplicate lane budget %s", profileID, lane.Lane)
		}
		seen[lane.Lane] = true
		if _, ok := required[lane.Lane]; ok {
			required[lane.Lane] = true
		}
		if lane.ReservedWorkers == 0 {
			return fmt.Errorf("backend capacity profile %s: lane %s reserved workers must be positive", profileID, lane.Lane)
		}
		if lane.MaxWorkers < lane.ReservedWorkers {
			return fmt.Errorf("backend capacity profile %s: lane %s max workers must cover reserved workers", profileID, lane.Lane)
		}
		if err := validateFinitePositive(profileID, string(lane.Lane)+" bytes/sec", lane.BytesPerSecond); err != nil {
			return err
		}
		if err := validateFinitePositive(profileID, string(lane.Lane)+" requests/sec", lane.RequestsPerSecond); err != nil {
			return err
		}
	}
	for lane, present := range required {
		if !present {
			return fmt.Errorf("backend capacity profile %s: missing required lane budget %s", profileID, lane)
		}
	}
	return nil
}

func validateRampPolicy(profileID string, ramp RampPolicy) error {
	if err := validateFinitePositive(profileID, "ramp initial requests/sec", ramp.InitialRequestsPerSecond); err != nil {
		return err
	}
	if err := validateFinitePositive(profileID, "ramp max requests/sec", ramp.MaxRequestsPerSecond); err != nil {
		return err
	}
	if err := validateFinitePositive(profileID, "ramp step requests/sec", ramp.StepRequestsPerSecond); err != nil {
		return err
	}
	if ramp.MaxRequestsPerSecond < ramp.InitialRequestsPerSecond {
		return fmt.Errorf("backend capacity profile %s: ramp max must cover initial requests", profileID)
	}
	if ramp.StepInterval <= 0 {
		return fmt.Errorf("backend capacity profile %s: ramp step interval must be positive", profileID)
	}
	return nil
}

func validateCircuitBreakers(profileID string, breakers CircuitBreakerPolicy) error {
	if breakers.ConsecutiveFailures == 0 {
		return fmt.Errorf("backend capacity profile %s: circuit breaker consecutive failures must be positive", profileID)
	}
	if breakers.ErrorRatePermille == 0 || breakers.ErrorRatePermille > 1000 {
		return fmt.Errorf("backend capacity profile %s: circuit breaker error rate must be 1..1000 permille", profileID)
	}
	if breakers.MinimumSamples == 0 {
		return fmt.Errorf("backend capacity profile %s: circuit breaker minimum samples must be positive", profileID)
	}
	if breakers.OpenInterval <= 0 {
		return fmt.Errorf("backend capacity profile %s: circuit breaker open interval must be positive", profileID)
	}
	if breakers.HalfOpenRequests == 0 {
		return fmt.Errorf("backend capacity profile %s: circuit breaker half-open requests must be positive", profileID)
	}
	return nil
}

func validateDiskGuardBands(profileID string, budget ByteBudget, bands DiskGuardBands) error {
	for _, field := range []struct {
		name  string
		value uint64
	}{
		{name: "admission min free bytes", value: bands.AdmissionMinFreeBytes},
		{name: "warning min free bytes", value: bands.WarningMinFreeBytes},
		{name: "critical min free bytes", value: bands.CriticalMinFreeBytes},
		{name: "fail-closed min free bytes", value: bands.FailClosedMinFreeBytes},
	} {
		if err := validateFinitePositive(profileID, field.name, field.value); err != nil {
			return err
		}
	}
	if bands.AdmissionMinFreeBytes < bands.WarningMinFreeBytes ||
		bands.WarningMinFreeBytes < bands.CriticalMinFreeBytes ||
		bands.CriticalMinFreeBytes < bands.FailClosedMinFreeBytes {
		return fmt.Errorf("backend capacity profile %s: disk guard bands must be admission >= warning >= critical >= fail-closed", profileID)
	}
	if bands.AdmissionMinFreeBytes >= budget.LocalUsableBytes {
		return fmt.Errorf("backend capacity profile %s: admission guard band exhausts local usable bytes", profileID)
	}
	if budget.MaxObjectBytes > budget.LocalUsableBytes-bands.AdmissionMinFreeBytes {
		return fmt.Errorf("backend capacity profile %s: max object size exceeds post-admission local runway", profileID)
	}
	return nil
}

func validateRunway(profileID string, budget ByteBudget, bands DiskGuardBands, runway RunwayThresholds) error {
	if runway.MinimumSafeRunway <= 0 {
		return fmt.Errorf("backend capacity profile %s: minimum safe runway must be positive", profileID)
	}
	if err := validateFinitePositive(profileID, "estimated bytes per day", runway.EstimatedBytesPerDay); err != nil {
		return err
	}
	days := uint64(runway.MinimumSafeRunway / (24 * time.Hour))
	if runway.MinimumSafeRunway%(24*time.Hour) != 0 {
		days++
	}
	if days == 0 {
		days = 1
	}
	if runway.EstimatedBytesPerDay > math.MaxUint64/days {
		return fmt.Errorf("backend capacity profile %s: runway byte estimate overflows", profileID)
	}
	required := runway.EstimatedBytesPerDay * days
	available := budget.LocalUsableBytes - bands.AdmissionMinFreeBytes
	if available < required {
		return fmt.Errorf("backend capacity profile %s: local runway bytes %d below required %d", profileID, available, required)
	}
	return nil
}

func validateFinitePositive(profileID string, field string, value uint64) error {
	if value == 0 {
		return fmt.Errorf("backend capacity profile %s: %s must be positive", profileID, field)
	}
	if value == math.MaxUint64 {
		return fmt.Errorf("backend capacity profile %s: %s must be finite", profileID, field)
	}
	return nil
}
