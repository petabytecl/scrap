package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/petabytecl/scrap/internal/telemetry"
)

func TestNewResourceIncludesScrapIdentityAndBuildAttributes(t *testing.T) {
	res, err := telemetry.NewResource(context.Background(), telemetry.ResourceConfig{
		ServiceName:  "scrapd",
		Environment:  "stress",
		InstanceID:   "scrapd-explicit",
		Version:      "v2.3.4",
		BuildSHA:     "abc123",
		BuildTime:    "2026-05-28T12:00:00Z",
		CellID:       "cell-a",
		MemberSlotID: "scrapd-0",
		MemberID:     "member-123",
		ShardID:      7,
		RaftID:       3,
	})
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	attrs := attrMap(res.Attributes())
	assertAttr(t, attrs, "service.name", "scrapd")
	assertAttr(t, attrs, "service.instance.id", "scrapd-explicit")
	assertAttr(t, attrs, "service.version", "v2.3.4")
	assertAttr(t, attrs, "deployment.environment", "stress")
	assertAttr(t, attrs, "scrap.build.sha", "abc123")
	assertAttr(t, attrs, "scrap.build.time", "2026-05-28T12:00:00Z")
	assertAttr(t, attrs, "scrap.cell_id", "cell-a")
	assertAttr(t, attrs, "scrap.member_slot_id", "scrapd-0")
	assertAttr(t, attrs, "scrap.member_id", "member-123")
	assertAttr(t, attrs, "scrap.shard_id", "7")
	assertAttr(t, attrs, "scrap.raft_id", "3")

	for _, forbidden := range []string{"transaction_id", "document_name"} {
		if _, ok := attrs[attribute.Key(forbidden)]; ok {
			t.Fatalf("resource contains high-cardinality attribute %q", forbidden)
		}
	}
}

func TestNewResourceDerivesServiceInstanceID(t *testing.T) {
	tests := map[string]struct {
		cfg  telemetry.ResourceConfig
		want string
	}{
		"explicit instance ID wins": {
			cfg: telemetry.ResourceConfig{
				InstanceID:   "scrapd-explicit",
				MemberSlotID: "scrapd-0",
				MemberID:     "member-123",
			},
			want: "scrapd-explicit",
		},
		"member slot identity": {
			cfg: telemetry.ResourceConfig{
				MemberSlotID: "scrapd-0",
				MemberID:     "member-123",
			},
			want: "scrapd-0",
		},
		"durable member identity": {
			cfg: telemetry.ResourceConfig{
				MemberID: "member-123",
			},
			want: "member-123",
		},
		"local fallback": {
			cfg:  telemetry.ResourceConfig{},
			want: "local",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			res, err := telemetry.NewResource(context.Background(), tt.cfg)
			if err != nil {
				t.Fatalf("NewResource: %v", err)
			}

			attrs := attrMap(res.Attributes())
			assertAttr(t, attrs, "service.instance.id", tt.want)
		})
	}
}

func TestNewResourceDefaultsServiceNameAndAcceptsNilContext(t *testing.T) {
	//nolint:staticcheck // Verifies NewResource handles a nil context defensively.
	res, err := telemetry.NewResource(nil, telemetry.ResourceConfig{})
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}

	attrs := attrMap(res.Attributes())
	assertAttr(t, attrs, "service.name", "scrapd")
	if _, ok := attrs[attribute.Key("scrap.build.sha")]; ok {
		t.Fatal("empty build SHA should not be emitted")
	}
	if _, ok := attrs[attribute.Key("scrap.build.time")]; ok {
		t.Fatal("empty build time should not be emitted")
	}
}

func attrMap(kvs []attribute.KeyValue) map[attribute.Key]attribute.Value {
	attrs := make(map[attribute.Key]attribute.Value, len(kvs))
	for _, kv := range kvs {
		attrs[kv.Key] = kv.Value
	}
	return attrs
}

func assertAttr(t *testing.T, attrs map[attribute.Key]attribute.Value, key string, want any) {
	t.Helper()

	got, ok := attrs[attribute.Key(key)]
	if !ok {
		t.Fatalf("missing attribute %q", key)
	}

	switch want := want.(type) {
	case string:
		if got.AsString() != want {
			t.Fatalf("attribute %q = %q, want %q", key, got.AsString(), want)
		}
	default:
		t.Fatalf("unsupported assertion type %T", want)
	}
}
