package cmd

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/shard"
)

const (
	minPort = 1
	maxPort = 65535
)

// Config is the fully assembled scrapd configuration: command-line flags plus
// environment variables, read and validated in one place by loadConfig.
//
// Defaults for subsystem tunable continue to live with their owning packages
// (e.g. shard.DefaultBlockSealSize, shard.DefaultUploadConcurrency, the scrub
// defaults). Config centralizes only the reading and validation, not the
// defaults.
type Config struct {
	// Command-line flags.
	DataDir       string
	ListenAddr    string
	PeerAddr      string
	AdminAddr     string
	BlockSealSize int64
	PeersFlag     string

	// Run-level environment.
	CellID             string
	ShardPlacementFile string
	UploadEnabled      bool
	UploadConcurrency  int
	RawTelemetryIDs    bool
	TestHooks          bool
	PprofEnabled       bool
	SecurityMode       security.Mode

	// Peer-resolution environment.
	Replicas        int
	HeadlessService string
	PeerPort        int
	Namespace       string

	// Subsystem configs assembled from their own env parsers (defaults owned by
	// their packages).
	Scrub          scrub.Config
	UploadPressure shard.UploadPressureConfig
	Eviction       shard.EvictionConfig

	// Production security gates. These are parsed from environment but enforced
	// in newApp before any serving surface or backend is opened.
	ProductionGates security.StartupGateConfig
}

