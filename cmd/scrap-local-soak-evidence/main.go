package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/closeutil"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/localsoak"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrap-local-soak-evidence", flag.ContinueOnError)
	fs.SetOutput(stderr)
	outPath := fs.String("out", localsoak.DefaultReportPath, "output report path, or - for stdout")
	releaseSHA := fs.String("release-sha", "", "release SHA recorded in evidence")
	dirtyTree := fs.String("dirty-tree", "", "dirty tree state recorded in evidence")
	profileID := fs.String("profile-id", localsoak.DefaultProfileID, "release profile id")
	environmentID := fs.String("environment-id", localsoak.DefaultEnvironmentID, "local environment id")
	runnerID := fs.String("runner-id", localsoak.DefaultRunnerID, "runner/profile identifier")
	imageIdentity := fs.String("image-identity", localsoak.DefaultImageIdentity, "image identity recorded in evidence")
	publicAddr := fs.String("public-addr", localsoak.DefaultPublicAddress, "public gRPC address")
	adminAddr := fs.String("admin-addr", localsoak.DefaultAdminAddress, "admin gRPC address")
	publicWorkload := fs.String("public-workload-identity", localsoak.DefaultPublicWorkloadIdentity, "workload identity for public RPCs")
	adminWorkload := fs.String("admin-workload-identity", localsoak.DefaultAdminWorkloadIdentity, "workload identity for admin RPCs")
	capacityReport := fs.String("capacity-sample-report", localsoak.DefaultCapacitySampleReportPath, "capacity sample advisory report path")
	samples := fs.Int("samples", localsoak.DefaultSampleCount, "bounded document sample count")
	duration := fs.Duration("duration", localsoak.DefaultDuration, "bounded campaign timeout")
	documentSizes := uint64ListFlag{}
	fs.Var(&documentSizes, "document-size", "document size in bytes; may be repeated")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "unexpected arguments: %s\n", strings.Join(fs.Args(), " "))
		return 2
	}

	opts := localsoak.Options{
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
		SampleCount:              *samples,
		DocumentSizesBytes:       documentSizes.values,
		Duration:                 *duration,
	}
	if err := runEvidence(context.Background(), opts, *outPath, stdout); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func runEvidence(ctx context.Context, opts localsoak.Options, outPath string, stdout io.Writer) error {
	validated, err := localsoak.ValidateOptions(opts)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, validated.Duration+5*time.Second)
	defer cancel()

	publicConn, err := dial(ctx, validated.PublicAddress, validated.PublicWorkloadIdentity)
	if err != nil {
		return fmt.Errorf("dial public gRPC: %w", err)
	}
	defer closeutil.Ignore(publicConn)
	adminConn, err := dial(ctx, validated.AdminAddress, validated.AdminWorkloadIdentity)
	if err != nil {
		return fmt.Errorf("dial admin gRPC: %w", err)
	}
	defer closeutil.Ignore(adminConn)

	report, err := localsoak.Run(ctx, validated, localsoak.Clients{
		Documents:  scrapv1.NewDocumentServiceClient(publicConn),
		Inspect:    adminv1.NewInspectServiceClient(adminConn),
		Operations: adminv1.NewOperationServiceClient(adminConn),
		Repair:     adminv1.NewRepairServiceClient(adminConn),
	})
	if err != nil {
		return err
	}
	if err := writeReport(outPath, stdout, report); err != nil {
		return err
	}
	if report.Status != localsoak.StatusPassed {
		return fmt.Errorf("local soak evidence status %s; blocking issue or approved exception is required", report.Status)
	}
	return nil
}

func dial(_ context.Context, address, workloadIdentity string) (*grpc.ClientConn, error) {
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

func writeReport(path string, stdout io.Writer, report localsoak.Report) error {
	writer := stdout
	var file *os.File
	if path != "-" {
		var err error
		// #nosec G304 -- the report path is an operator-provided local evidence output path.
		file, err = os.Create(path)
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

type uint64ListFlag struct {
	values []uint64
}

func (f *uint64ListFlag) String() string {
	if f == nil {
		return ""
	}
	parts := make([]string, 0, len(f.values))
	for _, value := range f.values {
		parts = append(parts, strconv.FormatUint(value, 10))
	}
	return strings.Join(parts, ",")
}

func (f *uint64ListFlag) Set(value string) error {
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return err
	}
	f.values = append(f.values, parsed)
	return nil
}
