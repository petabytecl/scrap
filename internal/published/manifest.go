package published

import (
	"crypto/sha256"
	"fmt"
	"time"

	publishedv1 "github.com/petabytecl/scrap/internal/gen/scrap/published/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const metadataLayoutVersion = "v1"

type ManifestOptions struct {
	CellID          string
	SourceNamespace string
	ManifestID      string
	Generation      uint64
	PublishedAt     time.Time
	ShardWatermarks []*publishedv1.ShardWatermark
	Snapshots       []*publishedv1.ArtifactRef
	Tails           []*publishedv1.ArtifactRef
	RequiredObjects []*publishedv1.ObjectRef
	ProducerBuild   string
	ProducerSchema  string
}

func CurrentPointerObjectKey(cellID string) (string, error) {
	if cellID == "" {
		return "", fmt.Errorf("published metadata: cell id is required")
	}
	return fmt.Sprintf("cells/%s/metadata/%s/current.pointer", cellID, metadataLayoutVersion), nil
}

func ManifestObjectKey(cellID string, manifestID string) (string, error) {
	if cellID == "" {
		return "", fmt.Errorf("published metadata: cell id is required")
	}
	if manifestID == "" {
		return "", fmt.Errorf("published metadata: manifest id is required")
	}
	return fmt.Sprintf("cells/%s/metadata/%s/manifests/%s.manifest", cellID, metadataLayoutVersion, manifestID), nil
}

func SnapshotObjectKey(cellID string, shardID string, snapshotID string) (string, error) {
	if cellID == "" {
		return "", fmt.Errorf("published metadata: cell id is required")
	}
	if shardID == "" {
		return "", fmt.Errorf("published metadata: shard id is required")
	}
	if snapshotID == "" {
		return "", fmt.Errorf("published metadata: snapshot id is required")
	}
	return fmt.Sprintf("cells/%s/metadata/%s/snapshots/shard=%s/%s.snap", cellID, metadataLayoutVersion, shardID, snapshotID), nil
}

func TailObjectKey(cellID string, shardID string, firstIndex uint64, lastIndex uint64) (string, error) {
	if cellID == "" {
		return "", fmt.Errorf("published metadata: cell id is required")
	}
	if shardID == "" {
		return "", fmt.Errorf("published metadata: shard id is required")
	}
	if firstIndex == 0 || lastIndex == 0 || firstIndex > lastIndex {
		return "", fmt.Errorf("published metadata: invalid tail index range")
	}
	return fmt.Sprintf("cells/%s/metadata/%s/tails/shard=%s/%020d-%020d.tail", cellID, metadataLayoutVersion, shardID, firstIndex, lastIndex), nil
}

func BuildManifest(options ManifestOptions) (*publishedv1.Manifest, error) {
	if options.CellID == "" {
		return nil, fmt.Errorf("published metadata: cell id is required")
	}
	if options.SourceNamespace == "" {
		return nil, fmt.Errorf("published metadata: source namespace is required")
	}
	if options.ManifestID == "" {
		return nil, fmt.Errorf("published metadata: manifest id is required")
	}
	if options.Generation == 0 {
		return nil, fmt.Errorf("published metadata: generation is required")
	}
	if options.PublishedAt.IsZero() {
		return nil, fmt.Errorf("published metadata: published_at is required")
	}
	return &publishedv1.Manifest{
		SchemaVersion:   CurrentSchemaVersion,
		CellId:          options.CellID,
		SourceNamespace: options.SourceNamespace,
		ManifestId:      options.ManifestID,
		Generation:      options.Generation,
		PublishedAt:     timestamppb.New(options.PublishedAt),
		ShardWatermarks: cloneShardWatermarks(options.ShardWatermarks),
		Snapshots:       cloneArtifactRefs(options.Snapshots),
		Tails:           cloneArtifactRefs(options.Tails),
		RequiredObjects: cloneObjectRefs(options.RequiredObjects),
		ProducerBuild:   options.ProducerBuild,
		ProducerSchema:  options.ProducerSchema,
	}, nil
}

func BuildCurrentPointer(manifest *publishedv1.Manifest, manifestData []byte) (*publishedv1.CurrentPointer, error) {
	if manifest == nil {
		return nil, fmt.Errorf("published metadata: manifest is required")
	}
	if len(manifestData) == 0 {
		return nil, fmt.Errorf("published metadata: manifest data is required")
	}
	if err := validateSchemaVersion("manifest", manifest.GetSchemaVersion()); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(manifestData)
	return &publishedv1.CurrentPointer{
		SchemaVersion:   CurrentSchemaVersion,
		CellId:          manifest.GetCellId(),
		SourceNamespace: manifest.GetSourceNamespace(),
		Generation:      manifest.GetGeneration(),
		ManifestId:      manifest.GetManifestId(),
		ManifestSha256:  append([]byte(nil), sum[:]...),
		PublishedAt:     manifest.GetPublishedAt(),
	}, nil
}

func cloneShardWatermarks(items []*publishedv1.ShardWatermark) []*publishedv1.ShardWatermark {
	if len(items) == 0 {
		return nil
	}
	out := make([]*publishedv1.ShardWatermark, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, proto.Clone(item).(*publishedv1.ShardWatermark))
	}
	return out
}

func cloneArtifactRefs(items []*publishedv1.ArtifactRef) []*publishedv1.ArtifactRef {
	if len(items) == 0 {
		return nil
	}
	out := make([]*publishedv1.ArtifactRef, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, proto.Clone(item).(*publishedv1.ArtifactRef))
	}
	return out
}

func cloneObjectRefs(items []*publishedv1.ObjectRef) []*publishedv1.ObjectRef {
	if len(items) == 0 {
		return nil
	}
	out := make([]*publishedv1.ObjectRef, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, nil)
			continue
		}
		out = append(out, proto.Clone(item).(*publishedv1.ObjectRef))
	}
	return out
}
