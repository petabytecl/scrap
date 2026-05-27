package ulid_test

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"

	"github.com/petabytecl/scrap/internal/ulid"
)

func TestNewStringParseRoundTrip(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	id := gen.New()
	s := id.String()

	if len(s) != 26 {
		t.Fatalf("String() length: got %d, want 26", len(s))
	}

	parsed, err := ulid.Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	if parsed != id {
		t.Fatalf("round-trip mismatch: got %v, want %v", parsed, id)
	}
}

func TestTimeExtraction(t *testing.T) {
	var ts uint64 = 1716681600000
	clock := func() uint64 { return ts }
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	id := gen.New()
	if got := id.Time(); got != ts {
		t.Fatalf("Time(): got %d, want %d", got, ts)
	}

	ts = 1716681601500
	id2 := gen.New()
	if got := id2.Time(); got != ts {
		t.Fatalf("Time() after advance: got %d, want %d", got, ts)
	}
}

func TestMonotonicSameMillisecond(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	prev := gen.New()
	for i := range 1000 {
		next := gen.New()
		if next.String() <= prev.String() {
			t.Fatalf("iteration %d: %s is not > %s", i, next.String(), prev.String())
		}
		prev = next
	}
}

func TestLexicographicMatchesChronological(t *testing.T) {
	var ts uint64 = 1716681600000
	clock := func() uint64 { return ts }
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	early := gen.New()
	ts = 1716681601000
	mid := gen.New()
	ts = 1716681602000
	late := gen.New()

	if early.String() >= mid.String() {
		t.Fatalf("early >= mid: %s >= %s", early.String(), mid.String())
	}
	if mid.String() >= late.String() {
		t.Fatalf("mid >= late: %s >= %s", mid.String(), late.String())
	}

	sorted := []string{late.String(), early.String(), mid.String()}
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	if sorted[0] != early.String() || sorted[1] != mid.String() || sorted[2] != late.String() {
		t.Fatalf("lex sort mismatch: got %v", sorted)
	}
}

func TestCompare(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	a := gen.New()
	b := gen.New()

	selfCmp := a.Compare(a) //nolint:gocritic // intentional self-comparison to test reflexivity
	if selfCmp != 0 {
		t.Fatalf("Compare(self): got %d, want 0", selfCmp)
	}
	if a.Compare(b) != -1 {
		t.Fatalf("Compare(a, b): got %d, want -1", a.Compare(b))
	}
	if b.Compare(a) != 1 {
		t.Fatalf("Compare(b, a): got %d, want 1", b.Compare(a))
	}
}

func TestIsZero(t *testing.T) {
	var zero ulid.ULID
	if !zero.IsZero() {
		t.Fatal("zero value should be zero")
	}

	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))
	id := gen.New()
	if id.IsZero() {
		t.Fatal("generated ULID should not be zero")
	}
}

func TestBytes(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))
	id := gen.New()

	b := id.Bytes()
	if len(b) != 16 {
		t.Fatalf("Bytes() length: got %d, want 16", len(b))
	}

	var reconstructed ulid.ULID
	copy(reconstructed[:], b)
	if reconstructed != id {
		t.Fatalf("Bytes() round-trip mismatch")
	}
}

func TestMarshalTextJSON(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))
	id := gen.New()

	text, err := id.MarshalText()
	if err != nil {
		t.Fatalf("MarshalText: %v", err)
	}
	if string(text) != id.String() {
		t.Fatalf("MarshalText: got %q, want %q", text, id.String())
	}

	var parsed ulid.ULID
	if err := parsed.UnmarshalText(text); err != nil {
		t.Fatalf("UnmarshalText: %v", err)
	}
	if parsed != id {
		t.Fatalf("UnmarshalText round-trip mismatch")
	}

	type doc struct {
		ID ulid.ULID `json:"id"`
	}
	original := doc{ID: id}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(id.String())) {
		t.Fatalf("JSON should contain string representation, got %s", data)
	}

	var decoded doc
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if decoded.ID != id {
		t.Fatalf("JSON round-trip mismatch")
	}
}

func TestMarshalBinary(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))
	id := gen.New()

	data, err := id.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	if len(data) != 16 {
		t.Fatalf("MarshalBinary length: got %d, want 16", len(data))
	}

	var parsed ulid.ULID
	if err := parsed.UnmarshalBinary(data); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if parsed != id {
		t.Fatalf("UnmarshalBinary round-trip mismatch")
	}
}

