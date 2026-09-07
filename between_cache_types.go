package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type betweenCache[V any] struct {
	entries   [2]betweenCacheEntry[V]
	seen      *betweenCacheSeen[V]
	overflow  *betweenCacheOverflow[V]
	next      uint8
	pressure  uint8
	misses    uint8
	observers *cacheObservers
}

type betweenCacheOverflow[V any] struct {
	entries [2]betweenCacheEntry[V]
	used    [4]uint64
	clock   uint64
}

func newBetweenCache[V any](pool *bitmapPool, id ...nodeID) *betweenCache[V] {
	if len(id) != 0 {
		return &betweenCache[V]{observers: pool.observersFor(id[0])}
	}
	return &betweenCache[V]{observers: pool.observers.clone()}
}

type betweenCacheSeen[V any] struct {
	entries  [2]betweenCacheKey[V]
	overflow *[2]betweenCacheKey[V]
	next     uint8
}

type betweenCacheKey[V any] struct {
	initialized bool
	hasFrom     bool
	hasUntil    bool
	from        V
	until       V
}

type betweenCacheEntry[V any] struct {
	initialized bool
	hasFrom     bool
	hasUntil    bool
	from        V
	until       V
	bits        *roaring.Bitmap
}
