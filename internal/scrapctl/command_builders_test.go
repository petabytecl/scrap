package scrapctl

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestInspectCommandBuildersCallExpectedRPCs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantOutput string
		assert     func(*commandBuilderAdminServer)
	}{
		{
			name:       "summary",
			args:       []string{"inspect", "summary"},
			wantOutput: "shard_count",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.summaryCalls, 1, "summary calls")
			},
		},
		{
			name:       "shard",
			args:       []string{"inspect", "shard", "--shard-id", "shard-a"},
			wantOutput: "shard-a",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.getShardReq.GetShardId(), "shard-a", "shard id")
			},
		},
		{
			name: "document",
			args: []string{
				"inspect", "document",
				"--tenant-id", "tenant-a",
				"--transaction-id", "tx-a",
				"--document-name", "invoice.xml",
			},
			wantOutput: "invoice.xml",
			assert: func(fake *commandBuilderAdminServer) {
				doc := fake.getDocumentReq.GetDocument()
				testutil.RequireEqualf(t, doc.GetTenantId(), "tenant-a", "document tenant")
				testutil.RequireEqualf(t, doc.GetTransactionId(), "tx-a", "document transaction")
				testutil.RequireEqualf(t, doc.GetDocumentName(), "invoice.xml", "document name")
			},
		},
		{
			name:       "block",
			args:       []string{"inspect", "block", "--shard-id", "shard-a", "--block-id", "block-1"},
			wantOutput: "block-1",
			assert: func(fake *commandBuilderAdminServer) {
				block := fake.getBlockReq.GetBlock()
				testutil.RequireEqualf(t, block.GetShardId(), "shard-a", "block shard")
				testutil.RequireEqualf(t, block.GetBlockId(), "block-1", "block id")
			},
		},
		{
			name:       "member",
			args:       []string{"inspect", "member", "--member-id", "member-a"},
			wantOutput: "member-a",
			assert: func(fake *commandBuilderAdminServer) {
				member := fake.getMemberReq.GetStorageMember()
				testutil.RequireEqualf(t, member.GetStorageMemberId(), "member-a", "member id")
			},
		},
		{
			name:       "capacity runway",
			args:       []string{"inspect", "capacity-runway", "--capacity-profile-id", "scrap-prod-v1"},
			wantOutput: "scrap-prod-v1",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.getCapacityRunwayReq.GetCapacityProfileId(), "scrap-prod-v1", "capacity profile")
			},
		},
		{
			name:       "repair queue",
			args:       []string{"inspect", "repair-queue", "--shard-id", "shard-a", "--page-size", "2", "--page-token", "next"},
			wantOutput: "next-page",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.getRepairQueueReq.GetShardId(), "shard-a", "repair shard")
				testutil.RequireEqualf(t, fake.getRepairQueueReq.GetPageSize(), uint32(2), "repair page size")
				testutil.RequireEqualf(t, fake.getRepairQueueReq.GetPageToken(), "next", "repair page token")
			},
		},
		{
			name:       "recovery readiness",
			args:       []string{"inspect", "recovery-readiness"},
			wantOutput: "latest_restorable_checkpoint_at",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.recoveryReadinessCalls, 1, "recovery readiness calls")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &commandBuilderAdminServer{}
			output := runScrapctlWithCommandBuilderFake(t, fake, tc.args...)
			testutil.RequireTruef(t, strings.Contains(output, tc.wantOutput), "output = %s, missing %s", output, tc.wantOutput)
			tc.assert(fake)
		})
	}
}

