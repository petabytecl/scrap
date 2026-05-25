package localstorage

import (
	"bytes"
	"context"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/replication"
)

type PeerPreparationOptions struct {
	Targets []replication.Target
	Policy  replication.Policy
}

func ConfigurePeerPreparation(app *Application, options PeerPreparationOptions) {
	if app == nil {
		return
	}
	app.peerPrepareTargets = append([]replication.Target(nil), options.Targets...)
	app.peerPreparePolicy = options.Policy
}

func PeerPreparer(app *Application) replication.Preparer {
	return peerPreparer{app: app}
}

type peerPreparer struct {
	app *Application
}

func (p peerPreparer) PrepareDocument(ctx context.Context, request replication.PrepareRequest) (replication.Receipt, error) {
	if p.app == nil {
		return replication.Receipt{}, replication.ErrReceiptMismatch
	}
	var data bytes.Buffer
	if err := request.WriteBytes(ctx, &data); err != nil {
		return replication.Receipt{}, err
	}
	if err := replication.ValidatePreparedBytes(request.Document, data.Bytes()); err != nil {
		return replication.Receipt{}, err
	}
	record := blockstore.Record{
		BlockID:       request.Document.BlockID,
		StoredOffset:  request.Document.StoredOffset,
		StoredLength:  request.Document.StoredLength,
		LogicalSHA256: request.Document.LogicalSHA256,
		StoredSHA256:  request.Document.StoredSHA256,
		Frames:        append([]blockstore.FrameRecord(nil), request.Document.Frames...),
	}
	if err := p.app.blocks.InstallVerifiedRange(ctx, record, request.Document.StoredSHA256, bytes.NewReader(data.Bytes())); err != nil {
		return replication.Receipt{}, err
	}
	return replication.ReceiptFromPreparedDocument(p.app.MemberID(), request.Document), nil
}
