package localstorage

import (
	"context"

	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageapp"
)

type VerificationEngine interface {
	waitByteServingReady(context.Context) error
	readVerifiedBackendRange(context.Context, metastore.Document, metastore.UploadIntent, storageapp.ReadRange) ([]byte, error)
	verifyBackendEnvelope(context.Context, metastore.UploadIntent) error
}

type verificationEngine struct {
	*Application
}
