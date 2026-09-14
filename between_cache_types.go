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

func (c *betweenCache[V]) find(
	from, until optionalValue[V],
	compare Compare[V],
) *betweenCacheEntry[V] {
	for i := range int(c.count) {
		entry := &c.entries[i]
		if entry.matches(from, until, compare) {
			return entry
		}
	}
	for _, entry := range c.overflow {
		if entry.matches(from, until, compare) {
			return entry
		}
	}
	return nil
}

func (e *betweenCacheEntry[V]) matches(
	from, until optionalValue[V],
	compare Compare[V],
) bool {
	return e.initialized &&
		e.hasFrom == from.ok && e.hasUntil == until.ok &&
		(!from.ok || compare(e.from, from.value) == 0) &&
		(!until.ok || compare(e.until, until.value) == 0)
}
