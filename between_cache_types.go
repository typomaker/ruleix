package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type betweenCache[V any] struct {
	entries   [2]betweenCacheEntry[V]
	count     uint8
	overflow  []*betweenCacheEntry[V] // Sorted by bound presence and comparator order.
	observers *cacheObservers
}

func newBetweenCache[V any](pool *bitmapPool, id ...nodeID) *betweenCache[V] {
	if len(id) != 0 {
		return &betweenCache[V]{observers: pool.observersFor(id[0])}
	}
	return &betweenCache[V]{}
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
	if len(c.overflow) <= 4 {
		for _, entry := range c.overflow {
			if entry.matches(from, until, compare) {
				return entry
			}
		}
	} else if index, found := c.findOverflow(from, until, compare); found {
		return c.overflow[index]
	}
	return nil
}

func (c *betweenCache[V]) findOverflow(
	from, until optionalValue[V],
	compare Compare[V],
) (int, bool) {
	low, high := 0, len(c.overflow)
	for low < high {
		mid := low + (high-low)/2
		entry := c.overflow[mid]
		order := compareBetweenCacheBound(entry.from, entry.hasFrom, from, compare)
		if order == 0 {
			order = compareBetweenCacheBound(entry.until, entry.hasUntil, until, compare)
		}
		switch {
		case order < 0:
			low = mid + 1
		case order > 0:
			high = mid
		default:
			return mid, true
		}
	}
	return low, false
}

func compareBetweenCacheBound[V any](stored V, present bool, query optionalValue[V], compare Compare[V]) int {
	if present != query.ok {
		if present {
			return 1
		}
		return -1
	}
	if present {
		return compare(stored, query.value)
	}
	return 0
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
