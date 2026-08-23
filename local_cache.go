package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type localNodeCache struct {
	equality  any
	ordered   any
	compareBy any
	between   any
	exclusion any
}

type valueBitmapCache[V any] struct {
	entries  [2]valueBitmapCacheEntry[V]
	seen     *valueBitmapSeen[V]
	overflow *valueBitmapCacheOverflow[V]
	next     uint8
	pressure uint8
	misses   uint8
}

type valueBitmapCacheOverflow[V any] struct {
	entries [2]valueBitmapCacheEntry[V]
	used    [4]uint64
	clock   uint64
}

type valueBitmapSeen[V any] struct {
	entries  [2]valueBitmapCacheKey[V]
	overflow *[2]valueBitmapCacheKey[V]
	next     uint8
}

type valueBitmapCacheKey[V any] struct {
	initialized bool
	hasValue    bool
	value       V
}

type valueBitmapCacheEntry[V any] struct {
	initialized bool
	hasValue    bool
	value       V
	bits        *roaring.Bitmap
}

func (c *valueBitmapCache[V]) lookup(value optionalValue[V], equal func(V, V) bool) (*roaring.Bitmap, bool) {
	hasValue, capacity := value.ok, c.capacity()
	for i := 0; i < capacity; i++ {
		entry := c.entry(i)
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			if c.overflow == nil {
				c.next = uint8(1 - i)
			} else {
				c.overflow.clock++
				c.overflow.used[i] = c.overflow.clock
			}
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *valueBitmapCache[V]) replace(value optionalValue[V]) *roaring.Bitmap {
	if c.overflow == nil && c.entries[c.next].initialized {
		c.pressure++
		if c.pressure >= 2 {
			c.grow()
		}
	}
	index := int(c.next)
	if c.overflow != nil {
		index = c.leastRecentlyUsed()
		c.overflow.clock++
		c.overflow.used[index] = c.overflow.clock
	} else {
		c.next = (c.next + 1) % uint8(len(c.entries))
	}
	entry := c.entry(index)
	if entry.bits == nil {
		entry.bits = roaring.New()
	} else {
		entry.bits.Clear()
	}
	entry.bits.SetCopyOnWrite(true)
	entry.initialized = true
	entry.hasValue = value.ok
	var zero V
	entry.value = zero
	if value.ok {
		entry.value = value.value
	}
	return entry.bits
}

func (c *valueBitmapCache[V]) capacity() int {
	if c.overflow != nil {
		return 4
	}
	return 2
}

func (c *valueBitmapCache[V]) entry(index int) *valueBitmapCacheEntry[V] {
	if index < len(c.entries) {
		return &c.entries[index]
	}
	return &c.overflow.entries[index-len(c.entries)]
}

func (c *valueBitmapCache[V]) grow() {
	c.overflow = &valueBitmapCacheOverflow[V]{clock: 2}
	// next is the least recently used entry in the two-slot cache.
	c.overflow.used[c.next] = 1
	c.overflow.used[1-c.next] = 2
	c.next = 0
}

func (c *valueBitmapCache[V]) leastRecentlyUsed() int {
	oldest := 0
	for i := 1; i < c.capacity(); i++ {
		entry := c.entry(i)
		if !entry.initialized {
			return i
		}
		if c.overflow.used[i] < c.overflow.used[oldest] {
			oldest = i
		}
	}
	return oldest
}

// admit reports whether a missed value has been seen recently enough to merit
// retaining its materialized bitmap. One-off values stay out of the cache,
// avoiding retained memory and cache pollution on high-churn query streams.
func (c *valueBitmapCache[V]) admit(value optionalValue[V], equal func(V, V) bool) bool {
	if c.seen == nil {
		c.seen = &valueBitmapSeen[V]{}
	}
	seenCapacity := 2
	if c.seen.overflow != nil {
		seenCapacity = 4
	}
	for i := 0; i < seenCapacity; i++ {
		entry := c.seenEntry(i)
		if entry.initialized && entry.hasValue == value.ok && (!value.ok || equal(entry.value, value.value)) {
			entry.initialized = false
			var zero V
			entry.value = zero
			if c.seenEmpty() {
				c.seen = nil
			}
			return true
		}
	}
	c.misses++
	if c.misses >= 8 && c.seen.overflow == nil {
		c.seen.overflow = &[2]valueBitmapCacheKey[V]{}
		seenCapacity = 4
	}
	entry := c.seenEntry(int(c.seen.next))
	c.seen.next = (c.seen.next + 1) % uint8(seenCapacity)
	entry.initialized = true
	entry.hasValue = value.ok
	var zero V
	entry.value = zero
	if value.ok {
		entry.value = value.value
	}
	return false
}

func (c *valueBitmapCache[V]) seenEntry(index int) *valueBitmapCacheKey[V] {
	if index < len(c.seen.entries) {
		return &c.seen.entries[index]
	}
	return &c.seen.overflow[index-len(c.seen.entries)]
}

func (c *valueBitmapCache[V]) seenEmpty() bool {
	for i := 0; i < 2; i++ {
		if c.seen.entries[i].initialized {
			return false
		}
		if c.seen.overflow != nil && c.seen.overflow[i].initialized {
			return false
		}
	}
	return true
}

func comparableValueCacheLookup[V comparable](c *valueBitmapCache[V], value optionalValue[V]) (*roaring.Bitmap, bool) {
	return c.lookup(value, func(a, b V) bool { return a == b })
}

func comparableValueCacheAdmit[V comparable](c *valueBitmapCache[V], value optionalValue[V]) bool {
	return c.admit(value, func(a, b V) bool { return a == b })
}

func comparedValueCacheLookup[V any](
	c *valueBitmapCache[V],
	value optionalValue[V],
	compare Compare[V],
) (*roaring.Bitmap, bool) {
	return c.lookup(value, func(a, b V) bool { return compare(a, b) == 0 })
}

func comparedValueCacheAdmit[V any](
	c *valueBitmapCache[V],
	value optionalValue[V],
	compare Compare[V],
) bool {
	return c.admit(value, func(a, b V) bool { return compare(a, b) == 0 })
}
