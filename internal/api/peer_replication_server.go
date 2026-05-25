package api

import (
	"bytes"
	"context"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/petabytecl/scrap/internal/blockstore"
	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/identity"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/replication"
)

type PeerReplicationApplication = replication.Preparer

type PeerReplicationServer struct {
	preparer PeerReplicationApplication
}

func NewPeerReplicationServer(preparer PeerReplicationApplication) *PeerReplicationServer {
	return &PeerReplicationServer{preparer: preparer}
}

func RegisterPeerReplicationServer(registrar grpc.ServiceRegistrar, server *PeerReplicationServer) {
	adminv1.RegisterPeerReplicationServiceServer(registrar, server)
}

func (s *PeerReplicationServer) PrepareDocument(ctx context.Context, req *adminv1.PrepareDocumentRequest) (*adminv1.PrepareDocumentResponse, error) {
	if s.preparer == nil {
		return nil, status.Error(codes.Unimplemented, "peer replication service is not configured")
	}
	document, data, err := peerPrepareRequestFromProto(req)
	if err != nil {
		return nil, err
	}
	receipt, err := s.preparer.PrepareDocument(ctx, replication.PrepareRequest{
		Document: document,
		Source: replication.ByteSourceFunc(func(ctx context.Context, _ replication.PreparedDocument, writer io.Writer) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := writer.Write(data)
			return err
		}),
	})
	if err != nil {
		return nil, ToGRPCError(err)
	}
	return &adminv1.PrepareDocumentResponse{Receipt: peerReplicaReceiptToProto(receipt)}, nil
}

func peerPrepareRequestFromProto(req *adminv1.PrepareDocumentRequest) (replication.PreparedDocument, []byte, error) {
	if req == nil {
		return replication.PreparedDocument{}, nil, invalidArgument("request", identity.ReasonRequired, "request is required")
	}
	doc, err := peerPrepareDocumentIdentity(req.GetIdentity())
	if err != nil {
		return replication.PreparedDocument{}, nil, err
	}
	logicalSHA, err := peerPrepareSHA256("logical_sha256", req.GetLogicalSha256())
	if err != nil {
		return replication.PreparedDocument{}, nil, err
	}
	storedSHA, err := peerPrepareSHA256("stored_sha256", req.GetStoredSha256())
	if err != nil {
		return replication.PreparedDocument{}, nil, err
	}
	frames, err := peerPrepareFramesFromProto(req.GetFrames())
	if err != nil {
		return replication.PreparedDocument{}, nil, err
	}
	data := append([]byte(nil), req.GetData()...)
	if uint64(len(data)) != req.GetStoredLength() {
		return replication.PreparedDocument{}, nil, invalidArgument("data", "SCRAP_LENGTH_MISMATCH", "data length must match stored_length")
	}
	document := replication.PreparedDocument{
		Identity:      doc,
		PriorityClass: peerPreparePriorityFromProto(req.GetPriorityClass()),
		BlockID:       req.GetBlockId(),
		StoredOffset:  req.GetStoredOffset(),
		StoredLength:  req.GetStoredLength(),
		LogicalSHA256: logicalSHA,
		StoredSHA256:  storedSHA,
		Frames:        frames,
	}
	if err := replication.ValidatePreparedBytes(document, data); err != nil {
		return replication.PreparedDocument{}, nil, ToGRPCError(err)
	}
	return document, data, nil
}

func peerPrepareDocumentIdentity(value *scrapv1.DocumentIdentity) (identity.Document, error) {
	if value == nil {
		return identity.Document{}, invalidArgument("identity", identity.ReasonRequired, "identity is required")
	}
	doc, problems := identity.NewDocument(value.GetTenantId(), value.GetTransactionId(), value.GetDocumentName())
	if len(problems) == 0 {
		return doc, nil
	}
	var violations violations
	for _, problem := range problems {
		violations.add("identity."+problem.Field, problem.Reason, problem.Description)
	}
	return identity.Document{}, violations.err()
}

func peerPrepareSHA256(field string, value []byte) ([32]byte, error) {
	var out [32]byte
	if len(value) != len(out) {
		return out, invalidArgument(field, "SCRAP_INVALID_SHA256", field+" must be 32 bytes")
	}
	copy(out[:], value)
	return out, nil
}

