package main

import "testing"

func TestScrapdTelemetryResourceConfigUsesMemberAndBuildMetadata(t *testing.T) {
	t.Setenv("SCRAP_CELL_ID", "cell-a")
	t.Setenv("SCRAP_MEMBER_ID", "member-123")
	t.Setenv("SCRAP_ENVIRONMENT", "stress")

	oldVersion, oldBuildSHA, oldBuildTime := version, buildSHA, buildTime
	version = "v2.3.4"
	buildSHA = "abc123"
	buildTime = "2026-05-28T12:00:00Z"
	t.Cleanup(func() {
		version = oldVersion
		buildSHA = oldBuildSHA
		buildTime = oldBuildTime
	})

	cfg := scrapdTelemetryResourceConfig("scrapd-0", 3, 7)

	assertString(t, "ServiceName", cfg.ServiceName, "scrapd")
	assertString(t, "Environment", cfg.Environment, "stress")
	assertString(t, "Version", cfg.Version, "v2.3.4")
	assertString(t, "BuildSHA", cfg.BuildSHA, "abc123")
	assertString(t, "BuildTime", cfg.BuildTime, "2026-05-28T12:00:00Z")
	assertString(t, "CellID", cfg.CellID, "cell-a")
	assertString(t, "MemberSlotID", cfg.MemberSlotID, "scrapd-0")
	assertString(t, "MemberID", cfg.MemberID, "member-123")
	if cfg.RaftID != 3 {
		t.Fatalf("RaftID = %d, want 3", cfg.RaftID)
	}
	if cfg.ShardID != 7 {
		t.Fatalf("ShardID = %d, want 7", cfg.ShardID)
	}
}

func assertString(t *testing.T, field, got, want string) {
	t.Helper()

	if got != want {
		t.Fatalf("%s = %q, want %q", field, got, want)
	}
}
