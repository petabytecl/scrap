package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
	"github.com/petabytecl/scrap/internal/security"
)

func scrapAddr() string {
	if addr := os.Getenv("SCRAP_E2E_ADDR"); addr != "" {
		return addr
	}
	return "localhost:9090"
}

func connect(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(scrapAddr(), grpc.WithTransportCredentials(e2eGRPCCredentials(t)))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return scrapv1.NewDocumentServiceClient(conn)
}

func e2eGRPCCredentials(t *testing.T) credentials.TransportCredentials {
	t.Helper()
	if !e2eTLSConfigured() {
		return insecure.NewCredentials()
	}
	tlsConfig, err := security.BuildMTLSClientConfig("SCRAP_E2E_TLS", e2eTLSFiles())
	if err != nil {
		t.Fatalf("build E2E TLS credentials: %v", err)
	}
	return credentials.NewTLS(tlsConfig)
}

func e2eTLSConfigured() bool {
	files := e2eTLSFiles()
	return files.CertPath != "" || files.KeyPath != "" || files.RootCAPath != "" || files.ServerName != ""
}

func e2eTLSFiles() security.ClientTLSFiles {
	return security.ClientTLSFiles{
		CertPath:   envOrDefault("SCRAP_E2E_TLS_CERT", os.Getenv("SCRAP_TLS_SCRAPCTL_CERT")),
		KeyPath:    envOrDefault("SCRAP_E2E_TLS_KEY", os.Getenv("SCRAP_TLS_SCRAPCTL_KEY")),
		RootCAPath: envOrDefault("SCRAP_E2E_TLS_CA", os.Getenv("SCRAP_TLS_SCRAPCTL_CLIENT_CA")),
		ServerName: envOrDefault("SCRAP_E2E_TLS_SERVER_NAME", os.Getenv("SCRAP_TLS_SCRAPCTL_SERVER_NAME")),
	}
}

func writeDocE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName, contentType string, data []byte) *scrapv1.WriteDocumentResponse {
	t.Helper()
	resp, err := tryWriteDocE2E(t, client, txID, docName, contentType, data)
	if err != nil {
		t.Fatalf("WriteDocument failed after retries: %v", err)
	}
	return resp
}

//nolint:gocognit,cyclop // test helper with exhaustive retry, streaming, and leader redirect logic
func tryWriteDocE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName, contentType string, data []byte) (*scrapv1.WriteDocumentResponse, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var lastErr error
	activeClient := client

	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			break
		}
		stream, err := activeClient.WriteDocument(ctx)
		if err != nil {
			lastErr = err
			waitBeforeRetry(ctx, attempt)
			continue
		}

		if err := stream.Send(&scrapv1.WriteDocumentRequest{
			Part: &scrapv1.WriteDocumentRequest_Init{
				Init: &scrapv1.WriteDocumentInit{
					TransactionId: txID,
					DocumentName:  docName,
					ContentType:   contentType,
				},
			},
		}); err != nil {
			lastErr = err
			continue
		}

		chunkErr := false
		for i := 0; i < len(data); i += 4096 {
			end := min(i+4096, len(data))
			if err := stream.Send(&scrapv1.WriteDocumentRequest{
				Part: &scrapv1.WriteDocumentRequest_ChunkData{
					ChunkData: data[i:end],
				},
			}); err != nil {
				lastErr = err
				chunkErr = true
				break
			}
		}
		if chunkErr {
			continue
		}

		var resp *scrapv1.WriteDocumentResponse
		resp, lastErr = stream.CloseAndRecv()
		if lastErr == nil {
			return resp, nil
		}

		st, ok := status.FromError(lastErr)
		if ok {
			switch st.Code() {
			case codes.Unavailable:
				if leaderClient, redirected := clientForLeaderHint(t, lastErr); redirected {
					activeClient = leaderClient
				}
				waitBeforeRetry(ctx, attempt)
				continue
			case codes.ResourceExhausted:
				if !isUploadPressureError(lastErr) {
					waitBeforeRetry(ctx, attempt)
					continue
				}
			case codes.DeadlineExceeded, codes.Canceled:
				waitBeforeRetry(ctx, attempt)
				continue
			default:
			}
		}
		break
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("write document %s/%s did not complete", txID, docName)
	}
	return nil, lastErr
}

func retryableE2EStatus(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
		return true
	case codes.ResourceExhausted:
		return !isUploadPressureError(err)
	default:
		return false
	}
}

func waitBeforeRetry(ctx context.Context, attempt int) {
	delay := time.Duration(attempt+1) * 200 * time.Millisecond
	if delay > time.Second {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func TestE2EWriteReadHead(t *testing.T) {
	if os.Getenv("SCRAP_E2E") == "" {
		t.Skip("set SCRAP_E2E=1 to run E2E tests")
	}

	client := connect(t)
	content := bytes.Repeat([]byte("e2e test data "), 100)
	txID := uniqueName("tx-e2e-write-read")

	resp := writeDocE2E(t, client, txID, "invoice.xml", "application/xml", content)
	if len(resp.GetSha256Checksum()) != 64 {
		t.Fatalf("checksum should be 64 hex chars, got %d", len(resp.GetSha256Checksum()))
	}

	headResp := headDocE2E(t, client, txID, "invoice.xml")
	if headResp.GetSize() != int64(len(content)) {
		t.Fatalf("HeadDocument size: got %d, want %d", headResp.GetSize(), len(content))
	}
	if headResp.GetContentType() != "application/xml" {
		t.Fatalf("HeadDocument content type: got %q", headResp.GetContentType())
	}

	readBack := readDocE2E(t, client, txID, "invoice.xml")
	if !bytes.Equal(readBack, content) {
		t.Fatalf("content mismatch: got %d bytes", len(readBack))
	}
}

func TestE2ELeaderFailover(t *testing.T) {
	if os.Getenv("SCRAP_E2E") == "" {
		t.Skip("set SCRAP_E2E=1 to run E2E tests")
	}

	client := connect(t)
	content := []byte("pre-failover data")
	txID := uniqueName("tx-e2e-failover")

	writeDocE2E(t, client, txID, "doc.xml", "text/xml", content)

	leader := findLeaderPod(t, txID, "doc.xml")
	deletePodAndWaitReady(t, leader)
	waitForCellWriteQuorum(t)

	headResp := headDocE2E(t, client, txID, "doc.xml")
	if headResp.GetSize() != int64(len(content)) {
		t.Fatalf("Size: got %d", headResp.GetSize())
	}
	readBack := readDocE2E(t, client, txID, "doc.xml")
	if !bytes.Equal(readBack, content) {
		t.Fatalf("read after leader failover: got %d bytes, want %d", len(readBack), len(content))
	}
}

func waitForCellWriteQuorum(t *testing.T) {
	t.Helper()
	client := connect(t)
	txID := uniqueName("tx-e2e-quorum")
	if _, err := tryWriteDocE2E(t, client, txID, "ready.xml", "text/xml", []byte("ready")); err != nil {
		t.Fatalf("cell did not accept canary write after leader replacement: %v", err)
	}
}
