package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/routing"
	"github.com/petabytecl/scrap/internal/scrub"
	"github.com/petabytecl/scrap/internal/security"
	"github.com/petabytecl/scrap/internal/shard"
	securityfixture "github.com/petabytecl/scrap/test/fixtures/security"
)

func TestValidateStartupRoutingGatesRequiresPlacementFileInProduction(t *testing.T) {
	cfg := Config{SecurityMode: security.ModeProduction}

	_, err := validateStartupRoutingGates(cfg)
	if err == nil {
		t.Fatal("validateStartupRoutingGates succeeded, want missing placement error")
	}
	if !strings.Contains(err.Error(), "SCRAP_SHARD_PLACEMENT_FILE") {
		t.Fatalf("error %q does not name SCRAP_SHARD_PLACEMENT_FILE", err)
	}
}

func TestValidateStartupRoutingGatesDefaultsSingleShardOutsideProduction(t *testing.T) {
	placement, err := validateStartupRoutingGates(Config{SecurityMode: security.ModeTest})
	if err != nil {
		t.Fatalf("validateStartupRoutingGates: %v", err)
	}
	if got := placement.RouteMapSummaryString(); got != "0-1023:shard=0" {
		t.Fatalf("RouteMapSummaryString() = %q, want single Shard map", got)
	}

	route, err := placement.Lookup("tx-bravo")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if route.ShardID != appShardID {
		t.Fatalf("route ShardID = %d, want appShardID %d", route.ShardID, appShardID)
	}
}

func TestValidateStartupTopologyLoadsProductionLocalShards(t *testing.T) {
	placementFile := writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [7, 9],
		"local_shards": [9, 7],
		"ranges": [
			{"shard_id": 7, "start_slot": 0, "end_slot": 511},
			{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
		]
	}`)

	topology, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeProduction,
		ShardPlacementFile: placementFile,
	})
	if err != nil {
		t.Fatalf("validateStartupTopology: %v", err)
	}
	if got := topology.RouteMapSummary; got != "0-511:shard=7,512-1023:shard=9" {
		t.Fatalf("RouteMapSummary = %q, want deterministic two-Shard summary", got)
	}
	if got, want := topology.LocalShardIDs, []uint64{7, 9}; !uint64SlicesEqual(got, want) {
		t.Fatalf("LocalShardIDs = %v, want %v", got, want)
	}
	if topology.SingleShardFallback {
		t.Fatal("SingleShardFallback = true, want multi-Shard production topology")
	}
}

func TestValidateStartupTopologyRejectsInvalidLocalMembership(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "omitted",
			body: `{
				"slot_count": 1024,
				"shards": [7, 9],
				"ranges": [
					{"shard_id": 7, "start_slot": 0, "end_slot": 511},
					{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
				]
			}`,
		},
		{
			name: "null",
			body: `{
				"slot_count": 1024,
				"shards": [7, 9],
				"local_shards": null,
				"ranges": [
					{"shard_id": 7, "start_slot": 0, "end_slot": 511},
					{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
				]
			}`,
		},
		{
			name: "empty",
			body: `{
				"slot_count": 1024,
				"shards": [7, 9],
				"local_shards": [],
				"ranges": [
					{"shard_id": 7, "start_slot": 0, "end_slot": 511},
					{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
				]
			}`,
		},
		{
			name: "duplicate",
			body: `{
				"slot_count": 1024,
				"shards": [7, 9],
				"local_shards": [7, 7],
				"ranges": [
					{"shard_id": 7, "start_slot": 0, "end_slot": 511},
					{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
				]
			}`,
		},
		{
			name: "unknown",
			body: `{
				"slot_count": 1024,
				"shards": [7, 9],
				"local_shards": [7, 10],
				"ranges": [
					{"shard_id": 7, "start_slot": 0, "end_slot": 511},
					{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
				]
			}`,
		},
		{
			name: "unknown field",
			body: `{
				"slot_count": 1024,
				"shards": [7, 9],
				"local_shards": [7, 9],
				"ranges": [
					{"shard_id": 7, "start_slot": 0, "end_slot": 511},
					{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
				],
				"local_shard_ids": [7, 9]
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placementFile := writePlacementFile(t, tt.body)
			_, err := validateStartupTopology(Config{
				SecurityMode:       security.ModeProduction,
				ShardPlacementFile: placementFile,
			})
			if err == nil {
				t.Fatal("validateStartupTopology succeeded, want local membership error")
			}
			if !errors.Is(err, routing.ErrInvalidPlacement) {
				t.Fatalf("validateStartupTopology error = %v, want ErrInvalidPlacement", err)
			}
			forbidden := []string{placementFile, t.TempDir(), "10.1.2.3", "secret-token", "tx-sensitive"}
			for _, raw := range forbidden {
				if raw != "" && strings.Contains(err.Error(), raw) {
					t.Fatalf("validateStartupTopology error leaked %q: %v", raw, err)
				}
			}
		})
	}
}

func TestValidateStartupTopologyAcceptsSingleLocalShardInProductionMultiShardPlacement(t *testing.T) {
	placementFile := writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [7, 9],
		"local_shards": [7],
		"ranges": [
			{"shard_id": 7, "start_slot": 0, "end_slot": 511},
			{"shard_id": 9, "start_slot": 512, "end_slot": 1023}
		]
	}`)

	topology, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeProduction,
		ShardPlacementFile: placementFile,
	})
	if err != nil {
		t.Fatalf("validateStartupTopology: %v", err)
	}
	if got, want := topology.LocalShardIDs, []uint64{7}; !uint64SlicesEqual(got, want) {
		t.Fatalf("LocalShardIDs = %v, want %v", got, want)
	}
	if got := topology.RouteMapSummary; got != "0-511:shard=7,512-1023:shard=9" {
		t.Fatalf("RouteMapSummary = %q, want two-Shard placement summary", got)
	}
}

func TestValidateStartupTopologyRejectsSingleShardProductionPlacement(t *testing.T) {
	placementFile := writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [7],
		"local_shards": [7],
		"ranges": [
			{"shard_id": 7, "start_slot": 0, "end_slot": 1023}
		]
	}`)

	_, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeProduction,
		ShardPlacementFile: placementFile,
	})
	if !errors.Is(err, routing.ErrInvalidPlacement) {
		t.Fatalf("validateStartupTopology error = %v, want ErrInvalidPlacement", err)
	}
}

