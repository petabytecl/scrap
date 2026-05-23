package cryptoenv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"sync"
)

type FakeTransit struct {
	mu          sync.Mutex
	versions    map[string]uint32
	missingKeys map[string]bool
	unavailable bool
	material    map[string]DataKey
}

func NewFakeTransit(versions map[string]uint32) *FakeTransit {
	out := &FakeTransit{
		versions:    make(map[string]uint32, len(versions)),
		missingKeys: make(map[string]bool),
		material:    make(map[string]DataKey),
	}
	for key, version := range versions {
		out.versions[key] = version
	}
	return out
}

func (f *FakeTransit) SetUnavailable(unavailable bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unavailable = unavailable
}

func (f *FakeTransit) SetMissingKey(keyID string, missing bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missingKeys[keyID] = missing
}

func (f *FakeTransit) GenerateDataKey(ctx context.Context, req GenerateDataKeyRequest) (DataKey, error) {
	if err := ctx.Err(); err != nil {
		return DataKey{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	version, err := f.keyVersion(req.KeyID)
	if err != nil {
		return DataKey{}, err
	}
	plain := fakeDigest("plain", req.KeyID, version, req.AAD, []byte(req.Algorithm))
	wrapped := fakeWrapped(req.KeyID, version, req.AAD, []byte(req.Algorithm), plain)
	key := DataKey{
		KeyID:        req.KeyID,
		KeyVersion:   version,
		PlaintextDEK: plain,
		WrappedDEK:   wrapped,
	}
	f.material[wrappedKey(wrapped)] = cloneDataKey(key)
	return cloneDataKey(key), nil
}

func (f *FakeTransit) UnwrapDataKey(ctx context.Context, req UnwrapDataKeyRequest) (DataKey, error) {
	if err := ctx.Err(); err != nil {
		return DataKey{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.keyVersion(req.KeyID); err != nil {
		return DataKey{}, err
	}
	key, ok := f.material[wrappedKey(req.WrappedDEK)]
	if !ok ||
		key.KeyID != req.KeyID ||
		key.KeyVersion != req.KeyVersion ||
		!bytes.Equal(key.WrappedDEK, req.WrappedDEK) {
		return DataKey{}, fmt.Errorf("%w: key %s version %d", ErrKeyMaterialUnavailable, req.KeyID, req.KeyVersion)
	}
	return cloneDataKey(key), nil
}

func (f *FakeTransit) RewrapDataKey(ctx context.Context, req RewrapDataKeyRequest) (WrappedKey, error) {
	if err := ctx.Err(); err != nil {
		return WrappedKey{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, err := f.keyVersion(req.SourceKeyID); err != nil {
		return WrappedKey{}, err
	}
	destinationVersion, err := f.keyVersion(req.DestinationKeyID)
	if err != nil {
		return WrappedKey{}, err
	}
	source, ok := f.material[wrappedKey(req.WrappedDEK)]
	if !ok ||
		source.KeyID != req.SourceKeyID ||
		source.KeyVersion != req.SourceKeyVersion {
		return WrappedKey{}, fmt.Errorf("%w: source key %s version %d", ErrKeyMaterialUnavailable, req.SourceKeyID, req.SourceKeyVersion)
	}
	wrapped := fakeWrapped(req.DestinationKeyID, destinationVersion, req.AAD, []byte(req.Algorithm), source.PlaintextDEK)
	key := DataKey{
		KeyID:        req.DestinationKeyID,
		KeyVersion:   destinationVersion,
		PlaintextDEK: append([]byte(nil), source.PlaintextDEK...),
		WrappedDEK:   wrapped,
	}
	f.material[wrappedKey(wrapped)] = cloneDataKey(key)
	return WrappedKey{
		KeyID:      req.DestinationKeyID,
		KeyVersion: destinationVersion,
		WrappedDEK: append([]byte(nil), wrapped...),
	}, nil
}

func (f *FakeTransit) keyVersion(keyID string) (uint32, error) {
	if f.unavailable {
		return 0, fmt.Errorf("%w: fake transit unavailable", ErrUnavailable)
	}
	if keyID == "" || f.missingKeys[keyID] {
		return 0, fmt.Errorf("%w: key %s", ErrKeyMaterialUnavailable, keyID)
	}
	version := f.versions[keyID]
	if version == 0 {
		version = 1
		f.versions[keyID] = version
	}
	return version, nil
}

func fakeWrapped(keyID string, version uint32, aad []byte, algorithm []byte, plain []byte) []byte {
	first := fakeDigest("wrapped-a", keyID, version, aad, algorithm, plain)
	second := fakeDigest("wrapped-b", keyID, version, aad, algorithm, plain)
	out := make([]byte, 0, len(first)+len(second))
	out = append(out, first...)
	out = append(out, second...)
	return out
}

func fakeDigest(label string, keyID string, version uint32, parts ...[]byte) []byte {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(label))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(keyID))
	_, _ = hasher.Write([]byte{0})
	_, _ = hasher.Write([]byte(strconv.FormatUint(uint64(version), 10)))
	for _, part := range parts {
		_, _ = hasher.Write([]byte{0})
		_, _ = hasher.Write(part)
	}
	return hasher.Sum(nil)
}

func wrappedKey(wrapped []byte) string {
	return hex.EncodeToString(wrapped)
}

func cloneDataKey(key DataKey) DataKey {
	return DataKey{
		KeyID:        key.KeyID,
		KeyVersion:   key.KeyVersion,
		PlaintextDEK: append([]byte(nil), key.PlaintextDEK...),
		WrappedDEK:   append([]byte(nil), key.WrappedDEK...),
	}
}
