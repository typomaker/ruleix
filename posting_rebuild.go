package ruleix

import (
	"math"

	"github.com/RoaringBitmap/roaring/v2"
)

// postingGeneration is mutable build-only state. A successful rebuild returns
// an independent generation, allowing its owner to publish it with one
// assignment and release the old map afterwards.
type postingGeneration[K comparable] map[K]*roaring.Bitmap

// rebuildPostingGeneration transforms every old key and merges postings that
// collide at the coarser precision. The returned accounting includes the map
// entry charge and serialized bitmap bytes. It never mutates old.
func rebuildPostingGeneration[K comparable](
	old postingGeneration[K],
	capacity int,
	entryBytes uint64,
	coarsen func(K) K,
) (postingGeneration[K], uint64, bool) {
	if capacity < 0 {
		return nil, 0, false
	}
	next := make(postingGeneration[K], min(len(old), capacity))
	for key, bits := range old {
		coarser := coarsen(key)
		if merged := next[coarser]; merged != nil {
			merged.Or(bits)
		} else if bits != nil {
			next[coarser] = bits.Clone()
		}
	}

	usage := uint64(0)
	for _, bits := range next {
		if math.MaxUint64-usage < entryBytes {
			return nil, 0, false
		}
		usage += entryBytes
		bytes := bitmapBytes(bits)
		if math.MaxUint64-usage < bytes {
			return nil, 0, false
		}
		usage += bytes
	}
	return next, usage, true
}
