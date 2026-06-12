package store_test

import (
	"testing"

	"google.golang.org/protobuf/proto"

	scrapv1 "github.com/petabytecl/scrap/gen/go/scrap/v1"
)

func TestDocumentMetadataScanStatusRoundTrip(t *testing.T) {
	head := &scrapv1.HeadDocumentResponse{
		Name:       "doc.xml",
		ScanStatus: scrapv1.ScanStatus_SCAN_STATUS_QUARANTINED,
	}
	decodedHead := &scrapv1.HeadDocumentResponse{}
	roundTripProto(t, head, decodedHead)
	if decodedHead.GetScanStatus() != scrapv1.ScanStatus_SCAN_STATUS_QUARANTINED {
		t.Fatalf("HeadDocument scan_status = %v, want QUARANTINED", decodedHead.GetScanStatus())
	}

	meta := &scrapv1.DocumentMeta{
		Name:       "doc.xml",
		ScanStatus: scrapv1.ScanStatus_SCAN_STATUS_UNSCANNED,
	}
	decodedMeta := &scrapv1.DocumentMeta{}
	roundTripProto(t, meta, decodedMeta)
	if decodedMeta.GetScanStatus() != scrapv1.ScanStatus_SCAN_STATUS_UNSCANNED {
		t.Fatalf("DocumentMeta scan_status = %v, want UNSCANNED", decodedMeta.GetScanStatus())
	}
}

func roundTripProto(t *testing.T, in, out proto.Message) {
	t.Helper()

	data, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := proto.Unmarshal(data, out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
}
