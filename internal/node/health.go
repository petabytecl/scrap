package node

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const adminReadinessHealthService = "scrap.admin.v1.AdminService"

type HealthApplication interface {
	CheckReadiness(context.Context) error
	CheckLiveness(context.Context) error
}

type healthServer struct {
	healthv1.UnimplementedHealthServer
	app HealthApplication
}

func registerHealthServer(registrar grpc.ServiceRegistrar, app HealthApplication) {
	healthv1.RegisterHealthServer(registrar, healthServer{app: app})
}

func (s healthServer) Check(ctx context.Context, req *healthv1.HealthCheckRequest) (*healthv1.HealthCheckResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "health check request is required")
	}
	var err error
	switch req.GetService() {
	case "":
		err = s.checkLiveness(ctx)
	case adminReadinessHealthService:
		err = s.checkReadiness(ctx)
	default:
		return nil, status.Error(codes.NotFound, "unknown health service")
	}
	return &healthv1.HealthCheckResponse{Status: servingStatus(err)}, nil
}

func (s healthServer) List(ctx context.Context, _ *healthv1.HealthListRequest) (*healthv1.HealthListResponse, error) {
	return &healthv1.HealthListResponse{
		Statuses: map[string]*healthv1.HealthCheckResponse{
			"": {
				Status: servingStatus(s.checkLiveness(ctx)),
			},
			adminReadinessHealthService: {
				Status: servingStatus(s.checkReadiness(ctx)),
			},
		},
	}, nil
}

func (s healthServer) checkReadiness(ctx context.Context) error {
	if s.app == nil {
		return errors.New("node: health application is not configured")
	}
	return s.app.CheckReadiness(ctx)
}

func (s healthServer) checkLiveness(ctx context.Context) error {
	if s.app == nil {
		return errors.New("node: health application is not configured")
	}
	return s.app.CheckLiveness(ctx)
}

func servingStatus(err error) healthv1.HealthCheckResponse_ServingStatus {
	if err != nil {
		return healthv1.HealthCheckResponse_NOT_SERVING
	}
	return healthv1.HealthCheckResponse_SERVING
}

func bypassHealthUnaryInterceptor(next grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if isHealthMethod(info.FullMethod) {
			return handler(ctx, req)
		}
		return next(ctx, req, info, handler)
	}
}

func bypassHealthStreamInterceptor(next grpc.StreamServerInterceptor) grpc.StreamServerInterceptor {
	return func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if isHealthMethod(info.FullMethod) {
			return handler(srv, stream)
		}
		return next(srv, stream, info, handler)
	}
}

func isHealthMethod(method string) bool {
	return strings.HasPrefix(method, "/grpc.health.v1.Health/")
}