func TestPlanCommandBuildersCallExpectedRPCs(t *testing.T) {
	pinUntil := "2030-01-02T03:04:05Z"
	expiresAt := "2030-02-03T04:05:06Z"
	cases := []struct {
		name       string
		args       []string
		wantOutput string
		assert     func(*commandBuilderAdminServer)
	}{
		{
			name: "restore",
			args: []string{
				"plan", "restore",
				"--target", "document:tenant-a/tx-a/invoice.xml",
				"--dry-run",
				"--metadata", "ticket=INC-1",
			},
			wantOutput: "plan-restore",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "restore", "ticket", "INC-1")
				testutil.RequireTruef(t, fake.lastPlan.dryRun, "restore dry run")
				requireTargetKind(t, fake.lastPlan.targets[0], "document")
			},
		},
		{
			name: "prewarm",
			args: []string{
				"plan", "prewarm",
				"--target", "transaction:tenant-a/tx-a",
				"--pin-until", pinUntil,
			},
			wantOutput: "plan-prewarm",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "prewarm", "", "")
				testutil.RequireTruef(t, fake.lastPlan.pinUntil != nil, "prewarm pin_until")
				requireTargetKind(t, fake.lastPlan.targets[0], "transaction")
			},
		},
		{
			name:       "repair",
			args:       []string{"plan", "repair", "--target", "block:shard-a/block-1"},
			wantOutput: "plan-repair",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "repair", "", "")
				requireTargetKind(t, fake.lastPlan.targets[0], "block")
			},
		},
		{
			name:       "scrub",
			args:       []string{"plan", "scrub", "--target", "shard:shard-a"},
			wantOutput: "plan-scrub",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "scrub", "", "")
				requireTargetKind(t, fake.lastPlan.targets[0], "shard")
			},
		},
		{
			name:       "drain",
			args:       []string{"plan", "drain", "--member-id", "member-a", "--dry-run", "--metadata", "ticket=INC-2"},
			wantOutput: "plan-drain",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "drain", "ticket", "INC-2")
				testutil.RequireEqualf(t, fake.lastPlan.memberID, "member-a", "drain member id")
				testutil.RequireTruef(t, fake.lastPlan.dryRun, "drain dry run")
			},
		},
		{
			name:       "tombstone",
			args:       []string{"plan", "tombstone", "--target", "snapshot:snapshot-a"},
			wantOutput: "plan-tombstone",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "tombstone", "", "")
				requireTargetKind(t, fake.lastPlan.targets[0], "snapshot")
			},
		},
		{
			name:       "key rotation",
			args:       []string{"plan", "key-rotation", "--target", "capacity-profile:scrap-prod-v1", "--destination-key-id", "key-next"},
			wantOutput: "plan-key-rotation",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "key-rotation", "", "")
				testutil.RequireEqualf(t, fake.lastPlan.destinationKeyID, "key-next", "destination key")
				requireTargetKind(t, fake.lastPlan.targets[0], "capacity_profile")
			},
		},
		{
			name:       "capacity override",
			args:       []string{"plan", "capacity-override", "--capacity-profile-id", "scrap-prod-v1", "--expires-at", expiresAt, "--reason", "incident"},
			wantOutput: "plan-capacity-override",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "capacity-override", "", "")
				testutil.RequireEqualf(t, fake.lastPlan.capacityProfileID, "scrap-prod-v1", "capacity profile")
				testutil.RequireEqualf(t, fake.lastPlan.reason, "incident", "capacity reason")
				testutil.RequireTruef(t, fake.lastPlan.expiresAt != nil, "capacity expires at")
			},
		},
		{
			name:       "recovery",
			args:       []string{"plan", "recovery", "--target", "member:member-a"},
			wantOutput: "plan-recovery",
			assert: func(fake *commandBuilderAdminServer) {
				requirePlanRecord(t, fake.lastPlan, "recovery", "", "")
				requireTargetKind(t, fake.lastPlan.targets[0], "storage_member")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &commandBuilderAdminServer{}
			output := runScrapctlWithCommandBuilderFake(t, fake, tc.args...)
			testutil.RequireTruef(t, strings.Contains(output, tc.wantOutput), "output = %s, missing %s", output, tc.wantOutput)
			tc.assert(fake)
		})
	}
}

func TestStartCommandBuildersCallExpectedRPCs(t *testing.T) {
	for _, operation := range []string{
		"restore",
		"prewarm",
		"repair",
		"scrub",
		"drain",
		"tombstone",
		"key-rotation",
		"capacity-override",
		"metadata-restore",
		"copy-verify",
		"dr-drill",
	} {
		t.Run(operation, func(t *testing.T) {
			fake := &commandBuilderAdminServer{}
			output := runScrapctlWithCommandBuilderFake(t, fake,
				"start", operation,
				"--plan-id", "plan-1",
				"--plan-hash", "hash-1",
				"--operation-id", "op-1",
				"--metadata", "ticket=INC-3",
			)
			testutil.RequireTruef(t, strings.Contains(output, "op-1"), "output = %s, missing operation id", output)
			testutil.RequireEqualf(t, fake.lastStart.kind, operation, "start operation kind")
			testutil.RequireEqualf(t, fake.lastStart.planID, "plan-1", "start plan id")
			testutil.RequireEqualf(t, fake.lastStart.planHash, "hash-1", "start plan hash")
			testutil.RequireEqualf(t, fake.lastStart.operationID, "op-1", "start operation id")
			testutil.RequireEqualf(t, fake.lastStart.metadata["ticket"], "INC-3", "start metadata")
		})
	}
}

