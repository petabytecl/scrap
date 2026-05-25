package localstorage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/petabytecl/scrap/internal/backend"
	"github.com/petabytecl/scrap/internal/backendupload"
	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/cryptoenv"
	metastorev1 "github.com/petabytecl/scrap/internal/gen/scrap/metastore/v1"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/operations"
	"github.com/petabytecl/scrap/internal/raftmeta"
	"github.com/petabytecl/scrap/internal/replication"
	"github.com/petabytecl/scrap/internal/storageapp"
)

type Application struct {
	dir                      string
	cellID                   string
	memberID                 string
	blocks                   *blockstore.Store
	backendStore             backend.Store
	envelopeSource           backendupload.BlockEnvelopeSource
	envelopeTransit          cryptoenv.Transit
	operationStore           *operations.Store
	metadata                 *metastore.Store
	authority                *raftmeta.Authority
	metadataAuthorityMember  string
	prepare                  *prepareLog
	memberMu                 sync.RWMutex
	memberState              localMemberState
	byteServingMu            sync.Mutex
	byteServingReady         bool
	byteServingErr           error
	byteServingCancel        context.CancelFunc
	byteServingDone          chan struct{}
	peerRepairSources        map[string]PeerRepairSource
	sealBlockAtBytes         uint64
	minUsableBytesAfterWrite uint64
	peerPrepareTargets       []replication.Target
	peerPreparePolicy        replication.Policy
	now                      func() time.Time
	writeFaults              writeFaultHooks

	documents         *documentApplication
	transactions      TransactionCoordinator
	verifier          verificationEngineRole
	operationExecutor *OperationExecutor
}

const DefaultSealBlockAtBytes uint64 = 256 * 1024 * 1024

const defaultLocalRuntimeID = "local"

type OpenOptions struct {
	CellID                    string
	MemberID                  string
	AuthorityMemberIDs        []string
	MetadataAuthorityMemberID string
}

type PeerRepairSource interface {
	ReadReplica(context.Context, blockstore.ReplicaRef, io.Writer) error
}

type PeerRepairSourceFunc func(context.Context, blockstore.ReplicaRef, io.Writer) error

func (f PeerRepairSourceFunc) ReadReplica(ctx context.Context, replica blockstore.ReplicaRef, writer io.Writer) error {
	return f(ctx, replica, writer)
}

// writeFaultHooks makes local durability boundaries injectable for deterministic
// crash-recovery tests without process-level chaos.
type writeFaultHooks struct {
	afterBlockSync     func(blockstore.Record) error
	afterPrepareSync   func(metastore.Document) error
	afterMetadataApply func(metastore.Document) error
	beforeACK          func(metastore.Document) error
}

func Open(dir string) (*Application, error) {
	return OpenWithContext(context.Background(), dir)
}

func OpenWithContext(ctx context.Context, dir string) (*Application, error) {
	return OpenWithOptions(ctx, dir, OpenOptions{})
}

