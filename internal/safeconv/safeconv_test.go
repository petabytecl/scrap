package safeconv

import (
	"math"
	"testing"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestCheckedConversionsAcceptBoundaryValues(t *testing.T) {
	requireUint64ToInt64(t, math.MaxInt64, math.MaxInt64)
	requireUint64ToInt(t, math.MaxInt, math.MaxInt)
	requireInt64ToUint64(t, math.MaxInt64, math.MaxInt64)
	requireIntToUint64(t, math.MaxInt, math.MaxInt)
	requireUint64ToUint32(t, math.MaxUint32, math.MaxUint32)
	requireIntToUint32(t, math.MaxInt32, math.MaxInt32)
	requireUint32ToUint16(t, math.MaxUint16, math.MaxUint16)
	requireInt32ToUint16(t, math.MaxUint16, math.MaxUint16)
}

func requireUint64ToInt64(t *testing.T, in uint64, want int64) {
	t.Helper()
	got, err := Uint64ToInt64("offset", in)
	testutil.RequireNoErrorf(t, err, "Uint64ToInt64")
	testutil.RequireEqualf(t, want, got, "Uint64ToInt64 boundary")
}

func requireUint64ToInt(t *testing.T, in uint64, want int) {
	t.Helper()
	got, err := Uint64ToInt("offset", in)
	testutil.RequireNoErrorf(t, err, "Uint64ToInt")
	testutil.RequireEqualf(t, want, got, "Uint64ToInt boundary")
}

func requireInt64ToUint64(t *testing.T, in int64, want uint64) {
	t.Helper()
	got, err := Int64ToUint64("size", in)
	testutil.RequireNoErrorf(t, err, "Int64ToUint64")
	testutil.RequireEqualf(t, want, got, "Int64ToUint64 boundary")
}

func requireIntToUint64(t *testing.T, in int, want uint64) {
	t.Helper()
	got, err := IntToUint64("count", in)
	testutil.RequireNoErrorf(t, err, "IntToUint64")
	testutil.RequireEqualf(t, want, got, "IntToUint64 boundary")
}

func requireUint64ToUint32(t *testing.T, in uint64, want uint32) {
	t.Helper()
	got, err := Uint64ToUint32("frame size", in)
	testutil.RequireNoErrorf(t, err, "Uint64ToUint32")
	testutil.RequireEqualf(t, want, got, "Uint64ToUint32 boundary")
}

func requireIntToUint32(t *testing.T, in int, want uint32) {
	t.Helper()
	got, err := IntToUint32("payload size", in)
	testutil.RequireNoErrorf(t, err, "IntToUint32")
	testutil.RequireEqualf(t, want, got, "IntToUint32 boundary")
}

func requireUint32ToUint16(t *testing.T, in uint32, want uint16) {
	t.Helper()
	got, err := Uint32ToUint16("enum", in)
	testutil.RequireNoErrorf(t, err, "Uint32ToUint16")
	testutil.RequireEqualf(t, want, got, "Uint32ToUint16 boundary")
}

func requireInt32ToUint16(t *testing.T, in int32, want uint16) {
	t.Helper()
	got, err := Int32ToUint16("enum", in)
	testutil.RequireNoErrorf(t, err, "Int32ToUint16")
	testutil.RequireEqualf(t, want, got, "Int32ToUint16 boundary")
}

func TestCheckedConversionsRejectOutOfRangeValues(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{name: "uint64 to int64 overflow", run: func() error { _, err := Uint64ToInt64("offset", math.MaxInt64+1); return err }},
		{name: "uint64 to int overflow", run: func() error { _, err := Uint64ToInt("offset", uint64(math.MaxInt)+1); return err }},
		{name: "negative int64 to uint64", run: func() error { _, err := Int64ToUint64("size", -1); return err }},
		{name: "negative int to uint64", run: func() error { _, err := IntToUint64("count", -1); return err }},
		{name: "uint64 to uint32 overflow", run: func() error { _, err := Uint64ToUint32("frame size", math.MaxUint32+1); return err }},
		{name: "int to uint32 negative", run: func() error { _, err := IntToUint32("payload size", -1); return err }},
		{name: "uint32 to uint16 overflow", run: func() error { _, err := Uint32ToUint16("enum", math.MaxUint16+1); return err }},
		{name: "int32 to uint16 negative", run: func() error { _, err := Int32ToUint16("enum", -1); return err }},
		{name: "int32 to uint16 overflow", run: func() error { _, err := Int32ToUint16("enum", math.MaxUint16+1); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("conversion succeeded, want error")
			}
		})
	}
}