func TestOperationsAndMemberCommandBuildersCallExpectedRPCs(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantOutput string
		assert     func(*commandBuilderAdminServer)
	}{
		{
			name: "operations list",
			args: []string{
				"operations", "list",
				"--state", "running",
				"--state", "OPERATION_STATE_FAILED",
				"--type", "repair",
				"--page-size", "2",
				"--page-token", "next",
			},
			wantOutput: "next-page",
			assert: func(fake *commandBuilderAdminServer) {
				req := fake.listOperationsReq
				testutil.RequireEqualf(t, len(req.GetStates()), 2, "operation states")
				testutil.RequireEqualf(t, req.GetOperationType(), "repair", "operation type")
				testutil.RequireEqualf(t, req.GetPageSize(), uint32(2), "operation page size")
				testutil.RequireEqualf(t, req.GetPageToken(), "next", "operation page token")
			},
		},
		{
			name:       "operations cancel",
			args:       []string{"operations", "cancel", "--operation-id", "op-cancel"},
			wantOutput: "op-cancel",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.cancelOperationReq.GetOperationId(), "op-cancel", "cancel operation id")
			},
		},
		{
			name:       "member cordon",
			args:       []string{"member", "cordon", "--member-id", "member-a", "--reason", "maintenance", "--operation-id", "op-cordon"},
			wantOutput: "member-a",
			assert: func(fake *commandBuilderAdminServer) {
				req := fake.cordonMemberReq
				testutil.RequireEqualf(t, req.GetStorageMember().GetStorageMemberId(), "member-a", "cordon member")
				testutil.RequireEqualf(t, req.GetReason(), "maintenance", "cordon reason")
				testutil.RequireEqualf(t, req.GetOperationId(), "op-cordon", "cordon operation")
			},
		},
		{
			name:       "member uncordon",
			args:       []string{"member", "uncordon", "--member-id", "member-a", "--operation-id", "op-uncordon"},
			wantOutput: "member-a",
			assert: func(fake *commandBuilderAdminServer) {
				req := fake.uncordonMemberReq
				testutil.RequireEqualf(t, req.GetStorageMember().GetStorageMemberId(), "member-a", "uncordon member")
				testutil.RequireEqualf(t, req.GetOperationId(), "op-uncordon", "uncordon operation")
			},
		},
		{
			name:       "member eviction safety",
			args:       []string{"member", "eviction-safety", "--member-id", "member-a"},
			wantOutput: "safe_to_evict",
			assert: func(fake *commandBuilderAdminServer) {
				testutil.RequireEqualf(t, fake.evictionSafetyReq.GetStorageMember().GetStorageMemberId(), "member-a", "eviction member")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &commandBuilderAdminServer{}
			output := runScrapctlWithCommandBuilderFake(t, fake, tc.args...)
			testutil.RequireTruef(t, strings.Contains(output, tc.wantOutput), "output = %s, missing %s", output, tc.wantOutput)
			tc.assert(fake)
		})
	}
}

