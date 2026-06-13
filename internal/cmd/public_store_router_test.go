package cmd

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/routing"
	storeapi "github.com/petabytecl/scrap/internal/store"
)

func TestPublicStoreRouterRoutesWriteDocumentByTransaction(t *testing.T) {
	router, stores := newRecordingPublicRouter(t)
	writeResult, err := router.WriteDocument(context.Background(), "tx-alpha", "doc-a", "text/xml", "idem-a", strings.NewReader("body-a"))
	if err != nil {
		t.Fatalf("WriteDocument Shard 7: %v", err)
	}
	if writeResult.Size != int64(len("body-a")) {
		t.Fatalf("WriteDocument Size = %d, want %d", writeResult.Size, len("body-a"))
	}
	writeResult, err = router.WriteDocument(context.Background(), "tx-bravo", "doc-b", "text/xml", "idem-b", strings.NewReader("body-b"))
	if err != nil {
		t.Fatalf("WriteDocument Shard 9: %v", err)
	}
	if writeResult.Size != int64(len("body-b")) {
		t.Fatalf("WriteDocument Size = %d, want %d", writeResult.Size, len("body-b"))
	}
	assertPublicStoreCalls(t, stores[7], []publicStoreCall{
		{method: "WriteDocument", txID: "tx-alpha", docName: "doc-a", body: "body-a"},
	})
	assertPublicStoreCalls(t, stores[9], []publicStoreCall{
		{method: "WriteDocument", txID: "tx-bravo", docName: "doc-b", body: "body-b"},
	})
}

func TestPublicStoreRouterRoutesHeadDocumentByTransaction(t *testing.T) {
	router, stores := newRecordingPublicRouter(t)
	meta, err := router.HeadDocument(context.Background(), "tx-alpha", "doc-a")
	if err != nil {
		t.Fatalf("HeadDocument Shard 7: %v", err)
	}
	if meta.Name != "doc-seven" {
		t.Fatalf("HeadDocument meta.Name = %q, want doc-seven", meta.Name)
	}

	meta, err = router.HeadDocument(context.Background(), "tx-bravo", "doc-b")
	if err != nil {
		t.Fatalf("HeadDocument Shard 9: %v", err)
	}
	if meta.Name != "doc-nine" {
		t.Fatalf("HeadDocument meta.Name = %q, want doc-nine", meta.Name)
	}
	assertPublicStoreCalls(t, stores[7], []publicStoreCall{
		{method: "HeadDocument", txID: "tx-alpha", docName: "doc-a"},
	})
	assertPublicStoreCalls(t, stores[9], []publicStoreCall{
		{method: "HeadDocument", txID: "tx-bravo", docName: "doc-b"},
	})
}

func TestPublicStoreRouterRoutesReadDocumentByTransaction(t *testing.T) {
	router, stores := newRecordingPublicRouter(t)
	body, meta, err := router.ReadDocument(context.Background(), "tx-alpha", "doc-c")
	if err != nil {
		t.Fatalf("ReadDocument Shard 7: %v", err)
	}
	defer func() { _ = body.Close() }()
	readBody, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadDocument body: %v", err)
	}
	if string(readBody) != "read-doc-seven" || meta.Name != "doc-seven" {
		t.Fatalf("ReadDocument body/meta = %q/%q, want Shard 7", string(readBody), meta.Name)
	}
	body, meta, err = router.ReadDocument(context.Background(), "tx-bravo", "doc-d")
	if err != nil {
		t.Fatalf("ReadDocument Shard 9: %v", err)
	}
	defer func() { _ = body.Close() }()
	readBody, err = io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadDocument body: %v", err)
	}
	if string(readBody) != "read-doc-nine" || meta.Name != "doc-nine" {
		t.Fatalf("ReadDocument body/meta = %q/%q, want Shard 9", string(readBody), meta.Name)
	}
	assertPublicStoreCalls(t, stores[7], []publicStoreCall{
		{method: "ReadDocument", txID: "tx-alpha", docName: "doc-c"},
	})
	assertPublicStoreCalls(t, stores[9], []publicStoreCall{
		{method: "ReadDocument", txID: "tx-bravo", docName: "doc-d"},
	})
}

