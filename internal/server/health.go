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
		err := s.checker.CheckReadiness(ctx)
		if err != nil {
			return &healthv1.HealthCheckResponse{
				Status: healthv1.HealthCheckResponse_NOT_SERVING,
			}, nil
		}
		return &healthv1.HealthCheckResponse{
			Status: healthv1.HealthCheckResponse_SERVING,
		}, nil
	default:
		return nil, status.Errorf(codes.NotFound, "unknown health service %q", req.GetService())
	}
}