func TestCommandBuildersRejectInvalidArgsBeforeDial(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "unknown top command", args: []string{"unknown"}, wantErr: "unknown command"},
		{name: "inspect missing subject", args: []string{"inspect"}, wantErr: "inspect subject is required"},
		{name: "inspect unknown subject", args: []string{"inspect", "planet"}, wantErr: "unknown inspect subject"},
		{name: "inspect summary rejects extra args", args: []string{"inspect", "summary", "extra"}, wantErr: "unexpected argument"},
		{name: "inspect shard missing id", args: []string{"inspect", "shard"}, wantErr: "--shard-id"},
		{name: "inspect document missing name", args: []string{"inspect", "document", "--tenant-id", "tenant-a", "--transaction-id", "tx-a"}, wantErr: "--document-name"},
		{name: "inspect block missing block", args: []string{"inspect", "block", "--shard-id", "shard-a"}, wantErr: "--block-id"},
		{name: "inspect member missing id", args: []string{"inspect", "member"}, wantErr: "--member-id"},
		{name: "inspect repair queue oversized page", args: []string{"inspect", "repair-queue", "--page-size", "4294967296"}, wantErr: "--page-size"},
		{name: "inspect recovery readiness rejects extra args", args: []string{"inspect", "recovery-readiness", "extra"}, wantErr: "unexpected argument"},
		{name: "plan missing operation", args: []string{"plan"}, wantErr: "plan operation is required"},
		{name: "prewarm malformed pin until", args: []string{"plan", "prewarm", "--target", "document:tenant-a/tx-a/doc.xml", "--pin-until", "tomorrow"}, wantErr: "--pin-until"},
		{name: "restore rejects pin until", args: []string{"plan", "restore", "--target", "document:tenant-a/tx-a/doc.xml", "--pin-until", "2030-01-02T03:04:05Z"}, wantErr: "does not accept --pin-until"},
		{name: "drain missing member", args: []string{"plan", "drain"}, wantErr: "--member-id"},
		{name: "key rotation missing destination", args: []string{"plan", "key-rotation", "--target", "shard:shard-a"}, wantErr: "--destination-key-id"},
		{name: "capacity override malformed expiration", args: []string{"plan", "capacity-override", "--capacity-profile-id", "scrap-prod-v1", "--expires-at", "next-week", "--reason", "incident"}, wantErr: "--expires-at"},
		{name: "capacity override missing reason", args: []string{"plan", "capacity-override", "--capacity-profile-id", "scrap-prod-v1", "--expires-at", "2030-01-02T03:04:05Z"}, wantErr: "--reason"},
		{name: "start missing operation", args: []string{"start"}, wantErr: "start operation is required"},
		{name: "start missing hash", args: []string{"start", "repair", "--plan-id", "plan-1", "--operation-id", "op-1"}, wantErr: "--plan-hash"},
		{name: "start unknown operation", args: []string{"start", "unknown", "--plan-id", "plan-1", "--plan-hash", "hash-1", "--operation-id", "op-1"}, wantErr: "unknown start operation"},
		{name: "operations missing command", args: []string{"operations"}, wantErr: "operations command is required"},
		{name: "operations list malformed state", args: []string{"operations", "list", "--state", "half-done"}, wantErr: "unknown operation state"},
		{name: "operations cancel missing id", args: []string{"operations", "cancel"}, wantErr: "--operation-id"},
		{name: "status missing operation id", args: []string{"status"}, wantErr: "--operation-id"},
		{name: "watch missing operation id", args: []string{"watch"}, wantErr: "--operation-id"},
		{name: "member missing command", args: []string{"member"}, wantErr: "member command is required"},
		{name: "member cordon missing reason", args: []string{"member", "cordon", "--member-id", "member-a", "--operation-id", "op-1"}, wantErr: "--reason"},
		{name: "member uncordon missing operation", args: []string{"member", "uncordon", "--member-id", "member-a"}, wantErr: "--operation-id"},
		{name: "member eviction safety missing member", args: []string{"member", "eviction-safety"}, wantErr: "--member-id"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calledDial := false
			err := Run(context.Background(), Config{
				Dial: func(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
					calledDial = true
					return nil, errors.New("dial should not happen")
				},
			}, append([]string{"--admin-addr", "bufnet"}, tc.args...), bytes.NewBuffer(nil))
			var usage usageError
			if !errors.As(err, &usage) {
				t.Fatalf("error = %v, want usageError", err)
			}
			testutil.RequireTruef(t, strings.Contains(err.Error(), tc.wantErr), "error = %v, want %q", err, tc.wantErr)
			testutil.RequireFalsef(t, calledDial, "dialer should not be called")
		})
	}
}