func peerPrepareFramesFromProto(values []*adminv1.PreparedFrame) ([]blockstore.FrameRecord, error) {
	if len(values) == 0 {
		return nil, nil
	}
	frames := make([]blockstore.FrameRecord, 0, len(values))
	for _, value := range values {
		sha, err := peerPrepareSHA256("frames.sha256", value.GetSha256())
		if err != nil {
			return nil, err
		}
		frames = append(frames, blockstore.FrameRecord{
			FrameOffset:   value.GetFrameOffset(),
			SegmentOffset: value.GetSegmentOffset(),
			SegmentLength: value.GetSegmentLength(),
			SHA256:        sha,
		})
		if value.GetSegmentLength() == 0 {
			return nil, invalidArgument("frames", "SCRAP_INVALID_FRAME", "frame segment_length must be positive")
		}
	}
	return frames, nil
}

func peerPreparePriorityFromProto(value scrapv1.PriorityClass) metastore.PriorityClass {
	switch value {
	case scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST:
		return metastore.PriorityClassCriticalIngest
	case scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL:
		return metastore.PriorityClassNormal
	case scrapv1.PriorityClass_PRIORITY_CLASS_BULK:
		return metastore.PriorityClassBulk
	default:
		return metastore.PriorityClassNormal
	}
}

func peerReplicaReceiptToProto(receipt replication.Receipt) *adminv1.PeerReplicaReceipt {
	return &adminv1.PeerReplicaReceipt{
		MemberId:      receipt.MemberID,
		BlockId:       receipt.BlockID,
		StoredOffset:  receipt.StoredOffset,
		StoredLength:  receipt.StoredLength,
		LogicalSha256: append([]byte(nil), receipt.LogicalSHA256[:]...),
		StoredSha256:  append([]byte(nil), receipt.StoredSHA256[:]...),
	}
}

func peerReplicaReceiptFromProto(receipt *adminv1.PeerReplicaReceipt) (replication.Receipt, error) {
	if receipt == nil {
		return replication.Receipt{}, invalidArgument("receipt", identity.ReasonRequired, "receipt is required")
	}
	logicalSHA, err := peerPrepareSHA256("receipt.logical_sha256", receipt.GetLogicalSha256())
	if err != nil {
		return replication.Receipt{}, err
	}
	storedSHA, err := peerPrepareSHA256("receipt.stored_sha256", receipt.GetStoredSha256())
	if err != nil {
		return replication.Receipt{}, err
	}
	return replication.Receipt{
		MemberID:      receipt.GetMemberId(),
		BlockID:       receipt.GetBlockId(),
		StoredOffset:  receipt.GetStoredOffset(),
		StoredLength:  receipt.GetStoredLength(),
		LogicalSHA256: logicalSHA,
		StoredSHA256:  storedSHA,
	}, nil
}

func peerPrepareRequestToProto(document replication.PreparedDocument, data []byte) *adminv1.PrepareDocumentRequest {
	frames := make([]*adminv1.PreparedFrame, 0, len(document.Frames))
	for _, frame := range document.Frames {
		frames = append(frames, &adminv1.PreparedFrame{
			FrameOffset:   frame.FrameOffset,
			SegmentOffset: frame.SegmentOffset,
			SegmentLength: frame.SegmentLength,
			Sha256:        append([]byte(nil), frame.SHA256[:]...),
		})
	}
	return &adminv1.PrepareDocumentRequest{
		Identity: &scrapv1.DocumentIdentity{
			TenantId:      document.Identity.TenantID,
			TransactionId: document.Identity.TransactionID,
			DocumentName:  document.Identity.DocumentName,
		},
		PriorityClass: peerPreparePriorityToProto(document.PriorityClass),
		BlockId:       document.BlockID,
		StoredOffset:  document.StoredOffset,
		StoredLength:  document.StoredLength,
		LogicalSha256: append([]byte(nil), document.LogicalSHA256[:]...),
		StoredSha256:  append([]byte(nil), document.StoredSHA256[:]...),
		Frames:        frames,
		Data:          append([]byte(nil), data...),
	}
}

func peerPreparePriorityToProto(value metastore.PriorityClass) scrapv1.PriorityClass {
	switch value {
	case metastore.PriorityClassCriticalIngest:
		return scrapv1.PriorityClass_PRIORITY_CLASS_CRITICAL_INGEST
	case metastore.PriorityClassNormal:
		return scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL
	case metastore.PriorityClassBulk:
		return scrapv1.PriorityClass_PRIORITY_CLASS_BULK
	default:
		return scrapv1.PriorityClass_PRIORITY_CLASS_NORMAL
	}
}

func peerPrepareDataFromRequest(ctx context.Context, request replication.PrepareRequest) ([]byte, error) {
	var data bytes.Buffer
	if err := request.WriteBytes(ctx, &data); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}
