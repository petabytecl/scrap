package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/authz"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
)

type options struct {
	publicAddress          string
	adminAddress           string
	publicWorkloadIdentity string
	adminWorkloadIdentity  string
	tenantID               string
	expectedReplicaCount   uint64
	expectFailure          bool
	timeout                time.Duration
}

type report struct {
	Mode                 string       `json:"mode"`
	TenantID             string       `json:"tenant_id"`
	TransactionID        string       `json:"transaction_id"`
	DocumentName         string       `json:"document_name"`
	ConfiguredTarget     uint64       `json:"configured_target_replicas"`
	ConfiguredQuorum     uint64       `json:"configured_quorum_replicas"`
	Write                *writeReport `json:"write,omitempty"`
	Block                *blockReport `json:"block,omitempty"`
	ObservedFailureCode  string       `json:"observed_failure_code,omitempty"`
	ObservedFailureError string       `json:"observed_failure_error,omitempty"`
}

type writeReport struct {
	DesiredReplicaCount  uint32 `json:"desired_replica_count"`
	AchievedReplicaCount uint32 `json:"achieved_replica_count"`
	RepairRequired       bool   `json:"repair_required"`
}

type blockReport struct {
	BlockID         string   `json:"block_id"`
	ReplicaMemberID []string `json:"replica_member_ids"`
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout *os.File) error {
	opts, err := parseOptions(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, opts.timeout)
	defer cancel()

	doc := smokeDocument(opts)
	body := []byte("scrap local production-like replicated write smoke\n")
	writeResp, writeErr := writeDocument(ctx, opts, doc, body)
	if opts.expectFailure {
		return writeFailureReport(stdout, opts, doc, writeErr)
	}
	if writeErr != nil {
		return writeErr
	}
	if uint64(writeResp.GetDesiredReplicaCount()) != opts.expectedReplicaCount {
		return fmt.Errorf("desired replica count = %d, want %d", writeResp.GetDesiredReplicaCount(), opts.expectedReplicaCount)
	}
	if uint64(writeResp.GetAchievedReplicaCount()) != opts.expectedReplicaCount {
		return fmt.Errorf("achieved replica count = %d, want %d", writeResp.GetAchievedReplicaCount(), opts.expectedReplicaCount)
	}
	if writeResp.GetRepairRequired() {
		return errors.New("write reported repair_required=true")
	}

	block, err := inspectWrittenBlock(ctx, opts, doc)
	if err != nil {
		return err
	}
	replicaMembers := append([]string(nil), block.GetReplicaMemberIds()...)
	sort.Strings(replicaMembers)
	if replicaMemberCount(replicaMembers) != opts.expectedReplicaCount {
		return fmt.Errorf("block replica member count = %d, want %d (%v)", len(replicaMembers), opts.expectedReplicaCount, replicaMembers)
	}
	return writeJSON(stdout, report{
		Mode:                "replicated_ack_success",
		TenantID:            doc.GetTenantId(),
		TransactionID:       doc.GetTransactionId(),
		DocumentName:        doc.GetDocumentName(),
		ConfiguredTarget:    opts.expectedReplicaCount,
		ConfiguredQuorum:    opts.expectedReplicaCount,
		Write:               writeReportFromProto(writeResp),
		Block:               blockReportFromProto(block, replicaMembers),
		ObservedFailureCode: "",
	})
}

func parseOptions(args []string) (options, error) {
	opts := options{}
	flags := flag.NewFlagSet("scrap-local-prod-smoke", flag.ContinueOnError)
	flags.StringVar(&opts.publicAddress, "public-addr", "127.0.0.1:18180", "public gRPC address for the metadata authority member")
	flags.StringVar(&opts.adminAddress, "admin-addr", "127.0.0.1:18181", "admin gRPC address for the metadata authority member")
	flags.StringVar(&opts.publicWorkloadIdentity, "public-workload-identity", "local-public-client", "public workload identity metadata")
	flags.StringVar(&opts.adminWorkloadIdentity, "admin-workload-identity", "local-operator", "admin workload identity metadata")
	flags.StringVar(&opts.tenantID, "tenant-id", "tenant-a", "tenant id used by the smoke write")
	flags.Uint64Var(&opts.expectedReplicaCount, "expected-replica-count", 3, "expected target and quorum replica count")
	flags.BoolVar(&opts.expectFailure, "expect-failure", false, "require the write to fail closed with codes.Unavailable")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "overall smoke timeout")
	if err := flags.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}
	if flags.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected argument %q", flags.Arg(0))
	}
	if opts.expectedReplicaCount == 0 {
		return options{}, errors.New("--expected-replica-count must be positive")
	}
	return opts, nil
}

