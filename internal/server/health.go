package server

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const ReadinessService = "scrap.v1-readiness"

type HealthChecker interface {
	CheckReadiness(ctx context.Context) error
}

type healthServer struct {
	healthv1.UnimplementedHealthServer
	checker HealthChecker
}

func RegisterHealth(gs *grpc.Server, checker HealthChecker) {
	healthv1.RegisterHealthServer(gs, &healthServer{checker: checker})
}

func (s *healthServer) Check(ctx context.Context, req *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	switch req.GetService() {
	case "":
		return &healthv1.HealthCheckResponse{
			Status: healthv1.HealthCheckResponse_SERVING,
		}, nil
	case ReadinessService:
		return s.checkReadinessResponse(ctx)

	default:
		return nil, status.Errorf(codes.NotFound, "unknown health service %q", req.GetService())
	}
}

// checkReadinessResponse maps readiness check errors to NOT_SERVING status
// without propagating the error as a gRPC error.
func (s *healthServer) checkReadinessResponse(ctx context.Context) (*healthv1.HealthCheckResponse, error) {
	serving := healthv1.HealthCheckResponse_SERVING
	if s.checker.CheckReadiness(ctx) != nil {
		serving = healthv1.HealthCheckResponse_NOT_SERVING
	}
	return &healthv1.HealthCheckResponse{Status: serving}, nil
}
