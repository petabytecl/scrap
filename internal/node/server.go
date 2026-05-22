package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/petabytecl/scrap/internal/api"
	"github.com/petabytecl/scrap/internal/config"
	"github.com/petabytecl/scrap/internal/operations"
	"google.golang.org/grpc"
)

type Applications struct {
	Documents    api.DocumentApplication
	Transactions api.TransactionApplication
	Inspect      api.InspectApplication
	Repair       api.RepairApplication
	Member       api.MemberApplication
	DR           api.DisasterRecoveryApplication
	Operations   *operations.Store
}

type Server struct {
	publicListener net.Listener
	adminListener  net.Listener
	publicGRPC     *grpc.Server
	adminGRPC      *grpc.Server
}

func Listen(cfg config.Config, apps Applications) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	publicListener, err := net.Listen("tcp", cfg.PublicListenAddress)
	if err != nil {
		return nil, fmt.Errorf("listen public grpc: %w", err)
	}
	adminListener, err := net.Listen("tcp", cfg.AdminListenAddress)
	if err != nil {
		_ = publicListener.Close()
		return nil, fmt.Errorf("listen admin grpc: %w", err)
	}

	return newServer(publicListener, adminListener, apps), nil
}

func newServer(publicListener net.Listener, adminListener net.Listener, apps Applications) *Server {
	publicGRPC := grpc.NewServer()
	api.RegisterPublicServer(publicGRPC, api.NewPublicServer(apps.Documents, apps.Transactions))
	adminGRPC := grpc.NewServer()
	api.RegisterAdminServer(adminGRPC, api.NewAdminServer(
		api.WithInspectApplication(apps.Inspect),
		api.WithRepairApplication(apps.Repair),
		api.WithMemberApplication(apps.Member),
		api.WithDisasterRecoveryApplication(apps.DR),
		api.WithOperationStore(apps.Operations),
	))
	return &Server{
		publicListener: publicListener,
		adminListener:  adminListener,
		publicGRPC:     publicGRPC,
		adminGRPC:      adminGRPC,
	}
}

func (s *Server) PublicAddress() string {
	return s.publicListener.Addr().String()
}

func (s *Server) AdminAddress() string {
	return s.adminListener.Addr().String()
}

func (s *Server) Serve(ctx context.Context) error {
	errCh := make(chan error, 2)
	var once sync.Once
	stop := func() {
		s.publicGRPC.GracefulStop()
		s.adminGRPC.GracefulStop()
	}

	go func() {
		if err := s.publicGRPC.Serve(s.publicListener); err != nil {
			errCh <- fmt.Errorf("serve public grpc: %w", err)
		}
	}()
	go func() {
		if err := s.adminGRPC.Serve(s.adminListener); err != nil {
			errCh <- fmt.Errorf("serve admin grpc: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		once.Do(stop)
		return nil
	case err := <-errCh:
		once.Do(stop)
		return err
	}
}

func (s *Server) Stop() {
	s.publicGRPC.Stop()
	s.adminGRPC.Stop()
}

func (s *Server) Close() error {
	s.Stop()
	return errors.Join(s.publicListener.Close(), s.adminListener.Close())
}
