package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/closeutil"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/localdrill"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrap-local-dr-drill-evidence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", localdrill.DefaultReportPath, "output report path, or - for stdout")
	releaseSHA := fs.String("release-sha", "", "release SHA recorded in evidence")
	dirtyTree := fs.String("dirty-tree", "", "dirty tree state recorded in evidence")
	profileID := fs.String("profile-id", localdrill.DefaultProfileID, "release profile id")
	environmentID := fs.String("environment-id", localdrill.DefaultEnvironmentID, "local environment id")
	runnerID := fs.String("runner-id", localdrill.DefaultRunnerID, "runner/profile identifier")
	imageIdentity := fs.String("image-identity", localdrill.DefaultImageIdentity, "image identity recorded in evidence")
	publicAddr := fs.String("public-addr", localdrill.DefaultPublicAddress, "public gRPC address")
	adminAddr := fs.String("admin-addr", localdrill.DefaultAdminAddress, "admin gRPC address")
	publicWorkload := fs.String("public-workload-identity", localdrill.DefaultPublicWorkloadIdentity, "workload identity for public RPCs")
	adminWorkload := fs.String("admin-workload-identity", localdrill.DefaultAdminWorkloadIdentity, "workload identity for admin RPCs")
	capacityReport := fs.String("capacity-sample-report", localdrill.DefaultCapacityReportPath, "capacity sample advisory report path")
	openbaoReport := fs.String("openbao-smoke-report", localdrill.DefaultOpenBaoReportPath, "OpenBao smoke evidence report path")
	operatorOwner := fs.String("operator-owner", localdrill.DefaultOperatorOwner, "operator approval owner recorded in evidence")
	approvalState := fs.String("approval-state", "approved-local-release-artifact-rehearsal", "operator approval state recorded in evidence")
	drillID := fs.String("drill-id", "", "drill id recorded in evidence")
	snapshotTarget := fs.String("snapshot-target-id", "latest-restorable-checkpoint", "snapshot target id used in recovery plan")
	fixtureSize := fs.Uint64("fixture-size", localdrill.DefaultFixtureSizeBytes, "fixture document size in bytes")
	duration := fs.Duration("duration", localdrill.DefaultDuration, "bounded drill timeout")
	pollInterval := fs.Duration("poll-interval", localdrill.DefaultPollInterval, "operation status poll interval")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	opts := localdrill.Options{
		ReleaseSHA:               *releaseSHA,
		DirtyTree:                *dirtyTree,
		ProfileID:                *profileID,
		EnvironmentID:            *environmentID,
		RunnerID:                 *runnerID,
		ImageIdentity:            *imageIdentity,
		PublicAddress:            *publicAddr,
		AdminAddress:             *adminAddr,
		PublicWorkloadIdentity:   *publicWorkload,
		AdminWorkloadIdentity:    *adminWorkload,
		CapacitySampleReportPath: *capacityReport,
		OpenBaoSmokeReportPath:   *openbaoReport,
		OperatorOwner:            *operatorOwner,
		ApprovalState:            *approvalState,
		DrillID:                  *drillID,
		SnapshotTargetID:         *snapshotTarget,
		FixtureSizeBytes:         *fixtureSize,
		Duration:                 *duration,
		PollInterval:             *pollInterval,
	}
	if err := runEvidence(context.Background(), opts, *outPath, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runEvidence(ctx context.Context, opts localdrill.Options, outPath string, stdout io.Writer) error {
	validated, err := localdrill.ValidateOptions(opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, validated.Duration+10*time.Second)
	defer cancel()

	publicConn, err := dial(validated.PublicAddress, validated.PublicWorkloadIdentity)
	if err != nil {
		return fmt.Errorf("dial public gRPC: %w", err)
	}
	defer closeutil.Ignore(publicConn)
	adminConn, err := dial(validated.AdminAddress, validated.AdminWorkloadIdentity)
	if err != nil {
		return fmt.Errorf("dial admin gRPC: %w", err)
	}
	defer closeutil.Ignore(adminConn)

	dr := adminv1.NewDisasterRecoveryServiceClient(adminConn)
	report, runErr := localdrill.Run(ctx, validated, localdrill.Clients{
		FixtureWriter: localdrill.GRPCFixtureWriter{Client: scrapv1.NewDocumentServiceClient(publicConn)},
		DR:            dr,
		Operations:    adminv1.NewOperationServiceClient(adminConn),
	})
	if report.ReportKind != "" {
		if err := writeReport(outPath, stdout, report); err != nil {
			return err
		}
	}
	if runErr != nil {
		if errors.Is(runErr, localdrill.ErrDrillFailed) {
			return errors.New("local DR drill evidence failed; blocking issue or approved exception is required")
		}
		return runErr
	}
	if report.Status != localdrill.StatusPassed {
		return errors.New("local DR drill evidence did not pass; blocking issue or approved exception is required")
	}
	return nil
}

func dial(address, workloadIdentity string) (*grpc.ClientConn, error) {
	return grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(workloadUnaryClientInterceptor(workloadIdentity)),
		grpc.WithChainStreamInterceptor(workloadStreamClientInterceptor(workloadIdentity)),
	)
}

func workloadUnaryClientInterceptor(workloadIdentity string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req any,
		reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		return invoker(workloadContext(ctx, workloadIdentity), method, req, reply, cc, opts...)
	}
}

func workloadStreamClientInterceptor(workloadIdentity string) grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		return streamer(workloadContext(ctx, workloadIdentity), desc, cc, method, opts...)
	}
}

func workloadContext(ctx context.Context, workloadIdentity string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, authz.WorkloadIdentityMetadataKey, workloadIdentity)
}

func writeReport(path string, stdout io.Writer, report localdrill.Report) error {
	writer := stdout
	var file *os.File
	if path != "-" {
		var err error
		file, err = os.Create(path) // #nosec G304 -- operator-provided evidence output path.
		if err != nil {
			return fmt.Errorf("create report: %w", err)
		}
		defer closeutil.Ignore(file)
		writer = file
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
