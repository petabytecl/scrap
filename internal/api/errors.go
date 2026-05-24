package api

import (
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"

	"github.com/petabytecl/scrap/internal/appstatus"
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
		message, ok := detail.(proto.Message)
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
