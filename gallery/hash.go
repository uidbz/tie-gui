package gallery

import (
	"encoding/hex"
	"io"

	"github.com/minio/highwayhash"
)

// hashKey is the fixed key tie uses for its content addresses. Hashing local
// files with the same key means a file that also exists in tie gets the same
// address, so the local thumbnail cache and the tie stores agree on keys.
var hashKey []byte

// Same key as tie's metadata package (its tieKey constant).
const galleryKey = "A00102030405060708090A0B0C0D0E0FF0E0D0C0B0A090807060504030201000"

func init() {
	k, err := hex.DecodeString(galleryKey)
	if err != nil {
		panic("gallery: invalid key constant: " + err.Error())
	}
	hashKey = k
}

// contentHash returns the tie-compatible content address of everything read
// from r. It is used as the cache key for thumbnails of local files.
func contentHash(r io.Reader) (string, error) {
	h, err := highwayhash.New(hashKey)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
