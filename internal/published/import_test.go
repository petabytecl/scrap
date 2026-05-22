package published

import (
	"bytes"
	"testing"

	"github.com/petabytecl/scrap/internal/metastore"
)

func TestReadSnapshotContentsImportsDocumentsAndUploadIntents(t *testing.T) {
	var snapshot bytes.Buffer
	data := []byte("imported bytes")
	document := publishedTestDocument("block-1", data)
	if err := WriteDocumentSnapshotRecords(&snapshot, SnapshotOptions{
		SourceNamespace: "source-a",
		ShardID:         "shard-a",
		HighWatermark:   42,
		LocationObjects: map[string]LocationObjects{
			"block-1": {
				BackendObjectKey: "objects/block-1.blk",
				IndexObjectKey:   "objects/block-1.idx",
			},
		},
	}, []metastore.Document{document}); err != nil {
		t.Fatalf("write snapshot records: %v", err)
	}

	contents, err := ReadSnapshotContents(bytes.NewReader(snapshot.Bytes()))
	if err != nil {
		t.Fatalf("read snapshot contents: %v", err)
	}
	if len(contents.Documents) != 1 || len(contents.UploadIntents) != 1 || contents.Tombstones != 0 {
		t.Fatalf("contents = %#v, want one document and upload intent", contents)
	}
	imported := contents.Documents[0]
	if imported.Identity != document.Identity ||
		imported.Length != document.Length ||
		imported.Availability != document.Availability ||
		imported.LifecycleState != document.LifecycleState ||
		imported.UploadState != metastore.UploadStateUploaded ||
		len(imported.Location.Frames) != 1 {
		t.Fatalf("imported document = %#v, want published document metadata", imported)
	}
	intent := contents.UploadIntents[0]
	if intent.BlockID != "block-1" ||
		intent.BackendObjectKey != "objects/block-1.blk" ||
		intent.IndexObjectKey != "objects/block-1.idx" ||
		intent.State != metastore.UploadStateUploaded {
		t.Fatalf("imported intent = %#v, want uploaded backend refs", intent)
	}
}