func replicaMemberCount(members []string) uint64 {
	var count uint64
	for range members {
		count++
	}
	return count
}

func smokeDocument(opts options) *scrapv1.DocumentIdentity {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	return &scrapv1.DocumentIdentity{
		TenantId:      opts.tenantID,
		TransactionId: "local-prod-smoke-" + stamp,
		DocumentName:  "replicated-ack-" + stamp + ".txt",
	}
}

func writeDocument(ctx context.Context, opts options, doc *scrapv1.DocumentIdentity, body []byte) (*scrapv1.WriteDocumentResponse, error) {
	conn, err := grpc.NewClient(opts.publicAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	callCtx := metadata.AppendToOutgoingContext(ctx, authz.WorkloadIdentityMetadataKey, opts.publicWorkloadIdentity)
	stream, err := scrapv1.NewDocumentServiceClient(conn).WriteDocument(callCtx)
	if err != nil {
		return nil, err
	}
	bodySHA := sha256.Sum256(body)
	bodyLength := uint64(len(body))
	if err := stream.Send(&scrapv1.WriteDocumentRequest{Message: &scrapv1.WriteDocumentRequest_Init{Init: &scrapv1.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    scrapv1.DocumentClass_DOCUMENT_CLASS_PERMANENT,
		PriorityClass:    scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL,
		ExpectedLength:   &bodyLength,
		ExpectedSha256:   bodySHA[:],
		CreatedByService: "local-prod-smoke",
	}}}); err != nil {
		return nil, err
	}
	if err := stream.Send(&scrapv1.WriteDocumentRequest{Message: &scrapv1.WriteDocumentRequest_Chunk{Chunk: &scrapv1.WriteDocumentChunk{Data: body}}}); err != nil {
		return nil, err
	}
	return stream.CloseAndRecv()
}

func writeFailureReport(stdout *os.File, opts options, doc *scrapv1.DocumentIdentity, writeErr error) error {
	if writeErr == nil {
		return errors.New("write succeeded, want fail-closed Unavailable error")
	}
	if status.Code(writeErr) != codes.Unavailable {
		return fmt.Errorf("write failure code = %s, want %s: %w", status.Code(writeErr), codes.Unavailable, writeErr)
	}
	return writeJSON(stdout, report{
		Mode:                 "replicated_ack_fail_closed",
		TenantID:             doc.GetTenantId(),
		TransactionID:        doc.GetTransactionId(),
		DocumentName:         doc.GetDocumentName(),
		ConfiguredTarget:     opts.expectedReplicaCount,
		ConfiguredQuorum:     opts.expectedReplicaCount,
		ObservedFailureCode:  status.Code(writeErr).String(),
		ObservedFailureError: writeErr.Error(),
	})
}

func inspectWrittenBlock(ctx context.Context, opts options, doc *scrapv1.DocumentIdentity) (*adminv1.Block, error) {
	conn, err := grpc.NewClient(opts.adminAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	callCtx := metadata.AppendToOutgoingContext(ctx, authz.WorkloadIdentityMetadataKey, opts.adminWorkloadIdentity)
	inspect := adminv1.NewInspectServiceClient(conn)
	docResp, err := inspect.GetDocument(callCtx, &adminv1.GetDocumentRequest{Document: &adminv1.DocumentTarget{
		TenantId:      doc.GetTenantId(),
		TransactionId: doc.GetTransactionId(),
		DocumentName:  doc.GetDocumentName(),
	}})
	if err != nil {
		return nil, err
	}
	blockIDs := docResp.GetDocument().GetBlockIds()
	if len(blockIDs) == 0 {
		return nil, errors.New("inspect document returned no block ids")
	}
	blockResp, err := inspect.GetBlock(callCtx, &adminv1.GetBlockRequest{Block: &adminv1.BlockTarget{
		ShardId: "local",
		BlockId: blockIDs[0],
	}})
	if err != nil {
		return nil, err
	}
	return blockResp.GetBlock(), nil
}

func writeReportFromProto(resp *scrapv1.WriteDocumentResponse) *writeReport {
	return &writeReport{
		DesiredReplicaCount:  resp.GetDesiredReplicaCount(),
		AchievedReplicaCount: resp.GetAchievedReplicaCount(),
		RepairRequired:       resp.GetRepairRequired(),
	}
}

func blockReportFromProto(block *adminv1.Block, replicaMembers []string) *blockReport {
	return &blockReport{
		BlockID:         block.GetBlockId(),
		ReplicaMemberID: replicaMembers,
	}
}

func writeJSON(stdout *os.File, value report) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
