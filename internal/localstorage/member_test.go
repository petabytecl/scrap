package localstorage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/blockstore"
	"github.com/petabytecl/scrap/internal/metastore"
	"github.com/petabytecl/scrap/internal/storageapp"
	"github.com/petabytecl/scrap/internal/testutil"
)

func TestMemberCordonLifecyclePersistsAdmissionState(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)

	member, err := Members(app).CordonMember(ctx, storageapp.MemberMutationRequest{StorageMember: "local"})
	testutil.RequireNoErrorf(t, err, "cordon local member")
	testutil.RequireTruef(t, member.GetCordoned(), "cordoned member = %#v", member)
	state, err := readLocalMemberState(app.dir)
	testutil.RequireNoErrorf(t, err, "read cordoned member state")
	testutil.RequireTruef(t, state.Cordoned, "persisted cordoned state = %#v", state)
	if err := app.requireWriteAdmission(ctx, nil); err == nil || !strings.Contains(err.Error(), "cordoned") {
		t.Fatalf("write admission after cordon = %v, want cordoned error", err)
	}

	member, err = Members(app).UncordonMember(ctx, storageapp.MemberMutationRequest{StorageMember: "local"})
	testutil.RequireNoErrorf(t, err, "uncordon local member")
	testutil.RequireFalsef(t, member.GetCordoned(), "uncordoned member = %#v", member)
	state, err = readLocalMemberState(app.dir)
	testutil.RequireNoErrorf(t, err, "read uncordoned member state")
	testutil.RequireFalsef(t, state.Cordoned, "persisted uncordoned state = %#v", state)
	testutil.RequireNoErrorf(t, app.requireWriteAdmission(ctx, nil), "write admission after uncordon")
}

func TestMemberAdminMethodsRejectRemoteAndCanceledRequests(t *testing.T) {
	app := openTestApplication(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Members(app).CordonMember(ctx, storageapp.MemberMutationRequest{StorageMember: "local"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cordon canceled error = %v, want %v", err, context.Canceled)
	}
	if _, err := Members(app).UncordonMember(context.Background(), storageapp.MemberMutationRequest{StorageMember: "remote"}); err == nil {
		t.Fatal("uncordon remote error = nil, want not found")
	}
	if _, err := Members(app).GetEvictionSafety(ctx, "local"); !errors.Is(err, context.Canceled) {
		t.Fatalf("eviction canceled error = %v, want %v", err, context.Canceled)
	}
	if safety, err := Members(app).GetEvictionSafety(context.Background(), "local"); err != nil || safety.GetSafeToEvict() {
		t.Fatalf("local eviction safety = %#v, %v; want unsafe local member", safety, err)
	}
	if safety, err := Members(app).GetEvictionSafety(context.Background(), "remote"); err == nil || safety != nil {
		t.Fatalf("remote eviction safety = %#v, %v; want not found", safety, err)
	}
}

func TestLocalMemberStateFileHandlesMissingMalformedAndRemoval(t *testing.T) {
	dir := t.TempDir()
	state, err := readLocalMemberState(dir)
	testutil.RequireNoErrorf(t, err, "read missing member state")
	testutil.RequireEqualf(t, state, localMemberState{}, "missing member state")

	statePath := filepath.Join(dir, localMemberStateFile)
	if err := os.WriteFile(statePath, []byte(`{"cordoned":`), 0o600); err != nil {
		t.Fatalf("write malformed member state: %v", err)
	}
	if _, err := readLocalMemberState(dir); err == nil {
		t.Fatal("malformed member state error = nil, want error")
	}

	testutil.RequireNoErrorf(t, writeLocalMemberState(dir, localMemberState{Cordoned: true, Draining: true}), "write member state")
	state, err = readLocalMemberState(dir)
	testutil.RequireNoErrorf(t, err, "read written member state")
	testutil.RequireEqualf(t, state, localMemberState{Cordoned: true, Draining: true}, "written member state")
	testutil.RequireNoErrorf(t, removeLocalPath(dir, statePath), "remove member state")
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat removed member state = %v, want not exist", err)
	}
}

func TestDocumentApplicationWriteFacadeStoresDocument(t *testing.T) {
	ctx := context.Background()
	app := openTestApplication(t)
	doc := testDocumentIdentity()
	doc.DocumentName = "document-app-facade.xml"

	result, err := app.Documents().WriteDocument(ctx, storageapp.WriteDocumentInit{
		Identity:         doc,
		DocumentClass:    storageapp.DocumentClassPermanent,
		PriorityClass:    storageapp.PriorityClassNormal,
		CreatedByService: "facade-test",
	}, newChunkReader([][]byte{[]byte("facade bytes")}))
	testutil.RequireNoErrorf(t, err, "write through document application facade")
	testutil.RequireEqualf(t, result.Metadata.Identity, doc, "facade write identity")
	_, err = app.metadata.HeadDocument(doc)
	testutil.RequireNoErrorf(t, err, "head facade-written document")
}

func TestPeerRepairSourceRegistrationCanRemoveSource(t *testing.T) {
	app := openTestApplication(t)
	source := PeerRepairSourceFunc(func(context.Context, blockstore.ReplicaRef, io.Writer) error {
		return nil
	})
	app.SetPeerRepairSource("member-1", source)
	testutil.RequireNotNilf(t, app.peerRepairSources["member-1"], "registered peer repair source")
	app.SetPeerRepairSource("member-1", nil)
	testutil.RequireNilf(t, app.peerRepairSources["member-1"], "removed peer repair source")

	reason := repairReason(metastore.RepairState{PhysicalRef: "local/block-1"})
	testutil.RequireEqualf(t, reason, "repair required for byte source local/block-1", "non-quarantined repair reason")
}