func TestValidateStartupRoutingGatesRejectsInvalidPlacementFile(t *testing.T) {
	tests := []struct {
		name          string
		placementFile func(t *testing.T) string
	}{
		{
			name: "unreadable",
			placementFile: func(t *testing.T) string {
				t.Helper()
				return filepath.Join(t.TempDir(), "missing-placement.json")
			},
		},
		{
			name: "malformed",
			placementFile: func(t *testing.T) string {
				t.Helper()
				return writePlacementFile(t, `{`)
			},
		},
		{
			name: "invalid coverage",
			placementFile: func(t *testing.T) string {
				t.Helper()
				return writePlacementFile(t, `{
					"slot_count": 1024,
					"shards": [0],
					"ranges": [{"shard_id": 0, "start_slot": 0, "end_slot": 1022}]
				}`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			placementFile := tt.placementFile(t)
			_, err := validateStartupRoutingGates(Config{
				SecurityMode:       security.ModeTest,
				ShardPlacementFile: placementFile,
			})
			if err == nil {
				t.Fatal("validateStartupRoutingGates succeeded, want placement error")
			}
			if !errors.Is(err, routing.ErrInvalidPlacement) {
				t.Fatalf("error %q does not wrap ErrInvalidPlacement", err)
			}
			if !strings.Contains(err.Error(), "SCRAP_SHARD_PLACEMENT_FILE") {
				t.Fatalf("error %q does not name SCRAP_SHARD_PLACEMENT_FILE", err)
			}
			if strings.Contains(err.Error(), placementFile) {
				t.Fatalf("error leaked placement file path %q: %v", placementFile, err)
			}
		})
	}
}

func uint64SlicesEqual(a, b []uint64) bool {
	return slices.Equal(a, b)
}

func TestNewAppRejectsInvalidPlacementBeforeListeners(t *testing.T) {
	placementFile := writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [0],
		"ranges": [{"shard_id": 0, "start_slot": 1, "end_slot": 1023}]
	}`)
	cfg := productionTestAppConfig(t)
	cfg.ShardPlacementFile = placementFile
	cfg.ListenAddr = "bad-listen-address"
	cfg.PeerAddr = "bad-peer-address"
	cfg.AdminAddr = "bad-admin-address"

	_, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err == nil {
		t.Fatal("newApp succeeded, want invalid placement error")
	}
	if !errors.Is(err, routing.ErrInvalidPlacement) {
		t.Fatalf("newApp error = %v, want ErrInvalidPlacement", err)
	}
	if !strings.Contains(err.Error(), "SCRAP_SHARD_PLACEMENT_FILE") {
		t.Fatalf("error %q does not name SCRAP_SHARD_PLACEMENT_FILE", err)
	}
	for _, leaked := range []string{"listen client", "listen peer", "bad-admin-address"} {
		if strings.Contains(err.Error(), leaked) {
			t.Fatalf("newApp reached serving setup before placement validation: %v", err)
		}
	}
}

func TestNewAppRejectsShardDataDirSymlinkCollisionBeforeBackendSetup(t *testing.T) {
	t.Setenv("SCRAP_BACKEND_TYPE", "unsupported-backend")
	cfg := testAppConfig(t)
	cfg.UploadEnabled = true
	cfg.ShardPlacementFile = writeTwoShardPlacementFile(t)

	target := filepath.Join(cfg.DataDir, "shared-shard")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatalf("MkdirAll target: %v", err)
	}
	shardsDir := filepath.Join(cfg.DataDir, "shards")
	if err := os.MkdirAll(shardsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll shards dir: %v", err)
	}
	for _, name := range []string{"shard-7", "shard-9"} {
		if err := os.Symlink(target, filepath.Join(shardsDir, name)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}

	_, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if !errors.Is(err, routing.ErrInvalidPlacement) {
		t.Fatalf("newApp error = %v, want ErrInvalidPlacement before Backend setup", err)
	}
	if strings.Contains(err.Error(), "unsupported-backend") {
		t.Fatalf("newApp reached Backend setup before Shard data-dir validation: %v", err)
	}
}

func TestLocalShardDataDirsRejectsBrokenSymlinksToSameTarget(t *testing.T) {
	cfg := testAppConfig(t)
	topology, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeTest,
		ShardPlacementFile: writeTwoShardPlacementFile(t),
	})
	if err != nil {
		t.Fatalf("validateStartupTopology: %v", err)
	}
	shardsDir := filepath.Join(cfg.DataDir, "shards")
	if err := os.MkdirAll(shardsDir, 0o700); err != nil {
		t.Fatalf("MkdirAll shards dir: %v", err)
	}
	for _, name := range []string{"shard-7", "shard-9"} {
		if err := os.Symlink("future-shared-target", filepath.Join(shardsDir, name)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
	}

	_, err = localShardDataDirs(cfg.DataDir, topology)
	if !errors.Is(err, routing.ErrInvalidPlacement) {
		t.Fatalf("localShardDataDirs error = %v, want ErrInvalidPlacement", err)
	}
}

func TestNewAppAcceptsMultiShardPlacementBeforeListeners(t *testing.T) {
	placementFile := writeTwoShardPlacementFile(t)
	cfg := testAppConfig(t)
	cfg.ShardPlacementFile = placementFile
	cfg.ListenAddr = "bad-listen-address"

	_, err := newApp(context.Background(), cfg, slog.New(slog.DiscardHandler), BuildInfo{})
	if err == nil {
		t.Fatal("newApp succeeded, want listener setup error")
	}
	if !strings.Contains(err.Error(), "listen client") {
		t.Fatalf("newApp error = %v, want listener setup after accepted multi-Shard topology", err)
	}
	if errors.Is(err, routing.ErrInvalidPlacement) {
		t.Fatalf("newApp rejected valid multi-Shard placement: %v", err)
	}
}

func TestLoadShardPlacementFileRejectsMissingZeroValuedRangeFields(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing shard id",
			body: `{"slot_count":1024,"shards":[0],"ranges":[{"start_slot":0,"end_slot":1023}]}`,
		},
		{
			name: "null start slot",
			body: `{"slot_count":1024,"shards":[0],"ranges":[{"shard_id":0,"start_slot":null,"end_slot":1023}]}`,
		},
		{
			name: "missing end slot",
			body: `{"slot_count":1024,"shards":[0],"ranges":[{"shard_id":0,"start_slot":0}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadShardPlacementFile(writePlacementFile(t, tt.body))
			if !errors.Is(err, routing.ErrInvalidPlacement) {
				t.Fatalf("loadShardPlacementFile error = %v, want ErrInvalidPlacement", err)
			}
		})
	}
}

func testAppConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DataDir:           t.TempDir(),
		ListenAddr:        "127.0.0.1:0",
		PeerAddr:          "127.0.0.1:0",
		AdminAddr:         "127.0.0.1:0",
		BlockSealSize:     shard.DefaultBlockSealSize,
		UploadConcurrency: shard.DefaultUploadConcurrency,
		PeerPort:          defaultPeerPort,
		Namespace:         "default",
		SecurityMode:      security.ModeTest,
		Scrub:             scrub.ParseConfig(),
	}
}

func productionTestAppConfig(t *testing.T) Config {
	t.Helper()

	cfg := testAppConfig(t)
	cfg.SecurityMode = security.ModeProduction
	cfg.CellID = "cell-a"
	cfg.UploadEnabled = false
	cfg.PeersFlag = "1=localhost:9091,2=localhost:9092,3=localhost:9093"
	cfg.ClientAddrsFlag = "1=localhost:9090,2=localhost:9090,3=localhost:9090"

	const memberID = "member-1"
	t.Setenv("OPENBAO_TOKEN", "test-token")
	identityPath := filepath.Join(cfg.DataDir, "identity", "member.json")
	if err := os.MkdirAll(filepath.Dir(identityPath), memberIdentityDirMode); err != nil {
		t.Fatalf("MkdirAll identity directory: %v", err)
	}
	identityData, err := json.Marshal(memberIdentityRecord{MemberID: memberID})
	if err != nil {
		t.Fatalf("Marshal member identity: %v", err)
	}
	if err := os.WriteFile(identityPath, identityData, memberIdentityFileMode); err != nil {
		t.Fatalf("WriteFile member identity: %v", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		t.Fatalf("hostname: %v", err)
	}
	bundle := securityfixture.WriteCertBundle(t, t.TempDir(), securityfixture.CertOptions{
		ServerName: "scrap.local",
	})
	cfg.ProductionGates = security.StartupGateConfig{
		Mode: security.ModeProduction,
		TLS: security.TLSConfig{
			Public:   productionTLSFiles(bundle),
			Peer:     productionTLSFiles(bundle),
			Admin:    productionTLSFiles(bundle),
			Scrapctl: productionTLSFiles(bundle),
		},
		RolePolicyPath:         writeRolePolicy(t, t.TempDir()),
		PeerIdentityPolicyPath: writePeerIdentityPolicy(t, t.TempDir(), cfg.CellID, hostname, memberID),
		Transit: security.TransitConfig{
			Address:      "https://openbao.example.invalid",
			MountPath:    "transit",
			KeyName:      "scrap-documents",
			TokenEnv:     "OPENBAO_TOKEN",
			TokenPresent: true,
		},
		AuditSink:  security.AuditSinkConfig{PolicyPath: writeAuditPolicy(t, t.TempDir())},
		RateLimits: security.RateLimitConfig{PolicyPath: writeRateLimitPolicy(t, t.TempDir())},
	}
	return cfg
}

func writePeerIdentityPolicy(t *testing.T, dir, cellID, memberHostname, memberID string) string {
	t.Helper()
	path := filepath.Join(dir, "peer-identity.json")
	data, err := json.Marshal(map[string]any{
		"cell_id":         cellID,
		"member_hostname": memberHostname,
		"member_id":       memberID,
	})
	if err != nil {
		t.Fatalf("marshal peer identity policy: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write peer identity policy: %v", err)
	}
	return path
}

func writePlacementFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "placement.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write placement fixture: %v", err)
	}
	return path
}

