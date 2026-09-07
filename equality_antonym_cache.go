package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type strictEqualityAntonymCacheEntry struct {
	keys        []any
	bits        *roaring.Bitmap
	initialized bool
}

type strictEqualityAntonymSeen struct {
	keys        []any
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
		if entry.initialized && rule.queryKeysMatch(value, entry.keys) {
			c.next = uint8(1 - index)
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *strictEqualityAntonymCache[T]) admit(rule *strictEqualityAntonymRule[T], value T) bool {
	for index := range c.seen {
		entry := &c.seen[index]
		if entry.initialized && rule.queryKeysMatch(value, entry.keys) {
			*entry = strictEqualityAntonymSeen{}
			c.observers.admission()
			return true
		}
	}
	keys, _ := rule.captureQueryKeys(value, c.seen[c.seenNext].keys)
	c.seen[c.seenNext] = strictEqualityAntonymSeen{keys: keys, initialized: true}
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
	keys, _ := rule.captureQueryKeys(value, entry.keys)
	*entry = strictEqualityAntonymCacheEntry{
		keys: keys, bits: entry.bits, initialized: true,
	}
	entry.bits.SetCopyOnWrite(true)
	return entry.bits
}

func (r *strictEqualityAntonymRule[T]) queryKeysMatch(value T, keys []any) bool {
	if len(keys) != len(r.queryKeyProviders) {
		return false
	}
	for index, provider := range r.queryKeyProviders {
		if !provider.localQueryKeyMatches(value, keys[index]) {
			return false
		}
	}
	return true
}

func (r *strictEqualityAntonymRule[T]) captureQueryKeys(value T, reuse []any) ([]any, uint64) {
	if cap(reuse) < len(r.queryKeyProviders) {
		reuse = make([]any, len(r.queryKeyProviders))
	} else {
		reuse = reuse[:len(r.queryKeyProviders)]
	}
	bytes := uint64(cap(reuse)) * 16
	for index, provider := range r.queryKeyProviders {
		key, retained := provider.localQueryKey(value)
		reuse[index] = key
		bytes = saturatingAdd(bytes, retained)
	}
	return reuse, bytes
}

func (c *strictEqualityAntonymCache[T]) reset(pool *bitmapPool) {
	for index := range c.entries {
		entry := &c.entries[index]
		if entry.bits != nil {
			pool.put(entry.bits)
		}
	}
	observers := c.observers
	*c = strictEqualityAntonymCache[T]{observers: observers}
}
