package scrapctl

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/closeutil"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
)

const defaultAdminAddress = "127.0.0.1:18081"

var jsonFormat = protojson.MarshalOptions{
	Multiline:     true,
	Indent:        "  ",
	UseProtoNames: true,
}

type Dialer func(context.Context, string) (*grpc.ClientConn, error)

type Config struct {
	Dial Dialer
}

type clients struct {
	inspect    adminv1.InspectServiceClient
	operations adminv1.OperationServiceClient
	restore    adminv1.RestoreServiceClient
	repair     adminv1.RepairServiceClient
	member     adminv1.MemberServiceClient
	lifecycle  adminv1.LifecycleServiceClient
	key        adminv1.KeyServiceClient
	capacity   adminv1.CapacityServiceClient
	dr         adminv1.DisasterRecoveryServiceClient
}

type action func(context.Context, clients, io.Writer) error

type usageError string

func (e usageError) Error() string {
	return string(e)
}

func Main(args []string, stdout, stderr io.Writer) int {
	if err := Run(context.Background(), Config{}, args, stdout); err != nil {
		writeFailure(stderr, err)
		var usage usageError
		if errors.As(err, &usage) {
			return 2
		}
		return 1
	}
	return 0
}

func Run(ctx context.Context, cfg Config, args []string, stdout io.Writer) error {
	global := newFlagSet("scrapctl")
	adminAddr := global.String("admin-addr", defaultAdminAddress, "admin gRPC address")
	timeout := global.Duration("timeout", 30*time.Second, "RPC timeout")
	if err := global.Parse(args); err != nil {
		return usageError(err.Error())
	}
	rest := global.Args()
	if len(rest) == 0 {
		return usageError("command is required")
	}

	call, err := buildAction(rest)
	if err != nil {
		return err
	}

	dial := cfg.Dial
	if dial == nil {
		dial = defaultDial
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	conn, err := dial(ctx, *adminAddr)
	if err != nil {
		return err
	}
	defer closeutil.Ignore(conn)

	return call(ctx, newClients(conn), stdout)
}

func defaultDial(_ context.Context, address string) (*grpc.ClientConn, error) {
	return grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func newClients(conn grpc.ClientConnInterface) clients {
	return clients{
		inspect:    adminv1.NewInspectServiceClient(conn),
		operations: adminv1.NewOperationServiceClient(conn),
		restore:    adminv1.NewRestoreServiceClient(conn),
		repair:     adminv1.NewRepairServiceClient(conn),
		member:     adminv1.NewMemberServiceClient(conn),
		lifecycle:  adminv1.NewLifecycleServiceClient(conn),
		key:        adminv1.NewKeyServiceClient(conn),
		capacity:   adminv1.NewCapacityServiceClient(conn),
		dr:         adminv1.NewDisasterRecoveryServiceClient(conn),
	}
}

func buildAction(args []string) (action, error) {
	switch args[0] {
	case "inspect":
		return buildInspectAction(args[1:])
	case "plan":
		return buildPlanAction(args[1:])
	case "start":
		return buildStartAction(args[1:])
	case "status":
		return buildStatusAction(args[1:])
	case "watch":
		return buildWatchAction(args[1:])
	case "operations":
		return buildOperationsAction(args[1:])
	case "member":
		return buildMemberAction(args[1:])
	default:
		return nil, usageError(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func buildInspectAction(args []string) (action, error) {
	if len(args) == 0 {
		return nil, usageError("inspect subject is required")
	}
	switch args[0] {
	case "summary":
		fs := newFlagSet("inspect summary")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.inspect.GetClusterSummary(ctx, &adminv1.GetClusterSummaryRequest{})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetSummary())
		}, nil
	case "shard":
		fs := newFlagSet("inspect shard")
		shardID := fs.String("shard-id", "", "shard id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *shardID == "" {
			return nil, usageError("inspect shard requires --shard-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.inspect.GetShard(ctx, &adminv1.GetShardRequest{ShardId: *shardID})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetShard())
		}, nil
	case "document":
		fs := newFlagSet("inspect document")
		doc := documentFlags(fs)
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		target, err := doc.target()
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.inspect.GetDocument(ctx, &adminv1.GetDocumentRequest{Document: target})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetDocument())
		}, nil
	case "block":
		fs := newFlagSet("inspect block")
		block := blockFlags(fs)
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		target, err := block.target()
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.inspect.GetBlock(ctx, &adminv1.GetBlockRequest{Block: target})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetBlock())
		}, nil
	case "member":
		fs := newFlagSet("inspect member")
		memberID := fs.String("member-id", "", "storage member id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *memberID == "" {
			return nil, usageError("inspect member requires --member-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.inspect.GetMember(ctx, &adminv1.GetMemberRequest{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: *memberID},
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetStorageMember())
		}, nil
	case "capacity-runway":
		fs := newFlagSet("inspect capacity-runway")
		profileID := fs.String("capacity-profile-id", "", "capacity profile id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.inspect.GetCapacityRunway(ctx, &adminv1.GetCapacityRunwayRequest{
				CapacityProfileId: optionalString(*profileID),
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetRunway())
		}, nil
	case "repair-queue":
		fs := newFlagSet("inspect repair-queue")
		shardID := fs.String("shard-id", "", "shard id")
		pageSize := fs.Uint64("page-size", 0, "page size")
		pageToken := fs.String("page-token", "", "page token")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		pageSizeValue, err := optionalPageSize(*pageSize, "inspect repair-queue")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.repair.GetRepairQueue(ctx, &adminv1.GetRepairQueueRequest{
				ShardId:   optionalString(*shardID),
				PageSize:  pageSizeValue.value,
				PageToken: *pageToken,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp)
		}, nil
	case "recovery-readiness":
		fs := newFlagSet("inspect recovery-readiness")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.dr.GetRecoveryReadiness(ctx, &adminv1.GetRecoveryReadinessRequest{})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetReadiness())
		}, nil
	default:
		return nil, usageError(fmt.Sprintf("unknown inspect subject %q", args[0]))
	}
}