func TestPublicStoreRouterRoutesFindDocumentsByTransaction(t *testing.T) {
	router, stores := newRecordingPublicRouter(t)
	docs, err := router.FindDocuments(context.Background(), "tx-alpha")
	if err != nil {
		t.Fatalf("FindDocuments Shard 7: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "doc-seven" {
		t.Fatalf("FindDocuments docs = %#v, want Shard 7 document", docs)
	}

	docs, err = router.FindDocuments(context.Background(), "tx-bravo")
	if err != nil {
		t.Fatalf("FindDocuments Shard 9: %v", err)
	}
	if len(docs) != 1 || docs[0].Name != "doc-nine" {
		t.Fatalf("FindDocuments docs = %#v, want Shard 9 document", docs)
	}
	assertPublicStoreCalls(t, stores[7], []publicStoreCall{
		{method: "FindDocuments", txID: "tx-alpha"},
	})
	assertPublicStoreCalls(t, stores[9], []publicStoreCall{
		{method: "FindDocuments", txID: "tx-bravo"},
	})
}

func TestPublicStoreRouterFailsClosedForInvalidOrUnavailableRoutes(t *testing.T) {
	placement := testTwoShardPlacement(t)

	t.Run("invalid transaction", func(t *testing.T) {
		router := newPublicStoreRouter(placement, map[uint64]storeapi.Store{7: &recordingPublicStore{}}, nil)
		_, err := router.HeadDocument(context.Background(), "", "doc-a")
		if !errors.Is(err, storeapi.ErrInvalidArgument) {
			t.Fatalf("HeadDocument error = %v, want ErrInvalidArgument", err)
		}
	})

	t.Run("routing not configured", func(t *testing.T) {
		router := newPublicStoreRouter(placement, nil, nil)
		_, err := router.HeadDocument(context.Background(), "tx-alpha", "doc-a")
		assertUnavailableReason(t, err, storeapi.UnavailableReasonShardRoutingPending)
	})

	t.Run("owning Shard not local", func(t *testing.T) {
		router := newPublicStoreRouter(placement, map[uint64]storeapi.Store{7: &recordingPublicStore{}}, nil)
		_, err := router.HeadDocument(context.Background(), "tx-bravo", "doc-b")
		assertUnavailableReason(t, err, storeapi.UnavailableReasonShardRouteUnavailable)
	})
}

func TestPublicStoreRouterRouteFailuresDoNotLeakRawIdentifiers(t *testing.T) {
	router := newPublicStoreRouter(testTwoShardPlacement(t), map[uint64]storeapi.Store{9: &recordingPublicStore{}}, nil)

	_, err := router.HeadDocument(context.Background(), "doc-secret-tenant-a", "invoice-secret.xml")
	if err == nil {
		t.Fatal("HeadDocument succeeded, want route failure")
	}
	rendered := err.Error()
	for _, forbidden := range []string{"doc-secret-tenant-a", "doc-secret", "tenant-a", "invoice-secret.xml"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("route error leaked %q in %q", forbidden, rendered)
		}
	}
}

func TestPublicStoreRouterRecordsBoundedLookupTelemetry(t *testing.T) {
	recorder := &recordingRouteLookupRecorder{}
	router := newPublicStoreRouter(
		testTwoShardPlacement(t),
		map[uint64]storeapi.Store{7: &recordingPublicStore{}},
		recorder,
	)

	_, _ = router.HeadDocument(context.Background(), "doc-secret-tenant-a", "invoice-secret.xml")
	_, _ = router.HeadDocument(context.Background(), "", "invoice-secret.xml")

	want := []routing.LookupRecord{
		{
			Outcome:      routing.LookupOutcomeRouted,
			Reason:       routing.LookupReasonMatched,
			ShardID:      7,
			ShardIDValid: true,
		},
		{
			Outcome: routing.LookupOutcomeRejected,
			Reason:  routing.LookupReasonInvalidTransaction,
		},
	}
	if !reflect.DeepEqual(recorder.records, want) {
		t.Fatalf("records = %#v, want %#v", recorder.records, want)
	}
	rendered := strings.Join(recorder.rendered(), " ")
	for _, forbidden := range []string{"doc-secret-tenant-a", "doc-secret", "tenant-a", "invoice-secret.xml"} {
		if strings.Contains(rendered, forbidden) {
			t.Fatalf("lookup telemetry leaked %q in %q", forbidden, rendered)
		}
	}
}

