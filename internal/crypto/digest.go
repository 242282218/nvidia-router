package crypto

import (
	"crypto/hmac"
	"crypto/sha256"
)

func (keys *KeySet) Fingerprint(value []byte) []byte {
	return digest(keys.fingerprintKey[:], value)
}

func (keys *KeySet) AccessKeyDigest(value []byte) []byte {
	return digest(keys.accessKeyKey[:], value)
}

func (keys *KeySet) SessionDigest(value []byte) []byte {
	return digest(keys.sessionKey[:], value)
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
