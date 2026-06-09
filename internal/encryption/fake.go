package encryption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

const (
	defaultFakeKeyName = "scrap-documents"
	fakeWrappedPrefix  = "fake-transit:v"
	fakeWrappedParts   = 4
	dataKeyBits128     = 128
	dataKeyBits256     = 256
	dataKeyBits512     = 512
)

type FakeConfig struct {
	KeyName        string
	Unavailable    bool
	AuthDenied     bool
	MissingKey     bool
	MinimumVersion int
}

type FakeTransit struct {
	mu             sync.Mutex
	keyName        string
	unavailable    bool
	authDenied     bool
	missingKey     bool
	currentVersion int
	minimumVersion int
	sequence       uint64
	keys           map[string]fakeDataKey
}

type fakeDataKey struct {
	plaintext []byte
	context   string
	version   int
	bits      int
	seed      uint64
}

func NewFakeTransit(cfg FakeConfig) *FakeTransit {
	keyName := strings.TrimSpace(cfg.KeyName)
	if keyName == "" {
		keyName = defaultFakeKeyName
	}
	minimumVersion := cfg.MinimumVersion
	if minimumVersion < 1 {
		minimumVersion = 1
	}
	return &FakeTransit{
		keyName:        keyName,
		unavailable:    cfg.Unavailable,
		authDenied:     cfg.AuthDenied,
		missingKey:     cfg.MissingKey,
		currentVersion: 1,
		minimumVersion: minimumVersion,
		keys:           map[string]fakeDataKey{},
	}
}

func (*FakeTransit) ProductionCapable() bool {
	return false
}

func (t *FakeTransit) GenerateDataKey(_ context.Context, req GenerateDataKeyRequest) (DataKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.stateError(); err != nil {
		return DataKey{}, err
	}
	keyBytes, err := requestedKeyBytes(req.Bits)
	if err != nil {
		return DataKey{}, err
	}
	t.sequence++
	seed := t.sequence
	bits := dataKeyBits(req.Bits)
	plaintext := t.derivePlaintext(req.Context, keyBytes, seed)
	wrappedKey := t.wrapLocked(t.currentVersion, req.Context, bits, seed)
	return DataKey{
		Plaintext:  cloneBytes(plaintext),
		WrappedKey: wrappedKey,
		Version:    t.currentVersion,
	}, nil
}

func (t *FakeTransit) UnwrapDataKey(_ context.Context, req UnwrapDataKeyRequest) (UnwrappedDataKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, err := t.lookupLocked(req.WrappedKey, req.Context)
	if err != nil {
		return UnwrappedDataKey{}, err
	}
	return UnwrappedDataKey{Plaintext: cloneBytes(key.plaintext), Version: key.version}, nil
}

func (t *FakeTransit) RewrapDataKey(_ context.Context, req RewrapDataKeyRequest) (RewrappedKey, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, err := t.lookupLocked(req.WrappedKey, req.Context)
	if err != nil {
		return RewrappedKey{}, err
	}
	targetVersion := t.currentVersion
	if req.KeyVersion > 0 {
		targetVersion = req.KeyVersion
	}
	if targetVersion > t.currentVersion {
		return RewrappedKey{}, fmt.Errorf("transit fake future key version: %w", ErrInvalidRequest)
	}
	if key.version >= targetVersion {
		return RewrappedKey{WrappedKey: req.WrappedKey, Version: key.version}, nil
	}
	wrappedKey := t.wrapLocked(targetVersion, req.Context, key.bits, key.seed)
	return RewrappedKey{WrappedKey: wrappedKey, Version: targetVersion, Changed: true}, nil
}

func (t *FakeTransit) Readiness(context.Context) (Readiness, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.stateError(); err != nil {
		return Readiness{}, err
	}
	return Readiness{
		Ready:                    true,
		LatestVersion:            t.currentVersion,
		MinimumDecryptionVersion: t.minimumVersion,
	}, nil
}

func (t *FakeTransit) Rotate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.currentVersion++
}

func (t *FakeTransit) RequireMinimumVersion(version int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if version < 1 {
		version = 1
	}
	t.minimumVersion = version
}

func (t *FakeTransit) stateError() error {
	switch {
	case t.unavailable:
		return fmt.Errorf("transit fake unavailable: %w", ErrUnavailable)
	case t.authDenied:
		return fmt.Errorf("transit fake denied: %w", ErrAuthDenied)
	case t.missingKey:
		return fmt.Errorf("transit fake missing key: %w", ErrMissingKey)
	default:
		return nil
	}
}

