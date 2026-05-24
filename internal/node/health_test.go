package node

import (
	"context"
	"errors"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"

	adminv1 "github.com/petabytecl/scrap/internal/gen/scrap/admin/v1"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestHealthServerRejectsNilRequest(t *testing.T) {
	server := healthServer{app: fakeHealthApplication{}}

	_, err := server.Check(context.Background(), nil)

	requireCode(t, err, codes.InvalidArgument)
}

func TestHealthServerReportsNotServingWithoutApplication(t *testing.T) {
	server := healthServer{}

	liveness, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{})
	testutil.RequireNoErrorf(t, err, "liveness check")
	testutil.RequireEqualf(t, liveness.GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "liveness status")
	readiness, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{Service: adminReadinessHealthService})
	testutil.RequireNoErrorf(t, err, "readiness check")
	testutil.RequireEqualf(t, readiness.GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "readiness status")
}

func TestHealthServerListReportsIndependentStatuses(t *testing.T) {
	server := healthServer{app: fakeHealthApplication{
		readinessErr: errors.New("read index unavailable"),
	}}

	resp, err := server.List(context.Background(), &healthv1.HealthListRequest{})
	testutil.RequireNoErrorf(t, err, "list health statuses")

	testutil.RequireEqualf(t, resp.GetStatuses()[""].GetStatus(), healthv1.HealthCheckResponse_SERVING, "liveness status")
	testutil.RequireEqualf(t, resp.GetStatuses()[adminReadinessHealthService].GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "readiness status")
	testutil.RequireEqualf(t, resp.GetStatuses()[adminv1.InspectService_ServiceDesc.ServiceName].GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "inspect service status")
}

func TestHealthServerTreatsAdminServicesAsReadinessChecks(t *testing.T) {
	server := healthServer{app: fakeHealthApplication{
		readinessErr: errors.New("read index unavailable"),
	}}

	resp, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{
		Service: adminv1.InspectService_ServiceDesc.ServiceName,
	})
	testutil.RequireNoErrorf(t, err, "inspect readiness check")

	testutil.RequireEqualf(t, resp.GetStatus(), healthv1.HealthCheckResponse_NOT_SERVING, "inspect readiness status")
}

func TestHealthServerUnknownServiceErrorIncludesRequestedService(t *testing.T) {
	server := healthServer{app: fakeHealthApplication{}}

	_, err := server.Check(context.Background(), &healthv1.HealthCheckRequest{Service: "scrap.unknown.v1.Service"})
	requireCode(t, err, codes.NotFound)
	testutil.RequireTruef(t, strings.Contains(err.Error(), "scrap.unknown.v1.Service"), "unknown service error = %v", err)
}

func TestBypassHealthUnaryInterceptorSkipsNextForHealthMethods(t *testing.T) {
	nextCalled := 0
	handlerCalled := 0
	interceptor := bypassHealthUnaryInterceptor(func(
		context.Context,
		any,
		*grpc.UnaryServerInfo,
		grpc.UnaryHandler,
	) (any, error) {
		nextCalled++
		return "next", nil
	})
	handler := func(context.Context, any) (any, error) {
		handlerCalled++
		return "handler", nil
	}

	resp, err := interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"},
		handler,
	)
	testutil.RequireNoErrorf(t, err, "health unary bypass")
	testutil.RequireEqualf(t, resp.(string), "handler", "health response")
	testutil.RequireEqualf(t, handlerCalled, 1, "health handler call count")
	testutil.RequireEqualf(t, nextCalled, 0, "health next call count")

	resp, err = interceptor(
		context.Background(),
		nil,
		&grpc.UnaryServerInfo{FullMethod: "/scrap.admin.v1.AdminService/Inspect"},
		handler,
	)
	testutil.RequireNoErrorf(t, err, "admin unary interceptor")
	testutil.RequireEqualf(t, resp.(string), "next", "admin response")
	testutil.RequireEqualf(t, nextCalled, 1, "admin next call count")
}

func TestBypassHealthStreamInterceptorSkipsNextForHealthMethods(t *testing.T) {
	nextCalled := 0
	handlerCalled := 0
	interceptor := bypassHealthStreamInterceptor(func(
		any,
		grpc.ServerStream,
		*grpc.StreamServerInfo,
		grpc.StreamHandler,
	) error {
		nextCalled++
		return nil
	})
	handler := func(any, grpc.ServerStream) error {
		handlerCalled++
		return nil
	}

	err := interceptor(
		nil,
		testServerStream{},
		&grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"},
		handler,
	)
	testutil.RequireNoErrorf(t, err, "health stream bypass")
	testutil.RequireEqualf(t, handlerCalled, 1, "health handler call count")
	testutil.RequireEqualf(t, nextCalled, 0, "health next call count")

	err = interceptor(
		nil,
		testServerStream{},
		&grpc.StreamServerInfo{FullMethod: "/scrap.admin.v1.AdminService/WatchOperation"},
		handler,
	)
	testutil.RequireNoErrorf(t, err, "admin stream interceptor")
	testutil.RequireEqualf(t, nextCalled, 1, "admin next call count")
}

type testServerStream struct {
	grpc.ServerStream
}