func writeTwoShardPlacementFile(t *testing.T) string {
	t.Helper()
	const firstShardID uint64 = 7
	const secondShardID uint64 = 9
	return writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [`+strconv.FormatUint(firstShardID, 10)+`, `+strconv.FormatUint(secondShardID, 10)+`],
		"local_shards": [`+strconv.FormatUint(firstShardID, 10)+`, `+strconv.FormatUint(secondShardID, 10)+`],
		"ranges": [
			{"shard_id": `+strconv.FormatUint(firstShardID, 10)+`, "start_slot": 0, "end_slot": 511},
			{"shard_id": `+strconv.FormatUint(secondShardID, 10)+`, "start_slot": 512, "end_slot": 1023}
		]
	}`)
}

func TestPersistPlacementIdentityRejectsSilentRemap(t *testing.T) {
	dataDir := t.TempDir()
	first, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeTest,
		ShardPlacementFile: writeTwoShardPlacementFile(t),
	})
	if err != nil {
		t.Fatalf("validateStartupTopology: %v", err)
	}
	if err := persistPlacementIdentity(dataDir, first); err != nil {
		t.Fatalf("persistPlacementIdentity initial: %v", err)
	}
	if err := persistPlacementIdentity(dataDir, first); err != nil {
		t.Fatalf("persistPlacementIdentity idempotent: %v", err)
	}

	remapped := writePlacementFile(t, `{
		"slot_count": 1024,
		"shards": [7, 9],
		"local_shards": [7, 9],
		"ranges": [
			{"shard_id": 9, "start_slot": 0, "end_slot": 511},
			{"shard_id": 7, "start_slot": 512, "end_slot": 1023}
		]
	}`)
	second, err := validateStartupTopology(Config{
		SecurityMode:       security.ModeTest,
		ShardPlacementFile: remapped,
	})
	if err != nil {
		t.Fatalf("validateStartupTopology remapped: %v", err)
	}
	err = persistPlacementIdentity(dataDir, second)
	if err == nil {
		t.Fatal("persistPlacementIdentity accepted silent remap")
	}
	if !errors.Is(err, routing.ErrInvalidPlacement) {
		t.Fatalf("persistPlacementIdentity error = %v, want ErrInvalidPlacement", err)
	}
}