func TestUnmarshalBinaryWrongSize(t *testing.T) {
	var u ulid.ULID
	if err := u.UnmarshalBinary([]byte{1, 2, 3}); err == nil {
		t.Fatal("UnmarshalBinary should reject wrong-size input")
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	if _, err := ulid.Parse("short"); err == nil {
		t.Fatal("Parse should reject short strings")
	}

	if _, err := ulid.Parse("0000000000000000000000000!"); err == nil {
		t.Fatal("Parse should reject invalid characters")
	}

	// I, L, O, U are excluded from Crockford Base32
	if _, err := ulid.Parse("0000000000000000000000000I"); err == nil {
		t.Fatal("Parse should reject 'I'")
	}
	if _, err := ulid.Parse("0000000000000000000000000L"); err == nil {
		t.Fatal("Parse should reject 'L'")
	}
	if _, err := ulid.Parse("0000000000000000000000000O"); err == nil {
		t.Fatal("Parse should reject 'O'")
	}
	if _, err := ulid.Parse("0000000000000000000000000U"); err == nil {
		t.Fatal("Parse should reject 'U'")
	}
}

func TestParseCaseInsensitive(t *testing.T) {
	clock := func() uint64 { return 1716681600000 }
	gen := ulid.NewGenerator(ulid.WithClock(clock))
	id := gen.New()

	upper := id.String()
	var buf []byte
	for _, c := range upper {
		if c >= 'A' && c <= 'Z' {
			buf = append(buf, byte(c+32)) //nolint:gosec // c is 'A'-'Z', c+32 fits in byte
		} else {
			buf = append(buf, byte(c)) //nolint:gosec // c is in Crockford Base32, fits in byte
		}
	}
	lower := string(buf)

	parsed, err := ulid.Parse(lower)
	if err != nil {
		t.Fatalf("Parse(lowercase): %v", err)
	}
	if parsed != id {
		t.Fatalf("case-insensitive round-trip mismatch")
	}
}

func TestConcurrentNewUniqueness(t *testing.T) {
	gen := ulid.NewGenerator()
	const goroutines = 10
	const idsPerGoroutine = 1000

	results := make([][]ulid.ULID, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := range goroutines {
		go func(idx int) {
			defer wg.Done()
			ids := make([]ulid.ULID, idsPerGoroutine)
			for i := range idsPerGoroutine {
				ids[i] = gen.New()
			}
			results[idx] = ids
		}(g)
	}
	wg.Wait()

	seen := make(map[ulid.ULID]struct{})
	for _, ids := range results {
		for _, id := range ids {
			if id.IsZero() {
				t.Fatal("generated zero ULID")
			}
			if _, ok := seen[id]; ok {
				t.Fatalf("duplicate ULID: %s", id.String())
			}
			seen[id] = struct{}{}
		}
	}

	if len(seen) != goroutines*idsPerGoroutine {
		t.Fatalf("unique count: got %d, want %d", len(seen), goroutines*idsPerGoroutine)
	}
}

func TestDeterministicClockProducesPredictableTimestamps(t *testing.T) {
	timestamps := []uint64{1000, 1000, 2000, 2000, 3000}
	idx := 0
	clock := func() uint64 {
		ts := timestamps[idx]
		idx++
		return ts
	}
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	ids := make([]ulid.ULID, len(timestamps))
	for i := range ids {
		ids[i] = gen.New()
	}

	if ids[0].Time() != 1000 {
		t.Fatalf("ids[0].Time(): got %d, want 1000", ids[0].Time())
	}
	if ids[1].Time() != 1000 {
		t.Fatalf("ids[1].Time(): got %d, want 1000", ids[1].Time())
	}
	if ids[2].Time() != 2000 {
		t.Fatalf("ids[2].Time(): got %d, want 2000", ids[2].Time())
	}
	if ids[4].Time() != 3000 {
		t.Fatalf("ids[4].Time(): got %d, want 3000", ids[4].Time())
	}

	for i := 1; i < len(ids); i++ {
		if ids[i].String() <= ids[i-1].String() {
			t.Fatalf("ids[%d] <= ids[%d]: %s <= %s", i, i-1, ids[i].String(), ids[i-1].String())
		}
	}
}

func TestPackageLevelNew(t *testing.T) {
	id := ulid.New()
	if id.IsZero() {
		t.Fatal("package-level New() returned zero")
	}
	if len(id.String()) != 26 {
		t.Fatalf("String() length: got %d, want 26", len(id.String()))
	}
}

func TestClockGoingBackwardMaintainsMonotonicity(t *testing.T) {
	timestamps := []uint64{5000, 3000, 2000, 6000}
	idx := 0
	clock := func() uint64 {
		ts := timestamps[idx]
		idx++
		return ts
	}
	gen := ulid.NewGenerator(ulid.WithClock(clock))

	ids := make([]ulid.ULID, len(timestamps))
	for i := range ids {
		ids[i] = gen.New()
	}

	// When clock goes backward, generator should clamp to lastTime
	if ids[1].Time() != 5000 {
		t.Fatalf("ids[1].Time(): got %d, want 5000 (clamped)", ids[1].Time())
	}
	if ids[2].Time() != 5000 {
		t.Fatalf("ids[2].Time(): got %d, want 5000 (clamped)", ids[2].Time())
	}
	// When clock advances past lastTime, use new time
	if ids[3].Time() != 6000 {
		t.Fatalf("ids[3].Time(): got %d, want 6000", ids[3].Time())
	}

	for i := 1; i < len(ids); i++ {
		if ids[i].String() <= ids[i-1].String() {
			t.Fatalf("ids[%d] <= ids[%d]: %s <= %s", i, i-1, ids[i].String(), ids[i-1].String())
		}
	}
}

func TestUnmarshalTextRejectsInvalid(t *testing.T) {
	var u ulid.ULID
	if err := u.UnmarshalText([]byte("too-short")); err == nil {
		t.Fatal("UnmarshalText should reject invalid input")
	}
}
