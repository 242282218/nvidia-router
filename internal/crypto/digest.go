package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

func (keys *KeySet) Fingerprint(value []byte) []byte {
	return keys.FingerprintVersion(keys.ActiveVersion(), value)
}

func (keys *KeySet) FingerprintVersion(version int, value []byte) []byte {
	return keys.digestVersion(version, func(derived derivedKeys) []byte { return derived.fingerprintKey[:] }, value)
}

func (keys *KeySet) AccessKeyDigest(value []byte) []byte {
	return keys.AccessKeyDigestVersion(keys.ActiveVersion(), value)
}

func (keys *KeySet) AccessKeyDigestVersion(version int, value []byte) []byte {
	return keys.digestVersion(version, func(derived derivedKeys) []byte { return derived.accessKeyKey[:] }, value)
}

func (keys *KeySet) SessionDigest(value []byte) []byte {
	return keys.SessionDigestVersion(keys.ActiveVersion(), value)
}

func (keys *KeySet) SessionDigestVersion(version int, value []byte) []byte {
	return keys.digestVersion(version, func(derived derivedKeys) []byte { return derived.sessionKey[:] }, value)
}

func (keys *KeySet) digestVersion(version int, selectKey func(derivedKeys) []byte, value []byte) []byte {
	if keys == nil {
		return nil
	}
	derived, ok := keys.versions[version]
	if !ok {
		return nil
	}
	return digest(selectKey(derived), value)
}

func (keys *KeySet) digestVersions(selectKey func(derivedKeys) []byte, value []byte) map[int][]byte {
	result := make(map[int][]byte, len(keys.versions))
	for version := range keys.versions {
		result[version] = keys.digestVersion(version, selectKey, value)
	}
	return result
}

func (keys *KeySet) AccessKeyDigests(value []byte) map[int][]byte {
	return keys.digestVersions(func(derived derivedKeys) []byte { return derived.accessKeyKey[:] }, value)
}

func (keys *KeySet) SessionDigests(value []byte) map[int][]byte {
	return keys.digestVersions(func(derived derivedKeys) []byte { return derived.sessionKey[:] }, value)
}

func (keys *KeySet) Fingerprints(value []byte) map[int][]byte {
	return keys.digestVersions(func(derived derivedKeys) []byte { return derived.fingerprintKey[:] }, value)
}

func digest(key, value []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(value)
	return mac.Sum(nil)
}

// Zero overwrites the provided bytes. Go's garbage collector cannot guarantee physical memory erasure.
func Zero(secret []byte) {
	clear(secret)
}