func buildPlanAction(args []string) (action, error) {
	if len(args) == 0 {
		return nil, usageError("plan operation is required")
	}
	switch args[0] {
	case "restore":
		opts, err := parseTargetPlan("plan restore", args[1:], false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.restore.PlanRestore(ctx, &adminv1.PlanRestoreRequest{
				Targets:  opts.targets,
				DryRun:   opts.dryRun,
				Metadata: opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "prewarm":
		opts, err := parseTargetPlan("plan prewarm", args[1:], true)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.restore.PlanPrewarm(ctx, &adminv1.PlanPrewarmRequest{
				Targets:  opts.targets,
				PinUntil: opts.pinUntil,
				DryRun:   opts.dryRun,
				Metadata: opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "repair":
		opts, err := parseTargetPlan("plan repair", args[1:], false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.repair.PlanRepair(ctx, &adminv1.PlanRepairRequest{
				Targets:  opts.targets,
				DryRun:   opts.dryRun,
				Metadata: opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "scrub":
		opts, err := parseTargetPlan("plan scrub", args[1:], false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.repair.PlanScrub(ctx, &adminv1.PlanScrubRequest{
				Targets:  opts.targets,
				DryRun:   opts.dryRun,
				Metadata: opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "drain":
		fs := newFlagSet("plan drain")
		memberID := fs.String("member-id", "", "storage member id")
		dryRun := fs.Bool("dry-run", false, "request a dry-run plan")
		metadata := metadataFlag{}
		fs.Var(&metadata, "metadata", "metadata key=value; may be repeated")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *memberID == "" {
			return nil, usageError("plan drain requires --member-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.member.PlanDrain(ctx, &adminv1.PlanDrainRequest{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: *memberID},
				DryRun:        *dryRun,
				Metadata:      metadata.mapValue(),
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "tombstone":
		opts, err := parseTargetPlan("plan tombstone", args[1:], false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.lifecycle.PlanTombstone(ctx, &adminv1.PlanTombstoneRequest{
				Targets:  opts.targets,
				DryRun:   opts.dryRun,
				Metadata: opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "key-rotation":
		fs := newFlagSet("plan key-rotation")
		targets := targetFlag{}
		metadata := metadataFlag{}
		destinationKeyID := fs.String("destination-key-id", "", "destination key id")
		dryRun := fs.Bool("dry-run", false, "request a dry-run plan")
		fs.Var(&targets, "target", "target kind:value; may be repeated")
		fs.Var(&metadata, "metadata", "metadata key=value; may be repeated")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if len(targets) == 0 {
			return nil, usageError("plan key-rotation requires at least one --target")
		}
		if *destinationKeyID == "" {
			return nil, usageError("plan key-rotation requires --destination-key-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.key.PlanKeyRotation(ctx, &adminv1.PlanKeyRotationRequest{
				Targets:          targets,
				DestinationKeyId: *destinationKeyID,
				DryRun:           *dryRun,
				Metadata:         metadata.mapValue(),
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "capacity-override":
		fs := newFlagSet("plan capacity-override")
		profileID := fs.String("capacity-profile-id", "", "capacity profile id")
		expiresAt := fs.String("expires-at", "", "RFC3339 expiration timestamp")
		reason := fs.String("reason", "", "operator reason")
		dryRun := fs.Bool("dry-run", false, "request a dry-run plan")
		metadata := metadataFlag{}
		fs.Var(&metadata, "metadata", "metadata key=value; may be repeated")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *profileID == "" {
			return nil, usageError("plan capacity-override requires --capacity-profile-id")
		}
		if *expiresAt == "" {
			return nil, usageError("plan capacity-override requires --expires-at")
		}
		parsedExpiresAt, err := parseTimestamp(*expiresAt)
		if err != nil {
			return nil, usageError("plan capacity-override --expires-at must be RFC3339")
		}
		if *reason == "" {
			return nil, usageError("plan capacity-override requires --reason")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.capacity.PlanCapacityOverride(ctx, &adminv1.PlanCapacityOverrideRequest{
				CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: *profileID},
				ExpiresAt:       timestamppb.New(parsedExpiresAt),
				Reason:          *reason,
				DryRun:          *dryRun,
				Metadata:        metadata.mapValue(),
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	case "recovery":
		opts, err := parseTargetPlan("plan recovery", args[1:], false)
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.dr.PlanRecovery(ctx, &adminv1.PlanRecoveryRequest{
				Targets:  opts.targets,
				DryRun:   opts.dryRun,
				Metadata: opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetPlan())
		}, nil
	default:
		return nil, usageError(fmt.Sprintf("unknown plan operation %q", args[0]))
	}
}

func buildStartAction(args []string) (action, error) {
	if len(args) == 0 {
		return nil, usageError("start operation is required")
	}
	opts, err := parseStart("start "+args[0], args[1:])
	if err != nil {
		return nil, err
	}
	switch args[0] {
	case "restore":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.restore.StartRestore(ctx, &adminv1.StartRestoreRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "prewarm":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.restore.StartPrewarm(ctx, &adminv1.StartPrewarmRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "repair":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.repair.StartRepair(ctx, &adminv1.StartRepairRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "scrub":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.repair.StartScrub(ctx, &adminv1.StartScrubRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "drain":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.member.StartDrain(ctx, &adminv1.StartDrainRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "tombstone":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.lifecycle.StartTombstone(ctx, &adminv1.StartTombstoneRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "key-rotation":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.key.StartKeyRotation(ctx, &adminv1.StartKeyRotationRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "capacity-override":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.capacity.StartCapacityOverride(ctx, &adminv1.StartCapacityOverrideRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "metadata-restore":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.dr.StartMetadataRestore(ctx, &adminv1.StartMetadataRestoreRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "copy-verify":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.dr.StartCopyVerify(ctx, &adminv1.StartCopyVerifyRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	case "dr-drill":
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.dr.StartDRDrill(ctx, &adminv1.StartDRDrillRequest{
				OperationPlanId: opts.planID,
				PlanHash:        opts.planHash,
				OperationId:     opts.operationID,
				Metadata:        opts.metadata,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	default:
		return nil, usageError(fmt.Sprintf("unknown start operation %q", args[0]))
	}
}

func buildStatusAction(args []string) (action, error) {
	fs := newFlagSet("status")
	operationID := fs.String("operation-id", "", "operation id")
	if err := parseExact(fs, args); err != nil {
		return nil, err
	}
	if *operationID == "" {
		return nil, usageError("status requires --operation-id")
	}
	return func(ctx context.Context, c clients, out io.Writer) error {
		resp, err := c.operations.GetOperation(ctx, &adminv1.GetOperationRequest{OperationId: *operationID})
		if err != nil {
			return err
		}
		return writeProto(out, resp.GetOperation())
	}, nil
}

func buildWatchAction(args []string) (action, error) {
	fs := newFlagSet("watch")
	operationID := fs.String("operation-id", "", "operation id")
	afterSequence := fs.Uint64("after-sequence", 0, "last observed watch sequence")
	if err := parseExact(fs, args); err != nil {
		return nil, err
	}
	if *operationID == "" {
		return nil, usageError("watch requires --operation-id")
	}
	return func(ctx context.Context, c clients, out io.Writer) error {
		stream, err := c.operations.WatchOperation(ctx, &adminv1.WatchOperationRequest{
			OperationId:   *operationID,
			AfterSequence: optionalUint64(*afterSequence),
		})
		if err != nil {
			return err
		}
		for {
			resp, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			if err := writeProto(out, resp); err != nil {
				return err
			}
		}
	}, nil
}

func buildOperationsAction(args []string) (action, error) {
	if len(args) == 0 {
		return nil, usageError("operations command is required")
	}
	switch args[0] {
	case "status":
		return buildStatusAction(args[1:])
	case "watch":
		return buildWatchAction(args[1:])
	case "list":
		fs := newFlagSet("operations list")
		states := stateFlag{}
		operationType := fs.String("type", "", "operation type")
		pageSize := fs.Uint64("page-size", 0, "page size")
		pageToken := fs.String("page-token", "", "page token")
		fs.Var(&states, "state", "operation state; may be repeated")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		pageSizeValue, err := optionalPageSize(*pageSize, "operations list")
		if err != nil {
			return nil, err
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.operations.ListOperations(ctx, &adminv1.ListOperationsRequest{
				States:        states,
				OperationType: optionalString(*operationType),
				PageSize:      pageSizeValue.value,
				PageToken:     *pageToken,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp)
		}, nil
	case "cancel":
		fs := newFlagSet("operations cancel")
		operationID := fs.String("operation-id", "", "operation id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *operationID == "" {
			return nil, usageError("operations cancel requires --operation-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.operations.CancelOperation(ctx, &adminv1.CancelOperationRequest{OperationId: *operationID})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetOperation())
		}, nil
	default:
		return nil, usageError(fmt.Sprintf("unknown operations command %q", args[0]))
	}
}

func buildMemberAction(args []string) (action, error) {
	if len(args) == 0 {
		return nil, usageError("member command is required")
	}
	switch args[0] {
	case "cordon":
		fs := newFlagSet("member cordon")
		memberID := fs.String("member-id", "", "storage member id")
		reason := fs.String("reason", "", "operator reason")
		operationID := fs.String("operation-id", "", "operation id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *memberID == "" {
			return nil, usageError("member cordon requires --member-id")
		}
		if *reason == "" {
			return nil, usageError("member cordon requires --reason")
		}
		if *operationID == "" {
			return nil, usageError("member cordon requires --operation-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.member.CordonMember(ctx, &adminv1.CordonMemberRequest{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: *memberID},
				Reason:        *reason,
				OperationId:   *operationID,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetStorageMember())
		}, nil
	case "uncordon":
		fs := newFlagSet("member uncordon")
		memberID := fs.String("member-id", "", "storage member id")
		operationID := fs.String("operation-id", "", "operation id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *memberID == "" {
			return nil, usageError("member uncordon requires --member-id")
		}
		if *operationID == "" {
			return nil, usageError("member uncordon requires --operation-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.member.UncordonMember(ctx, &adminv1.UncordonMemberRequest{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: *memberID},
				OperationId:   *operationID,
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetStorageMember())
		}, nil
	case "eviction-safety":
		fs := newFlagSet("member eviction-safety")
		memberID := fs.String("member-id", "", "storage member id")
		if err := parseExact(fs, args[1:]); err != nil {
			return nil, err
		}
		if *memberID == "" {
			return nil, usageError("member eviction-safety requires --member-id")
		}
		return func(ctx context.Context, c clients, out io.Writer) error {
			resp, err := c.member.GetEvictionSafety(ctx, &adminv1.GetEvictionSafetyRequest{
				StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: *memberID},
			})
			if err != nil {
				return err
			}
			return writeProto(out, resp.GetSafety())
		}, nil
	default:
		return nil, usageError(fmt.Sprintf("unknown member command %q", args[0]))
	}
}

type targetPlanOptions struct {
	targets  []*adminv1.Target
	dryRun   bool
	pinUntil *timestamppb.Timestamp
	metadata map[string]string
}

func parseTargetPlan(name string, args []string, allowPinUntil bool) (targetPlanOptions, error) {
	fs := newFlagSet(name)
	targets := targetFlag{}
	metadata := metadataFlag{}
	dryRun := fs.Bool("dry-run", false, "request a dry-run plan")
	pinUntil := fs.String("pin-until", "", "RFC3339 prewarm pin timestamp")
	fs.Var(&targets, "target", "target kind:value; may be repeated")
	fs.Var(&metadata, "metadata", "metadata key=value; may be repeated")
	if err := parseExact(fs, args); err != nil {
		return targetPlanOptions{}, err
	}
	if len(targets) == 0 {
		return targetPlanOptions{}, usageError(name + " requires at least one --target")
	}
	if *pinUntil != "" && !allowPinUntil {
		return targetPlanOptions{}, usageError(name + " does not accept --pin-until")
	}
	var pinUntilProto *timestamppb.Timestamp
	if *pinUntil != "" {
		parsed, err := parseTimestamp(*pinUntil)
		if err != nil {
			return targetPlanOptions{}, usageError(name + " --pin-until must be RFC3339")
		}
		pinUntilProto = timestamppb.New(parsed)
	}
	return targetPlanOptions{
		targets:  targets,
		dryRun:   *dryRun,
		pinUntil: pinUntilProto,
		metadata: metadata.mapValue(),
	}, nil
}

type startOptions struct {
	planID      string
	planHash    string
	operationID string
	metadata    map[string]string
}

func parseStart(name string, args []string) (startOptions, error) {
	fs := newFlagSet(name)
	planID := fs.String("plan-id", "", "operation plan id")
	planHash := fs.String("plan-hash", "", "operation plan hash")
	operationID := fs.String("operation-id", "", "operation id")
	metadata := metadataFlag{}
	fs.Var(&metadata, "metadata", "metadata key=value; may be repeated")
	if err := parseExact(fs, args); err != nil {
		return startOptions{}, err
	}
	if *planID == "" {
		return startOptions{}, usageError(name + " requires --plan-id")
	}
	if *planHash == "" {
		return startOptions{}, usageError(name + " requires --plan-hash")
	}
	if *operationID == "" {
		return startOptions{}, usageError(name + " requires --operation-id")
	}
	return startOptions{
		planID:      *planID,
		planHash:    *planHash,
		operationID: *operationID,
		metadata:    metadata.mapValue(),
	}, nil
}

type docFlagValues struct {
	tenantID      *string
	transactionID *string
	documentName  *string
}

func documentFlags(fs *flag.FlagSet) docFlagValues {
	return docFlagValues{
		tenantID:      fs.String("tenant-id", "", "tenant id"),
		transactionID: fs.String("transaction-id", "", "transaction id"),
		documentName:  fs.String("document-name", "", "document name"),
	}
}

func (v docFlagValues) target() (*adminv1.DocumentTarget, error) {
	if *v.tenantID == "" {
		return nil, usageError("document target requires --tenant-id")
	}
	if *v.transactionID == "" {
		return nil, usageError("document target requires --transaction-id")
	}
	if *v.documentName == "" {
		return nil, usageError("document target requires --document-name")
	}
	return &adminv1.DocumentTarget{
		TenantId:      *v.tenantID,
		TransactionId: *v.transactionID,
		DocumentName:  *v.documentName,
	}, nil
}

type blockFlagValues struct {
	shardID *string
	blockID *string
}

func blockFlags(fs *flag.FlagSet) blockFlagValues {
	return blockFlagValues{
		shardID: fs.String("shard-id", "", "shard id"),
		blockID: fs.String("block-id", "", "block id"),
	}
}

func (v blockFlagValues) target() (*adminv1.BlockTarget, error) {
	if *v.shardID == "" {
		return nil, usageError("block target requires --shard-id")
	}
	if *v.blockID == "" {
		return nil, usageError("block target requires --block-id")
	}
	return &adminv1.BlockTarget{ShardId: *v.shardID, BlockId: *v.blockID}, nil
}

type targetFlag []*adminv1.Target

func (f *targetFlag) Set(value string) error {
	kind, spec, ok := strings.Cut(value, ":")
	if !ok || strings.TrimSpace(kind) == "" || spec == "" {
		return errors.New("target must be kind:value")
	}
	target, err := parseTarget(strings.TrimSpace(kind), spec)
	if err != nil {
		return err
	}
	*f = append(*f, target)
	return nil
}

func (f targetFlag) String() string {
	if len(f) == 0 {
		return ""
	}
	return fmt.Sprintf("%d target(s)", len(f))
}

func parseTarget(kind, spec string) (*adminv1.Target, error) {
	switch strings.ReplaceAll(strings.ToLower(kind), "-", "_") {
	case "document", "doc":
		parts := strings.SplitN(spec, "/", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return nil, errors.New("document target must be document:tenant_id/transaction_id/document_name")
		}
		return &adminv1.Target{Target: &adminv1.Target_Document{Document: &adminv1.DocumentTarget{
			TenantId:      parts[0],
			TransactionId: parts[1],
			DocumentName:  parts[2],
		}}}, nil
	case "transaction", "txn":
		parts := strings.SplitN(spec, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, errors.New("transaction target must be transaction:tenant_id/transaction_id")
		}
		return &adminv1.Target{Target: &adminv1.Target_Transaction{Transaction: &adminv1.TransactionTarget{
			TenantId:      parts[0],
			TransactionId: parts[1],
		}}}, nil
	case "block":
		parts := strings.SplitN(spec, "/", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, errors.New("block target must be block:shard_id/block_id")
		}
		return &adminv1.Target{Target: &adminv1.Target_Block{Block: &adminv1.BlockTarget{
			ShardId: parts[0],
			BlockId: parts[1],
		}}}, nil
	case "shard":
		if spec == "" {
			return nil, errors.New("shard target must be shard:shard_id")
		}
		return &adminv1.Target{Target: &adminv1.Target_Shard{Shard: &adminv1.ShardTarget{ShardId: spec}}}, nil
	case "storage_member", "member":
		if spec == "" {
			return nil, errors.New("member target must be member:storage_member_id")
		}
		return &adminv1.Target{Target: &adminv1.Target_StorageMember{StorageMember: &adminv1.StorageMemberTarget{StorageMemberId: spec}}}, nil
	case "snapshot":
		if spec == "" {
			return nil, errors.New("snapshot target must be snapshot:snapshot_id")
		}
		return &adminv1.Target{Target: &adminv1.Target_Snapshot{Snapshot: &adminv1.SnapshotTarget{SnapshotId: spec}}}, nil
	case "capacity_profile", "capacity-profile":
		if spec == "" {
			return nil, errors.New("capacity profile target must be capacity-profile:capacity_profile_id")
		}
		return &adminv1.Target{Target: &adminv1.Target_CapacityProfile{CapacityProfile: &adminv1.CapacityProfileTarget{CapacityProfileId: spec}}}, nil
	default:
		return nil, fmt.Errorf("unknown target kind %q", kind)
	}
}

type metadataFlag map[string]string

func (f *metadataFlag) Set(value string) error {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return errors.New("metadata must be key=value")
	}
	if *f == nil {
		*f = make(map[string]string)
	}
	(*f)[key] = val
	return nil
}

func (f metadataFlag) String() string {
	if len(f) == 0 {
		return ""
	}
	keys := make([]string, 0, len(f))
	for key := range f {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func (f metadataFlag) mapValue() map[string]string {
	if len(f) == 0 {
		return nil
	}
	out := make(map[string]string, len(f))
	for key, value := range f {
		out[key] = value
	}
	return out
}

type stateFlag []adminv1.OperationState

func (f *stateFlag) Set(value string) error {
	state, err := parseState(value)
	if err != nil {
		return err
	}
	*f = append(*f, state)
	return nil
}

func (f stateFlag) String() string {
	if len(f) == 0 {
		return ""
	}
	values := make([]string, 0, len(f))
	for _, state := range f {
		values = append(values, state.String())
	}
	return strings.Join(values, ",")
}

func parseState(value string) (adminv1.OperationState, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), "-", "_"))
	if normalized == "" {
		return adminv1.OperationState_OPERATION_STATE_UNSPECIFIED, errors.New("state is required")
	}
	if !strings.HasPrefix(normalized, "OPERATION_STATE_") {
		normalized = "OPERATION_STATE_" + normalized
	}
	raw, ok := adminv1.OperationState_value[normalized]
	if !ok {
		return adminv1.OperationState_OPERATION_STATE_UNSPECIFIED, fmt.Errorf("unknown operation state %q", value)
	}
	return adminv1.OperationState(raw), nil
}

func parseTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseExact(fs *flag.FlagSet, args []string) error {
	if err := fs.Parse(args); err != nil {
		return usageError(err.Error())
	}
	if fs.NArg() > 0 {
		return usageError(fmt.Sprintf("%s received unexpected argument %q", fs.Name(), fs.Arg(0)))
	}
	return nil
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

type pageSizeValue struct {
	value *uint32
}

func optionalPageSize(value uint64, command string) (pageSizeValue, error) {
	if value == 0 {
		return pageSizeValue{}, nil
	}
	if value > uint64(^uint32(0)) {
		return pageSizeValue{}, usageError(fmt.Sprintf("%s --page-size must be <= %d", command, uint64(^uint32(0))))
	}
	converted := uint32(value)
	return pageSizeValue{value: &converted}, nil
}

func optionalUint64(value uint64) *uint64 {
	if value == 0 {
		return nil
	}
	return &value
}

func writeProto(out io.Writer, msg proto.Message) error {
	if msg == nil {
		return nil
	}
	encoded, err := jsonFormat.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(encoded))
	return err
}

type failurePayload struct {
	Code       string             `json:"code"`
	Message    string             `json:"message"`
	Violations []failureViolation `json:"violations,omitempty"`
}

type failureViolation struct {
	Field       string `json:"field"`
	Reason      string `json:"reason"`
	Description string `json:"description"`
}

func writeFailure(out io.Writer, err error) {
	payload := failureFromError(err)
	encoded, marshalErr := json.MarshalIndent(payload, "", "  ")
	if marshalErr != nil {
		_, _ = fmt.Fprintf(out, "error: %v\n", err)
		return
	}
	_, _ = fmt.Fprintln(out, string(encoded))
}

func failureFromError(err error) failurePayload {
	var usage usageError
	if errors.As(err, &usage) {
		return failurePayload{Code: "USAGE", Message: usage.Error()}
	}
	st, ok := status.FromError(err)
	if !ok {
		return failurePayload{Code: "LOCAL_ERROR", Message: err.Error()}
	}
	payload := failurePayload{
		Code:    st.Code().String(),
		Message: st.Message(),
	}
	if st.Code() == codes.OK {
		return payload
	}
	for _, detail := range st.Details() {
		badRequest, ok := detail.(*errdetails.BadRequest)
		if !ok {
			continue
		}
		for _, violation := range badRequest.GetFieldViolations() {
			payload.Violations = append(payload.Violations, failureViolation{
				Field:       violation.GetField(),
				Reason:      violation.GetReason(),
				Description: violation.GetDescription(),
			})
		}
	}
	return payload
}
