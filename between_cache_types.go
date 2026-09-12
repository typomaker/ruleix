package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type betweenCache[V any] struct {
	entries   [2]betweenCacheEntry[V]
	count     uint8
	overflow  []*betweenCacheEntry[V]
	observers *cacheObservers
}

func newBetweenCache[V any](pool *bitmapPool, id ...nodeID) *betweenCache[V] {
	if len(id) != 0 {
		return &betweenCache[V]{observers: pool.observersFor(id[0])}
	}
	return &betweenCache[V]{observers: pool.observers.clone()}
}

type betweenCacheEntry[V any] struct {
	initialized bool
	hasFrom     bool
	hasUntil    bool
	from        V
	until       V
	bits        *roaring.Bitmap
}