func (t *FakeTransit) lookupLocked(wrappedKey string, context []byte) (fakeDataKey, error) {
	if err := t.stateError(); err != nil {
		return fakeDataKey{}, err
	}
	key, ok, err := t.parseWrappedKey(wrappedKey, context)
	if err != nil {
		return fakeDataKey{}, err
	}
	if !ok {
		key, ok = t.keys[wrappedKey]
	}
	if !ok {
		return fakeDataKey{}, fmt.Errorf("transit fake missing key: %w", ErrMissingKey)
	}
	if key.version < t.minimumVersion {
		return fakeDataKey{}, fmt.Errorf("transit fake minimum version: %w", ErrMinimumVersion)
	}
	if key.context != contextHandle(context) {
		return fakeDataKey{}, fmt.Errorf("transit fake context mismatch: %w", ErrAuthDenied)
	}
	return key, nil
}

func (t *FakeTransit) parseWrappedKey(wrappedKey string, context []byte) (fakeDataKey, bool, error) {
	if !strings.HasPrefix(wrappedKey, fakeWrappedPrefix) {
		return fakeDataKey{}, false, nil
	}
	rest := strings.TrimPrefix(wrappedKey, fakeWrappedPrefix)
	parts := strings.Split(rest, ":")
	if len(parts) != fakeWrappedParts {
		return fakeDataKey{}, false, fmt.Errorf("transit fake wrapped key parts: got %d, want %d", len(parts), fakeWrappedParts)
	}
	version, err := strconv.Atoi(parts[0])
	if err != nil {
		return fakeDataKey{}, false, fmt.Errorf("transit fake wrapped key version: %w", err)
	}
	if version < 1 {
		return fakeDataKey{}, false, fmt.Errorf("transit fake wrapped key version %d is invalid", version)
	}
	bits, err := strconv.Atoi(parts[1])
	if err != nil {
		return fakeDataKey{}, false, fmt.Errorf("transit fake wrapped key bits: %w", err)
	}
	keyBytes, err := requestedKeyBytes(bits)
	if err != nil {
		return fakeDataKey{}, false, err
	}
	seed, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return fakeDataKey{}, false, fmt.Errorf("transit fake wrapped key seed: %w", err)
	}
	if seed == 0 {
		return fakeDataKey{}, false, errors.New("transit fake wrapped key seed is required")
	}
	if parts[3] != t.wrapMAC(version, bits, context, seed) {
		return fakeDataKey{}, false, fmt.Errorf("transit fake context mismatch: %w", ErrAuthDenied)
	}
	plaintext := t.derivePlaintext(context, keyBytes, seed)
	return fakeDataKey{
		plaintext: plaintext,
		context:   contextHandle(context),
		version:   version,
		bits:      bits,
		seed:      seed,
	}, true, nil
}

func (t *FakeTransit) derivePlaintext(context []byte, size int, seed uint64) []byte {
	plaintext := make([]byte, 0, size)
	for len(plaintext) < size {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%d", t.keyName, contextHandle(context), seed, len(plaintext))))
		plaintext = append(plaintext, sum[:]...)
	}
	return plaintext[:size]
}

func (t *FakeTransit) wrapLocked(version int, context []byte, bits int, seed uint64) string {
	wrappedKey := fmt.Sprintf("%s%d:%d:%d:%s", fakeWrappedPrefix, version, bits, seed, t.wrapMAC(version, bits, context, seed))
	t.keys[wrappedKey] = fakeDataKey{
		plaintext: t.derivePlaintext(context, bits/bitsPerByte, seed),
		context:   contextHandle(context),
		version:   version,
		bits:      bits,
		seed:      seed,
	}
	return wrappedKey
}

func (t *FakeTransit) wrapMAC(version, bits int, context []byte, seed uint64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s|%d", t.keyName, version, bits, contextHandle(context), seed)))
	return hex.EncodeToString(sum[:8])
}

func requestedKeyBytes(bits int) (int, error) {
	bits = dataKeyBits(bits)
	switch bits {
	case dataKeyBits128, dataKeyBits256, dataKeyBits512:
		return bits / bitsPerByte, nil
	default:
		return 0, fmt.Errorf("transit data key bits must be 128, 256, or 512: %w", ErrInvalidRequest)
	}
}

func contextHandle(context []byte) string {
	if len(context) == 0 {
		return ""
	}
	sum := sha256.Sum256(context)
	return hex.EncodeToString(sum[:8])
}

func versionFromWrappedKey(wrappedKey string) int {
	if strings.HasPrefix(wrappedKey, fakeWrappedPrefix) {
		rest := strings.TrimPrefix(wrappedKey, fakeWrappedPrefix)
		rawVersion, _, _ := strings.Cut(rest, ":")
		version, _ := strconv.Atoi(rawVersion)
		return version
	}
	if strings.HasPrefix(wrappedKey, "vault:v") {
		rest := strings.TrimPrefix(wrappedKey, "vault:v")
		rawVersion, _, _ := strings.Cut(rest, ":")
		version, _ := strconv.Atoi(rawVersion)
		return version
	}
	return 0
}
