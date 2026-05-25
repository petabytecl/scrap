package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/petabytecl/scrap/internal/authz"
	"github.com/petabytecl/scrap/internal/localdrill"
)

func TestRunLocalDRDrillRejectsUnexpectedArguments(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"unexpected"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("run exit = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("stderr = %q, want unexpected arguments", stderr.String())
	}
}

func TestRunLocalDRDrillRejectsMissingPrerequisiteReportBeforeDial(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-out", "-", "-fixture-size", "0"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("run exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "open capacity sample report") {
		t.Fatalf("stderr = %q, want prerequisite report error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestLocalDRDrillWriteReportWritesStdoutAndFile(t *testing.T) {
	report := localdrill.Report{
		ReportKind: localdrill.ReportKind,
		Status:     localdrill.StatusPassed,
		Release:    localdrill.ReleaseIdentity{ReleaseSHA: "abc123"},
	}
	var stdout bytes.Buffer
	if err := writeReport("-", &stdout, report); err != nil {
		t.Fatalf("write stdout report: %v", err)
	}
	decoded := decodeLocalDRDrillReport(t, stdout.Bytes())
	if decoded.Release.ReleaseSHA != "abc123" {
		t.Fatalf("stdout report release SHA = %q", decoded.Release.ReleaseSHA)
	}

	outPath := filepath.Join(t.TempDir(), "drill.json")
	if err := writeReport(outPath, &stdout, report); err != nil {
		t.Fatalf("write file report: %v", err)
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- test reads report path under t.TempDir.
	if err != nil {
		t.Fatalf("read file report: %v", err)
	}
	decoded = decodeLocalDRDrillReport(t, data)
	if decoded.ReportKind != localdrill.ReportKind {
		t.Fatalf("file report kind = %q", decoded.ReportKind)
	}
}

func TestLocalDRDrillWorkloadInterceptorsAttachIdentity(t *testing.T) {
	interceptor := workloadUnaryClientInterceptor("operator-a")
	err := interceptor(context.Background(), "/service/Method", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		outgoing, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("missing outgoing metadata")
		}
		if got := outgoing.Get(authz.WorkloadIdentityMetadataKey); len(got) != 1 || got[0] != "operator-a" {
			t.Fatalf("workload metadata = %#v, want operator-a", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func decodeLocalDRDrillReport(t *testing.T, data []byte) localdrill.Report {
	t.Helper()
	var report localdrill.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(data))
	}
	return report
}