func OpenWithOptions(ctx context.Context, dir string, options OpenOptions) (*Application, error) {
	cellID, memberID := normalizeOpenIdentity(options)
	blocks, err := blockstore.Open(dir)
	if err != nil {
		return nil, err
	}
	_, metadataStatErr := os.Stat(filepath.Join(dir, "metadata"))
	if metadataStatErr != nil && !errors.Is(metadataStatErr, os.ErrNotExist) {
		_ = blocks.Close()
		return nil, metadataStatErr
	}
	metadata, err := metastore.Open(dir)
	if err != nil {
		_ = blocks.Close()
		return nil, err
	}
	authorityMembers := authorityMembersFromIDs(options.AuthorityMemberIDs, memberID)
	metadataAuthorityMemberID := normalizeMetadataAuthorityMemberID(options.MetadataAuthorityMemberID, memberID)
	authorityOptions := raftmeta.AuthorityOptions{
		LocalMemberID: memberID,
		Members:       authorityMembers,
	}
	if metadataAuthorityMemberID != "" {
		authorityOptions.FreshnessChecker = raftmeta.NewStaticLeaderFreshnessChecker(metadataAuthorityMemberID)
	}
	authority, err := raftmeta.OpenAuthorityWithOptions(filepath.Join(dir, "raftmeta"), "local", metadata, authorityOptions)
	if err != nil {
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	prepare, err := openPrepareLog(dir)
	if err != nil {
		_ = authority.Close()
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	if _, err := prepare.Recover(); err != nil {
		_ = prepare.Close()
		_ = authority.Close()
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	memberState, err := readLocalMemberState(dir)
	if err != nil {
		_ = prepare.Close()
		_ = authority.Close()
		_ = metadata.Close()
		_ = blocks.Close()
		return nil, err
	}
	app := &Application{
		dir:                     dir,
		cellID:                  cellID,
		memberID:                memberID,
		blocks:                  blocks,
		metadata:                metadata,
		authority:               authority,
		metadataAuthorityMember: metadataAuthorityMemberID,
		prepare:                 prepare,
		memberState:             memberState,
		byteServingDone:         make(chan struct{}),
		sealBlockAtBytes:        DefaultSealBlockAtBytes,
		now:                     func() time.Time { return time.Now().UTC() },
	}
	app.initComponents()
	app.verifier.startByteServingVerifier(ctx)
	return app, nil
}

func authorityMembersFromIDs(memberIDs []string, localMemberID string) []raftmeta.Member {
	ids := append([]string(nil), memberIDs...)
	if len(ids) == 0 {
		ids = append(ids, localMemberID)
	}
	members := make([]raftmeta.Member, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	nextRaftID := uint64(1)
	for _, memberID := range ids {
		memberID = strings.TrimSpace(memberID)
		if memberID == "" {
			continue
		}
		if _, exists := seen[memberID]; exists {
			continue
		}
		seen[memberID] = struct{}{}
		members = append(members, raftmeta.Member{
			RaftID:   nextRaftID,
			MemberID: memberID,
			Role:     metastorev1.MembershipRole_MEMBERSHIP_ROLE_VOTER,
		})
		nextRaftID++
	}
	return members
}

func normalizeMetadataAuthorityMemberID(configured, localMemberID string) string {
	configured = strings.TrimSpace(configured)
	if configured != "" {
		return configured
	}
	if len(strings.TrimSpace(localMemberID)) == 0 {
		return defaultLocalRuntimeID
	}
	return localMemberID
}

func normalizeOpenIdentity(options OpenOptions) (string, string) {
	cellID := strings.TrimSpace(options.CellID)
	memberID := strings.TrimSpace(options.MemberID)
	if cellID == "" {
		cellID = defaultLocalRuntimeID
	}
	if memberID == "" {
		memberID = defaultLocalRuntimeID
	}
	return cellID, memberID
}

func (a *Application) CellID() string {
	if a == nil || a.cellID == "" {
		return defaultLocalRuntimeID
	}
	return a.cellID
}

func (a *Application) MemberID() string {
	if a == nil || a.memberID == "" {
		return defaultLocalRuntimeID
	}
	return a.memberID
}

func (a *Application) isMetadataAuthorityMember() bool {
	if a == nil {
		return false
	}
	authorityMemberID := strings.TrimSpace(a.metadataAuthorityMember)
	return authorityMemberID == "" || authorityMemberID == a.MemberID()
}

func (a *Application) initComponents() {
	a.documents = &documentApplication{Application: a}
	a.transactions = &transactionCoordinator{Application: a}
	a.verifier = &verificationEngine{Application: a}
	a.operationExecutor = &OperationExecutor{Application: a}
}

func (a *Application) Close() error {
	if a == nil {
		return nil
	}
	if a.byteServingCancel != nil {
		a.byteServingCancel()
		<-a.byteServingDone
	}
	return errors.Join(a.prepare.Close(), a.authority.Close(), a.metadata.Close(), a.blocks.Close())
}

func (a *Application) Documents() storageapp.DocumentApplication {
	if a == nil {
		return nil
	}
	return a.documents
}

func (a *Application) Transactions() storageapp.TransactionApplication {
	if a == nil {
		return nil
	}
	return a.transactions
}

func (a *Application) OperationExecutor() OperationExecution {
	if a == nil {
		return nil
	}
	return a.operationExecutor
}

func (a *Application) SetEnvelopeTransit(transit cryptoenv.Transit, keyID string) {
	a.envelopeTransit = transit
	a.envelopeSource = backendupload.TransitBlockEnvelopeSource{
		Transit: transit,
		CellID:  localPublishedCellID,
		KeyID:   keyID,
		TempDir: a.scratchDir("backend-upload"),
	}
}

func (a *Application) SetBlockEnvelopeSource(source backendupload.BlockEnvelopeSource) {
	a.envelopeSource = source
}

func (a *Application) SetBackendStore(store backend.Store) {
	a.backendStore = store
}

func (a *Application) SetOperationStore(store *operations.Store) {
	a.operationStore = store
}

func (a *Application) SetSealBlockAtBytes(bytes uint64) {
	a.sealBlockAtBytes = bytes
}

func (a *Application) scratchDir(name string) string {
	return filepath.Join(a.dir, "tmp", name)
}

func (a *Application) createScratchFile(name, pattern string) (*os.File, error) {
	dir := a.scratchDir(name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return os.CreateTemp(dir, pattern)
}

func (a *Application) backendEnvelopeSource() backendupload.BlockEnvelopeSource {
	if a.envelopeSource != nil {
		return a.envelopeSource
	}
	return backendupload.LocalBlockEnvelopeSource{CellID: localPublishedCellID}
}

func (a *Application) SetPeerRepairSource(memberID string, source PeerRepairSource) {
	if a.peerRepairSources == nil {
		a.peerRepairSources = make(map[string]PeerRepairSource)
	}
	if source == nil {
		delete(a.peerRepairSources, memberID)
		return
	}
	a.peerRepairSources[memberID] = source
}
