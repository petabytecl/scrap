package evidencebundle

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPrivacyScanCoversDataRoot(t *testing.T) {
	// /data is the default --data-dir and roots every Block/openlog/pebble path,
	// so the privacy-scan gate must treat a /data/ host path as sensitive even
	// though the log redaction allowlist never copies free text into the bundle.
	dataPath := "/data/shards/3/blocks/000000000000002a.blk"

	patterns := bundlePrivacyPatterns()
	findings := scanPrivacyArtifact("logs/scrapd.json", []byte(`{"msg":"`+dataPath+`"}`), patterns)
	if len(findings) == 0 {
		t.Fatalf("privacy scan found no findings for a /data/ host path in %q", dataPath)
	}
	var sawHostPath bool
	for _, f := range findings {
		if f.Pattern == "host_path_shape" {
			sawHostPath = true
		}
	}
	if !sawHostPath {
		t.Fatalf("privacy scan findings = %+v, want a host_path_shape match", findings)
	}
}

func TestPrivacyScanCoversAllHostPathRoots(t *testing.T) {
	patterns := bundlePrivacyPatterns()
	for _, root := range sensitiveHostPathRoots {
		path := "/" + root + "/x/y"
		if len(scanPrivacyArtifact("a.json", []byte(path), patterns)) == 0 {
			t.Errorf("privacy scan missed host root %q", root)
		}
	}
}

func TestRedactedLogResponseAllowlistsKnownShapeOnly(t *testing.T) {
	body := []byte(`{
		"status": "success",
		"data": {
			"resultType": "streams",
			"result": [{
				"stream": {
					"service_name": "scrapd",
					"level": "info",
					"log_file_path": "/var/log/containers/scrapd.log",
					"pod": "scrapd-0"
				},
				"values": [
					["1780056000000000000", "wrote /data/shards/3/blocks/000000000000002a.blk token=SECRET"]
				]
			}]
		},
		"stats": {"summary": {"totalBytesProcessed": 12}}
	}`)

	data, err := json.Marshal(redactedLogResponse(body))
	if err != nil {
		t.Fatalf("marshal redacted response: %v", err)
	}
	text := string(data)
	for _, leak := range []string{"/var/log", "/data/", "SECRET", "log_file_path", "pod", "stats", "totalBytesProcessed"} {
		if strings.Contains(text, leak) {
			t.Fatalf("redacted response leaked %q: %s", leak, text)
		}
	}
	for _, keep := range []string{`"service_name":"scrapd"`, `"level":"info"`, "1780056000000000000", `"status":"success"`, `"resultType":"streams"`} {
		if !strings.Contains(text, keep) {
			t.Fatalf("redacted response missing allowlisted %q: %s", keep, text)
		}
	}
}

func TestRedactedLogResponseFailsClosedOnUnrecognizedShape(t *testing.T) {
	cases := map[string]string{
		"not json":               `{{`,
		"non-string label value": `{"status":"success","data":{"resultType":"streams","result":[{"stream":{"level":5},"values":[]}]}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			redacted, ok := redactedLogResponse([]byte(body)).(map[string]any)
			if !ok {
				t.Fatalf("redacted response type = %T, want map", redactedLogResponse([]byte(body)))
			}
			if redacted["parse_error"] != "unrecognized_response_shape" {
				t.Fatalf("redacted response = %+v, want fail-closed parse_error", redacted)
			}
		})
	}
}

func TestRedactedLogResponseRedactsUnknownEnumsAndBadTimestamps(t *testing.T) {
	body := []byte(`{
		"status": "partial",
		"data": {
			"resultType": "surprise",
			"result": [{
				"stream": {"service_name": "scrapd/../../etc"},
				"values": [["/home/user/leak", "line"]]
			}]
		}
	}`)

	data, err := json.Marshal(redactedLogResponse(body))
	if err != nil {
		t.Fatalf("marshal redacted response: %v", err)
	}
	text := string(data)
	for _, leak := range []string{"partial", "surprise", "/home/user/leak", "etc", "line"} {
		if strings.Contains(text, leak) {
			t.Fatalf("redacted response leaked %q: %s", leak, text)
		}
	}
}
