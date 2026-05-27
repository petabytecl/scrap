package e2e_test

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func scrapAddr() string {
	if addr := os.Getenv("SCRAP_E2E_ADDR"); addr != "" {
		return addr
	}
	return "localhost:9090"
}

func connect(t *testing.T) scrapv1.DocumentServiceClient {
	t.Helper()
	conn, err := grpc.NewClient(scrapAddr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return scrapv1.NewDocumentServiceClient(conn)
}

//nolint:gocognit,cyclop // test helper with exhaustive retry, streaming, and leader redirect logic
func writeDocE2E(t *testing.T, client scrapv1.DocumentServiceClient, txID, docName, contentType string, data []byte) *scrapv1.WriteDocumentResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var lastErr error
	activeClient := client

	for attempt := range 10 {
		stream, err := activeClient.WriteDocument(ctx)
		if err != nil {
			lastErr = err
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
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
			return resp
		}

		st, ok := status.FromError(lastErr)
		if ok && st.Code() == codes.Unavailable {
			if leaderClient, redirected := clientForLeaderHint(t, lastErr); redirected {
				activeClient = leaderClient
			}
			time.Sleep(time.Duration(attempt+1) * 200 * time.Millisecond)
			continue
		}
		break
	}
	t.Fatalf("WriteDocument failed after retries: %v", lastErr)
	return nil
}

func TestE2EWriteAndRead(t *testing.T) {
	if os.Getenv("SCRAP_E2E") == "" {
		t.Skip("set SCRAP_E2E=1 to run E2E tests")
	}

	client := connect(t)
	content := bytes.Repeat([]byte("e2e test data "), 100)

	resp := writeDocE2E(t, client, "tx-e2e-001", "invoice.xml", "application/xml", content)
	if len(resp.GetSha256Checksum()) != 64 {
		t.Fatalf("checksum should be 64 hex chars, got %d", len(resp.GetSha256Checksum()))
	}

	readBack := readDocE2E(t, client, "tx-e2e-001", "invoice.xml")
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

	writeDocE2E(t, client, "tx-e2e-failover", "doc.xml", "text/xml", content)

	t.Log("Document written. To test failover: delete the leader pod, then verify read.")
	t.Log("kubectl -n scrap delete pod <leader-pod>")

	headResp := headDocE2E(t, client, "tx-e2e-failover", "doc.xml")
	if headResp.GetSize() != int64(len(content)) {
		t.Fatalf("Size: got %d", headResp.GetSize())
	}
}