func newRecordingPublicRouter(t *testing.T) (storeapi.Store, map[uint64]*recordingPublicStore) {
	t.Helper()
	stores := map[uint64]*recordingPublicStore{
		7: {meta: storeapi.DocumentMeta{Name: "doc-seven"}},
		9: {meta: storeapi.DocumentMeta{Name: "doc-nine"}},
	}
	targets := map[uint64]storeapi.Store{
		7: stores[7],
		9: stores[9],
	}
	return newPublicStoreRouter(testTwoShardPlacement(t), targets, nil), stores
}

func testTwoShardPlacement(t *testing.T) routing.Placement {
	t.Helper()
	placement, err := routing.NewPlacement(routing.PlacementConfig{
		SlotCount: routing.SlotCount,
		Shards:    []uint64{7, 9},
		Ranges: []routing.SlotRange{
			{ShardID: 7, StartSlot: 0, EndSlot: 511},
			{ShardID: 9, StartSlot: 512, EndSlot: 1023},
		},
	})
	if err != nil {
		t.Fatalf("NewPlacement: %v", err)
	}
	return placement
}

func assertUnavailableReason(t *testing.T, err error, want string) {
	t.Helper()
	got, ok := storeapi.UnavailableReason(err)
	if !ok || got != want {
		t.Fatalf("UnavailableReason = %q/%v, err=%v; want %q", got, ok, err, want)
	}
}

func assertPublicStoreCalls(t *testing.T, store *recordingPublicStore, want []publicStoreCall) {
	t.Helper()
	if !reflect.DeepEqual(store.calls, want) {
		t.Fatalf("calls = %#v, want %#v", store.calls, want)
	}
}

type publicStoreCall struct {
	method  string
	txID    string
	docName string
	body    string
}

type recordingPublicStore struct {
	calls []publicStoreCall
	meta  storeapi.DocumentMeta
}

type recordingRouteLookupRecorder struct {
	records []routing.LookupRecord
}

func (r *recordingRouteLookupRecorder) RecordRoutingLookup(_ context.Context, record routing.LookupRecord) {
	r.records = append(r.records, record)
}

func (r *recordingRouteLookupRecorder) rendered() []string {
	rendered := make([]string, 0, len(r.records))
	for _, record := range r.records {
		rendered = append(rendered, string(record.Outcome)+string(record.Reason))
		if record.ShardIDValid {
			rendered = append(rendered, strconv.FormatUint(record.ShardID, 10))
		}
	}
	return rendered
}

func (s *recordingPublicStore) WriteDocument(_ context.Context, txID, docName, _, _ string, body io.Reader) (storeapi.WriteResult, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return storeapi.WriteResult{}, err
	}
	s.calls = append(s.calls, publicStoreCall{
		method:  "WriteDocument",
		txID:    txID,
		docName: docName,
		body:    string(data),
	})
	return storeapi.WriteResult{Size: int64(len(data))}, nil
}

func (s *recordingPublicStore) HeadDocument(_ context.Context, txID, docName string) (storeapi.DocumentMeta, error) {
	s.calls = append(s.calls, publicStoreCall{method: "HeadDocument", txID: txID, docName: docName})
	return s.meta, nil
}

func (s *recordingPublicStore) ReadDocument(_ context.Context, txID, docName string) (io.ReadCloser, storeapi.DocumentMeta, error) {
	s.calls = append(s.calls, publicStoreCall{method: "ReadDocument", txID: txID, docName: docName})
	return io.NopCloser(strings.NewReader("read-" + s.meta.Name)), s.meta, nil
}

func (s *recordingPublicStore) FindDocuments(_ context.Context, txID string) ([]storeapi.DocumentMeta, error) {
	s.calls = append(s.calls, publicStoreCall{method: "FindDocuments", txID: txID})
	return []storeapi.DocumentMeta{s.meta}, nil
}
