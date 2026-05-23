package safeconv

import (
	"math"
	"testing"
)

func TestCheckedConversionsAcceptBoundaryValues(t *testing.T) {
	if got, err := Uint64ToInt64("offset", math.MaxInt64); err != nil || got != math.MaxInt64 {
		t.Fatalf("Uint64ToInt64 boundary = %d, %v", got, err)
	}
	if got, err := Uint64ToInt("offset", math.MaxInt); err != nil || got != math.MaxInt {
		t.Fatalf("Uint64ToInt boundary = %d, %v", got, err)
	}
	if got, err := Int64ToUint64("size", math.MaxInt64); err != nil || got != math.MaxInt64 {
		t.Fatalf("Int64ToUint64 boundary = %d, %v", got, err)
	}
	if got, err := IntToUint64("count", math.MaxInt); err != nil || got != math.MaxInt {
		t.Fatalf("IntToUint64 boundary = %d, %v", got, err)
	}
	if got, err := Uint64ToUint32("frame size", math.MaxUint32); err != nil || got != math.MaxUint32 {
		t.Fatalf("Uint64ToUint32 boundary = %d, %v", got, err)
	}
	if got, err := IntToUint32("payload size", math.MaxInt32); err != nil || got != math.MaxInt32 {
		t.Fatalf("IntToUint32 boundary = %d, %v", got, err)
	}
	if got, err := Uint32ToUint16("enum", math.MaxUint16); err != nil || got != math.MaxUint16 {
		t.Fatalf("Uint32ToUint16 boundary = %d, %v", got, err)
	}
	if got, err := Int32ToUint16("enum", math.MaxUint16); err != nil || got != math.MaxUint16 {
		t.Fatalf("Int32ToUint16 boundary = %d, %v", got, err)
	}
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