// loadConfig assembles Config from the given command-line arguments and the
// process environment, validating typed and ranged values. On malformed or
// out-of-range input it returns an error naming the offending key rather than
// silently falling back to a default.
func loadConfig(args []string) (Config, error) {
	fs := flag.NewFlagSet("scrapd", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "/data", "storage root directory")
	listenAddr := fs.String("listen-addr", ":9090", "gRPC client listen address")
	peerAddr := fs.String("peer-addr", ":9091", "gRPC peer listen address")
	adminAddr := fs.String("admin-addr", ":8080", "HTTP admin listen address (health)")
	blockSealSize := fs.Int64("block-seal-size", shard.DefaultBlockSealSize, "block seal threshold in bytes")
	peersFlag := fs.String("peers", "", "raft peers (e.g. 1=localhost:9091,2=localhost:9092)")
	if err := fs.Parse(args); err != nil {
		return Config{}, fmt.Errorf("parse flags: %w", err)
	}

	cfg := Config{
		DataDir:            *dataDir,
		ListenAddr:         *listenAddr,
		PeerAddr:           *peerAddr,
		AdminAddr:          *adminAddr,
		BlockSealSize:      *blockSealSize,
		PeersFlag:          *peersFlag,
		CellID:             os.Getenv("SCRAP_CELL_ID"),
		ShardPlacementFile: os.Getenv("SCRAP_SHARD_PLACEMENT_FILE"),
		HeadlessService:    os.Getenv("SCRAP_HEADLESS_SERVICE"),
		Namespace:          envString("POD_NAMESPACE", "default"),
		Scrub:              scrub.ParseConfig(),
		UploadPressure:     shard.ParseUploadPressureConfigFromEnv(),
	}

	if err := loadCheckedEnv(&cfg); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func loadCheckedEnv(cfg *Config) error {
	if err := loadSubsystemEnv(cfg); err != nil {
		return err
	}
	if err := loadBoolEnv(cfg); err != nil {
		return err
	}
	if err := loadIntEnv(cfg); err != nil {
		return err
	}
	return loadSecurityEnv(cfg)
}

func loadSubsystemEnv(cfg *Config) error {
	eviction, err := shard.ParseEvictionConfigFromEnv()
	if err != nil {
		return err
	}
	cfg.Eviction = eviction
	return nil
}

func loadBoolEnv(cfg *Config) error {
	var err error
	if cfg.UploadEnabled, err = envBoolChecked("SCRAP_UPLOAD_ENABLED", true); err != nil {
		return err
	}
	if cfg.RawTelemetryIDs, err = envBoolChecked("SCRAP_TELEMETRY_RAW_IDS", false); err != nil {
		return err
	}
	if cfg.TestHooks, err = envBoolChecked("SCRAP_TEST_HOOKS", false); err != nil {
		return err
	}
	if cfg.PprofEnabled, err = envBoolChecked("SCRAP_PPROF_ENABLED", false); err != nil {
		return err
	}
	return nil
}

func loadIntEnv(cfg *Config) error {
	var err error
	if cfg.UploadConcurrency, err = envIntChecked("SCRAP_UPLOAD_CONCURRENCY", shard.DefaultUploadConcurrency); err != nil {
		return err
	}
	if cfg.Replicas, err = envIntChecked("SCRAP_REPLICAS", 0); err != nil {
		return err
	}
	if cfg.PeerPort, err = envIntChecked("SCRAP_PEER_PORT", defaultPeerPort); err != nil {
		return err
	}
	return nil
}

func loadSecurityEnv(cfg *Config) error {
	mode, err := security.ParseMode(os.Getenv("SCRAP_SECURITY_MODE"))
	if err != nil {
		return err
	}
	transitFake, err := envBoolChecked("SCRAP_TRANSIT_FAKE", false)
	if err != nil {
		return err
	}
	transitTokenEnv := os.Getenv("SCRAP_TRANSIT_TOKEN_ENV")

	cfg.SecurityMode = mode
	cfg.ProductionGates = security.StartupGateConfig{
		Mode: mode,
		TLS: security.TLSConfig{
			Public:   surfaceTLSFiles("SCRAP_TLS_PUBLIC"),
			Peer:     surfaceTLSFiles("SCRAP_TLS_PEER"),
			Admin:    surfaceTLSFiles("SCRAP_TLS_ADMIN"),
			Scrapctl: surfaceTLSFiles("SCRAP_TLS_SCRAPCTL"),
		},
		RolePolicyPath:         os.Getenv("SCRAP_ROLE_POLICY_FILE"),
		PeerIdentityPolicyPath: os.Getenv("SCRAP_PEER_IDENTITY_POLICY_FILE"),
		Transit: security.TransitConfig{
			Address:      os.Getenv("SCRAP_TRANSIT_ADDR"),
			MountPath:    os.Getenv("SCRAP_TRANSIT_MOUNT"),
			KeyName:      os.Getenv("SCRAP_TRANSIT_KEY"),
			TokenEnv:     transitTokenEnv,
			TokenPresent: transitTokenPresent(transitTokenEnv),
			Fake:         transitFake,
		},
		AuditSink:  security.AuditSinkConfig{PolicyPath: os.Getenv("SCRAP_AUDIT_POLICY_FILE")},
		RateLimits: security.RateLimitConfig{PolicyPath: os.Getenv("SCRAP_RATE_LIMIT_POLICY_FILE")},
		TestHooks:  cfg.TestHooks,
		Pprof:      security.PprofConfig{Enabled: cfg.PprofEnabled},
	}
	return nil
}

func surfaceTLSFiles(prefix string) security.TLSFiles {
	return security.TLSFiles{
		ServerCertPath: os.Getenv(prefix + "_CERT"),
		ServerKeyPath:  os.Getenv(prefix + "_KEY"),
		ClientCAPath:   os.Getenv(prefix + "_CLIENT_CA"),
		ServerName:     os.Getenv(prefix + "_SERVER_NAME"),
	}
}

func transitTokenPresent(tokenEnv string) bool {
	tokenEnv = strings.TrimSpace(tokenEnv)
	if tokenEnv == "" {
		return false
	}
	token, ok := os.LookupEnv(tokenEnv)
	return ok && strings.TrimSpace(token) != ""
}

func (c Config) startupGateConfig(peerIdentity security.PeerIdentityConfig) security.StartupGateConfig {
	gates := c.ProductionGates
	gates.Mode = c.SecurityMode
	gates.PeerIdentity = peerIdentity
	gates.TestHooks = c.TestHooks
	gates.Pprof.Enabled = c.PprofEnabled
	return gates
}

// validate enforces ranges on typed values. It only rejects values that are
// nonsensical regardless of subsystem clamping (e.g. a non-positive seal size or
// an out-of-range port), so previously valid configurations keep loading.
func (c Config) validate() error {
	if c.BlockSealSize <= 0 {
		return fmt.Errorf("invalid -block-seal-size: %d (must be > 0)", c.BlockSealSize)
	}
	if c.Replicas < 0 {
		return fmt.Errorf("invalid SCRAP_REPLICAS: %d (must be >= 0)", c.Replicas)
	}
	if c.PeerPort < minPort || c.PeerPort > maxPort {
		return fmt.Errorf("invalid SCRAP_PEER_PORT: %d (must be %d-%d)", c.PeerPort, minPort, maxPort)
	}
	return nil
}

// envIntChecked reads an integer environment variable. An unset/empty value
// yields the fallback; a present but noninteger value is an error (no silent
// fallback to the default).
func envIntChecked(key string, fallback int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q is not an integer", key, v)
	}
	return n, nil
}

// envBoolChecked reads a boolean environment variable. An unset/empty value
// yields the fallback; a present but unparseable value is an error (no silent
// fallback to the default).
func envBoolChecked(key string, fallback bool) (bool, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %q is not a boolean", key, v)
	}
	return b, nil
}
