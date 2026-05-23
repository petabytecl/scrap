package safeconv

import (
	"fmt"
	"math"
)

func Uint64ToInt64(name string, value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("%s %d exceeds int64 max", name, value)
	}
	// #nosec G115 -- range checked above.
	return int64(value), nil
}

func Uint64ToInt(name string, value uint64) (int, error) {
	if value > math.MaxInt {
		return 0, fmt.Errorf("%s %d exceeds int max", name, value)
	}
	// #nosec G115 -- range checked above.
	return int(value), nil
}

func Int64ToUint64(name string, value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s %d is negative", name, value)
	}
	// #nosec G115 -- range checked above.
	return uint64(value), nil
}

func IntToUint64(name string, value int) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s %d is negative", name, value)
	}
	// #nosec G115 -- range checked above.
	return uint64(value), nil
}

func Uint64ToUint32(name string, value uint64) (uint32, error) {
	if value > math.MaxUint32 {
		return 0, fmt.Errorf("%s %d exceeds uint32 max", name, value)
	}
	// #nosec G115 -- range checked above.
	return uint32(value), nil
}

func IntToUint32(name string, value int) (uint32, error) {
	converted, err := IntToUint64(name, value)
	if err != nil {
		return 0, err
	}
	return Uint64ToUint32(name, converted)
}

func Uint32ToUint16(name string, value uint32) (uint16, error) {
	if value > math.MaxUint16 {
		return 0, fmt.Errorf("%s %d exceeds uint16 max", name, value)
	}
	// #nosec G115 -- range checked above.
	return uint16(value), nil
}

func Int32ToUint16(name string, value int32) (uint16, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s %d is negative", name, value)
	}
	if value > math.MaxUint16 {
		return 0, fmt.Errorf("%s %d exceeds uint16 max", name, value)
	}
	// #nosec G115 -- range checked above.
	return uint16(value), nil
}
