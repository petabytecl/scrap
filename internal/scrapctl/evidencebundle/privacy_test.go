package evidencebundle

import "testing"

func TestRedactionCoversDataRoot(t *testing.T) {
	// /data is the default --data-dir and roots every Block/openlog/pebble path,
	// so both redaction layers must treat a /data/ host path as sensitive. This
	// guards against the two denylists drifting apart or dropping the data root.
	dataPath := "/data/shards/3/blocks/000000000000002a.blk"

	if !containsSensitiveLogString(dataPath) {
		t.Fatalf("containsSensitiveLogString(%q) = false, want true", dataPath)
	}

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

func TestRedactionLayersShareHostPathRoots(t *testing.T) {
	// Every root the log denylist recognizes must also be caught by the privacy
	// scan, so a redacted-clean bundle cannot still contain a flagged root.
	patterns := bundlePrivacyPatterns()
	for _, root := range sensitiveHostPathRoots {
		path := "/" + root + "/x/y"
		if !containsSensitiveLogString(path) {
			t.Errorf("containsSensitiveLogString(%q) = false, want true", path)
		}
		if len(scanPrivacyArtifact("a.json", []byte(path), patterns)) == 0 {
			t.Errorf("privacy scan missed host root %q", root)
		}
	}
}