func TestScrapctlHelperParsers(t *testing.T) {
	targetCases := []struct {
		name string
		kind string
		spec string
		want string
	}{
		{name: "document", kind: "document", spec: "tenant-a/tx-a/doc.xml", want: "document"},
		{name: "transaction alias", kind: "txn", spec: "tenant-a/tx-a", want: "transaction"},
		{name: "block", kind: "block", spec: "shard-a/block-1", want: "block"},
		{name: "shard", kind: "shard", spec: "shard-a", want: "shard"},
		{name: "member alias", kind: "member", spec: "member-a", want: "storage_member"},
		{name: "snapshot", kind: "snapshot", spec: "snapshot-a", want: "snapshot"},
		{name: "capacity profile", kind: "capacity-profile", spec: "scrap-prod-v1", want: "capacity_profile"},
	}
	for _, tc := range targetCases {
		t.Run(tc.name, func(t *testing.T) {
			target, err := parseTarget(tc.kind, tc.spec)
			testutil.RequireNoErrorf(t, err, "parse target")
			requireTargetKind(t, target, tc.want)
		})
	}

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{name: "missing target separator", run: func() error { return (&targetFlag{}).Set("document") }},
		{name: "malformed transaction target", run: func() error { _, err := parseTarget("transaction", "tenant-only"); return err }},
		{name: "malformed block target", run: func() error { _, err := parseTarget("block", "shard-only"); return err }},
		{name: "unknown target kind", run: func() error { _, err := parseTarget("planet", "mars"); return err }},
		{name: "malformed metadata", run: func() error { return (&metadataFlag{}).Set("ticket") }},
		{name: "malformed uint64 list", run: func() error { return (&uint64ListFlag{}).Set("-1") }},
		{name: "unknown state", run: func() error { _, err := parseState("half-done"); return err }},
		{name: "malformed timestamp", run: func() error { _, err := parseTimestamp("yesterday"); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("parser accepted malformed input")
			}
		})
	}

	var metadata metadataFlag
	testutil.RequireNoErrorf(t, metadata.Set("ticket=INC-1"), "set metadata")
	testutil.RequireNoErrorf(t, metadata.Set("owner=storage"), "set metadata")
	testutil.RequireEqualf(t, metadata.String(), "owner,ticket", "metadata string")
	copied := metadata.mapValue()
	copied["ticket"] = "changed"
	testutil.RequireEqualf(t, metadata["ticket"], "INC-1", "metadata map copy")

	var sizes uint64ListFlag
	testutil.RequireNoErrorf(t, sizes.Set("64"), "set size")
	testutil.RequireNoErrorf(t, sizes.Set("128"), "set size")
	testutil.RequireEqualf(t, sizes.String(), "64,128", "uint64 list string")

	var states stateFlag
	testutil.RequireNoErrorf(t, states.Set("running"), "set state")
	testutil.RequireNoErrorf(t, states.Set("OPERATION_STATE_FAILED"), "set state")
	testutil.RequireEqualf(t, states.String(), "OPERATION_STATE_RUNNING,OPERATION_STATE_FAILED", "state list string")

	parsed, err := parseTimestamp("2030-01-02T03:04:05.123456789-03:00")
	testutil.RequireNoErrorf(t, err, "parse timestamp")
	testutil.RequireEqualf(t, parsed.Location(), time.UTC, "timestamp location")
	testutil.RequireEqualf(t, parsed.Format(time.RFC3339Nano), "2030-01-02T06:04:05.123456789Z", "timestamp UTC")

	testutil.RequireNilf(t, optionalString(""), "empty optional string")
	testutil.RequireEqualf(t, *optionalString("value"), "value", "optional string")
	testutil.RequireNilf(t, optionalUint64(0), "zero optional uint64")
	testutil.RequireEqualf(t, *optionalUint64(7), uint64(7), "optional uint64")
	pageSize, err := optionalPageSize(9, "test")
	testutil.RequireNoErrorf(t, err, "optional page size")
	testutil.RequireEqualf(t, *pageSize.value, uint32(9), "optional page size")
}

func TestMainAndWriteFailureEmitStructuredErrors(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Main(nil, &stdout, &stderr)
	testutil.RequireEqualf(t, code, 2, "usage exit code")
	testutil.RequireTruef(t, strings.Contains(stderr.String(), `"code": "USAGE"`), "usage stderr = %s", stderr.String())
	testutil.RequireEqualf(t, stdout.Len(), 0, "stdout bytes")

	stderr.Reset()
	writeFailure(&stderr, errors.New("dial failed"))
	testutil.RequireTruef(t, strings.Contains(stderr.String(), `"code": "LOCAL_ERROR"`), "local error stderr = %s", stderr.String())
}

func runScrapctlWithCommandBuilderFake(t *testing.T, fake *commandBuilderAdminServer, args ...string) string {
	t.Helper()

	dial := newBufconnDialer(t, func(server *grpc.Server) {
		registerCommandBuilderFake(server, fake)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var out bytes.Buffer
	err := Run(ctx, Config{Dial: dial}, append([]string{"--admin-addr", "bufnet"}, args...), &out)
	testutil.RequireNoErrorf(t, err, "run scrapctl")
	if out.Len() == 0 {
		t.Fatal("scrapctl wrote no output")
	}
	return out.String()
}
