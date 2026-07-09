package encryption_test

import (
	"context"
	"errors"
	"testing"

	"github.com/petabytecl/scrap/internal/encryption"
)

func TestRouterRoutesUnwrapByEnvelopeIdentity(t *testing.T) {
	defaultTransit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: "scrap-documents"})
	altTransit := encryption.NewFakeTransit(encryption.FakeConfig{KeyName: "alt-key"})

	router, err := encryption.NewRouter(encryption.RouterConfig{
		Default: defaultTransit,
		Routes: map[encryption.TransitRoute]encryption.Transit{
			{Mount: "transit", Key: "alt-key"}: altTransit,
		},
	})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	router.SetDefaultRoute("transit", "scrap-documents")

	key, err := altTransit.GenerateDataKey(context.Background(), encryption.GenerateDataKeyRequest{})
	if err != nil {
		t.Fatalf("GenerateDataKey: %v", err)
	}
	_, err = router.UnwrapDataKey(context.Background(), encryption.UnwrapDataKeyRequest{
		WrappedKey:   key.WrappedKey,
		TransitMount: "transit",
		TransitKey:   "alt-key",
	})
	if err != nil {
		t.Fatalf("UnwrapDataKey via alt route: %v", err)
	}

	_, err = router.UnwrapDataKey(context.Background(), encryption.UnwrapDataKeyRequest{
		WrappedKey:   key.WrappedKey,
		TransitMount: "transit",
		TransitKey:   "unknown-key",
	})
	if !errors.Is(err, encryption.ErrMissingKey) {
		t.Fatalf("UnwrapDataKey unknown route = %v, want ErrMissingKey", err)
	}
}
