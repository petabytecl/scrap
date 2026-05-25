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
	"github.com/petabytecl/scrap/internal/localsoak"
)

func TestRunLocalSoakRejectsUnexpectedArguments(t *testing.T) {
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

func TestRunLocalSoakRejectsMissingPrerequisiteReportBeforeDial(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := run([]string{"-out", "-", "-samples", "0"}, &stdout, &stderr)
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

func TestLocalSoakWriteReportWritesStdoutAndFile(t *testing.T) {
	report := localsoak.Report{
		ReportKind: localsoak.ReportKind,
		Status:     localsoak.StatusPassed,
		Release:    localsoak.ReleaseIdentity{ReleaseSHA: "abc123"},
	}
	var stdout bytes.Buffer
	if err := writeReport("-", &stdout, report); err != nil {
		t.Fatalf("write stdout report: %v", err)
	}
	decoded := decodeLocalSoakReport(t, stdout.Bytes())
	if decoded.Release.ReleaseSHA != "abc123" {
		t.Fatalf("stdout report release SHA = %q", decoded.Release.ReleaseSHA)
	}

	outPath := filepath.Join(t.TempDir(), "soak.json")
	if err := writeReport(outPath, &stdout, report); err != nil {
		t.Fatalf("write file report: %v", err)
	}
	data, err := os.ReadFile(outPath) // #nosec G304 -- test reads report path under t.TempDir.
	if err != nil {
		t.Fatalf("read file report: %v", err)
	}
	decoded = decodeLocalSoakReport(t, data)
	if decoded.ReportKind != localsoak.ReportKind {
		t.Fatalf("file report kind = %q", decoded.ReportKind)
	}
}

func TestLocalSoakWorkloadInterceptorsAttachIdentity(t *testing.T) {
	interceptor := workloadUnaryClientInterceptor("workload-a")
	err := interceptor(context.Background(), "/service/Method", nil, nil, nil, func(ctx context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		values := metadata.ValueFromIncomingContext(ctx, authz.WorkloadIdentityMetadataKey)
		if len(values) != 0 {
			t.Fatalf("incoming metadata = %#v, want outgoing metadata only", values)
		}
		outgoing, ok := metadata.FromOutgoingContext(ctx)
		if !ok {
			t.Fatal("missing outgoing metadata")
		}
		if got := outgoing.Get(authz.WorkloadIdentityMetadataKey); len(got) != 1 || got[0] != "workload-a" {
			t.Fatalf("workload metadata = %#v, want workload-a", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestUint64ListFlagParsesAndFormatsValues(t *testing.T) {
	var flag uint64ListFlag
	if got := flag.String(); got != "" {
		t.Fatalf("empty flag string = %q, want empty", got)
	}
	if err := flag.Set("7"); err != nil {
		t.Fatalf("set first value: %v", err)
	}
	if err := flag.Set("11"); err != nil {
		t.Fatalf("set second value: %v", err)
	}
	if got := flag.String(); got != "7,11" {
		t.Fatalf("flag string = %q, want 7,11", got)
	}
	if err := flag.Set("bad"); err == nil {
		t.Fatal("set bad value succeeded, want error")
	}
}

func decodeLocalSoakReport(t *testing.T, data []byte) localsoak.Report {
	t.Helper()
	var report localsoak.Report
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, string(data))
	}
	return report
}
