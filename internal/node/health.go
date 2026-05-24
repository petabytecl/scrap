package node

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
)

const adminReadinessHealthService = "scrap.admin.v1-readiness"

var adminReadinessHealthServices = []string{
	adminReadinessHealthService,
	adminv1.InspectService_ServiceDesc.ServiceName,
	adminv1.OperationService_ServiceDesc.ServiceName,
	adminv1.RestoreService_ServiceDesc.ServiceName,
	adminv1.RepairService_ServiceDesc.ServiceName,
	adminv1.MemberService_ServiceDesc.ServiceName,
	adminv1.LifecycleService_ServiceDesc.ServiceName,
	adminv1.KeyService_ServiceDesc.ServiceName,
	adminv1.CapacityService_ServiceDesc.ServiceName,
	adminv1.DisasterRecoveryService_ServiceDesc.ServiceName,
}

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
	default:
		if !isAdminReadinessHealthService(req.GetService()) {
			return nil, status.Errorf(codes.NotFound, "unknown health service %q", req.GetService())
		}
		err = s.checkReadiness(ctx)
	}
	return &healthv1.HealthCheckResponse{Status: servingStatus(err)}, nil
}

func (s healthServer) List(ctx context.Context, _ *healthv1.HealthListRequest) (*healthv1.HealthListResponse, error) {
	readinessStatus := servingStatus(s.checkReadiness(ctx))
	statuses := map[string]*healthv1.HealthCheckResponse{
		"": {
			Status: servingStatus(s.checkLiveness(ctx)),
		},
	}
	for _, service := range adminReadinessHealthServices {
		statuses[service] = &healthv1.HealthCheckResponse{Status: readinessStatus}
	}
	return &healthv1.HealthListResponse{
		Statuses: statuses,
	}, nil
}

func isAdminReadinessHealthService(service string) bool {
	for _, readinessService := range adminReadinessHealthServices {
		if service == readinessService {
			return true
		}
	}
	return false
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
