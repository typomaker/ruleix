package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type strictEqualityAntonymCacheEntry struct {
	left, right any
	bits        *roaring.Bitmap
	bytes       uint64
	initialized bool
}

type strictEqualityAntonymSeen struct {
	left, right any
	initialized bool
}

type strictEqualityAntonymCache[T any] struct {
	entries   [2]strictEqualityAntonymCacheEntry
	seen      [2]strictEqualityAntonymSeen
	next      uint8
	seenNext  uint8
	observers *cacheObservers
}

func (c *strictEqualityAntonymCache[T]) lookup(
	rule *strictEqualityAntonymRule[T], value T,
) (*roaring.Bitmap, bool) {
	bits, found := c.peek(rule, value)
	if found {
		c.observers.hit()
		return bits, true
	}
	c.observers.miss()
	return nil, false
}

func (c *strictEqualityAntonymCache[T]) peek(
	rule *strictEqualityAntonymRule[T], value T,
) (*roaring.Bitmap, bool) {
	for index := range c.entries {
		entry := &c.entries[index]
		if entry.initialized && rule.left.localQueryKeyMatches(value, entry.left) &&
			rule.right.localQueryKeyMatches(value, entry.right) {
			c.next = uint8(1 - index)
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *strictEqualityAntonymCache[T]) admit(rule *strictEqualityAntonymRule[T], value T) bool {
	for index := range c.seen {
		entry := &c.seen[index]
		if entry.initialized && rule.left.localQueryKeyMatches(value, entry.left) &&
			rule.right.localQueryKeyMatches(value, entry.right) {
			*entry = strictEqualityAntonymSeen{}
			c.observers.admission()
			return true
		}
	}
	left, _ := rule.left.localQueryKey(value)
	right, _ := rule.right.localQueryKey(value)
	c.seen[c.seenNext] = strictEqualityAntonymSeen{left: left, right: right, initialized: true}
	c.seenNext = 1 - c.seenNext
	return false
}

func (c *strictEqualityAntonymCache[T]) replace(
	rule *strictEqualityAntonymRule[T], value T, pool *bitmapPool,
) *roaring.Bitmap {
	pool.invalidateResultCache()
	entry := &c.entries[c.next]
	c.next = 1 - c.next
	if entry.initialized {
		c.observers.eviction()
	}
	if entry.bits == nil {
		entry.bits = pool.get()
	} else {
		entry.bits.Clear()
	}
	pool.childCacheBytes -= entry.bytes
	left, _ := rule.left.localQueryKey(value)
	right, _ := rule.right.localQueryKey(value)
	*entry = strictEqualityAntonymCacheEntry{
		left: left, right: right, bits: entry.bits, initialized: true,
	}
	entry.bits.SetCopyOnWrite(true)
	return entry.bits
}

func (c *strictEqualityAntonymCache[T]) commit(bits *roaring.Bitmap, pool *bitmapPool) {
	for index := range c.entries {
		entry := &c.entries[index]
		if entry.bits != bits {
			continue
		}
		bytes := bits.GetSizeInBytes()
		if bytes > uint64(maxLocalChildCacheBytes)-min(pool.childCacheBytes, uint64(maxLocalChildCacheBytes)) {
			*entry = strictEqualityAntonymCacheEntry{}
			pool.put(bits)
			return
		}
		entry.bytes = bytes
		pool.childCacheBytes += bytes
		return
	}
}

func (c *strictEqualityAntonymCache[T]) reset(pool *bitmapPool) {
	for index := range c.entries {
		entry := &c.entries[index]
		if entry.bits != nil {
			pool.childCacheBytes -= entry.bytes
			pool.put(entry.bits)
		}
	}
	observers := c.observers
	*c = strictEqualityAntonymCache[T]{observers: observers}
}
