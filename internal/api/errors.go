package api

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/petabytecl/scrap/internal/appstatus"
	scrapv1 "github.com/petabytecl/scrap/internal/gen/scrap/v1"
	"github.com/petabytecl/scrap/internal/storageapp"
)

func ToGRPCError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	var applicationError *appstatus.Error
	if !errors.As(err, &applicationError) {
		return err
	}
	st := status.New(grpcCode(applicationError.Code), applicationError.Message)
	withDetails, detailErr := statusWithDetails(st, applicationError.Details())
	if detailErr != nil {
		return st.Err()
	}
	return withDetails.Err()
}

func statusWithDetails(st *status.Status, details []any) (*status.Status, error) {
	if len(details) == 0 {
		return st, nil
	}
	messages := make([]protoadapt.MessageV1, 0, len(details))
	for _, detail := range details {
		message, ok := applicationDetailToProto(detail)
		if !ok {
			continue
		}
		messages = append(messages, protoadapt.MessageV1Of(message))
	}
	if len(messages) == 0 {
		return st, nil
	}
	return st.WithDetails(messages...)
}

func applicationDetailToProto(detail any) (proto.Message, bool) {
	switch detail := detail.(type) {
	case storageapp.IntegrityFailureDetail:
		return &scrapv1.IntegrityFailureDetail{
			Identity:         documentIdentityToProto(detail.Identity),
			AttemptedSources: append([]string(nil), detail.AttemptedSources...),
			EvidenceId:       detail.EvidenceID,
		}, true
	case storageapp.RestorePendingDetail:
		return &scrapv1.RestorePendingDetail{
			Identity:         documentIdentityToProto(detail.Identity),
			AffectedBlockIds: append([]string(nil), detail.AffectedBlockIDs...),
			RestoreState:     detail.RestoreState,
			RestoreQueued:    detail.RestoreQueued,
			RetryHint:        retryHintToProto(detail.RetryHint),
		}, true
	case storageapp.CryptoUnavailableDetail:
		return &scrapv1.CryptoUnavailableDetail{
			Identity:  documentIdentityToProto(detail.Identity),
			KeyScope:  detail.KeyScope,
			RetryHint: retryHintToProto(detail.RetryHint),
		}, true
	case storageapp.UnsafeCapacityDetail:
		return &scrapv1.UnsafeCapacityDetail{
			CapacityProfileId: detail.CapacityProfileID,
			RequiredBytes:     detail.RequiredBytes,
			AvailableBytes:    detail.AvailableBytes,
			Warnings:          append([]string(nil), detail.Warnings...),
		}, true
	default:
		return nil, false
	}
}

func retryHintToProto(hint storageapp.RetryHint) *scrapv1.RetryHint {
	out := &scrapv1.RetryHint{
		Retryable: hint.Retryable,
		Reason:    hint.Reason,
	}
	if hint.RetryAt != nil {
		out.RetryAt = timestamppb.New(*hint.RetryAt)
	}
	if hint.RetryDelay != nil {
		out.RetryDelay = durationpb.New(*hint.RetryDelay)
	}
	return out
}

func grpcCode(code appstatus.Code) codes.Code {
	switch code {
	case appstatus.CodeInvalidArgument:
		return codes.InvalidArgument
	case appstatus.CodeNotFound:
		return codes.NotFound
	case appstatus.CodeAlreadyExists:
		return codes.AlreadyExists
	case appstatus.CodeFailedPrecondition:
		return codes.FailedPrecondition
	case appstatus.CodeResourceExhausted:
		return codes.ResourceExhausted
	case appstatus.CodeUnavailable:
		return codes.Unavailable
	case appstatus.CodeDataLoss:
		return codes.DataLoss
	case appstatus.CodeInternal:
		return codes.Internal
	default:
		return codes.Unknown
	}
}
