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
	entries [2]valueBitmapCacheEntry[V]
	seen    *valueBitmapSeen[V]
	next    uint8
}

type valueBitmapSeen[V any] struct {
	entries [2]valueBitmapCacheKey[V]
	next    uint8
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
	hasValue := value.ok
	for i := range c.entries {
		entry := &c.entries[i]
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, value.value)) {
			c.next = uint8(1 - i)
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *valueBitmapCache[V]) replace(value optionalValue[V]) *roaring.Bitmap {
	entry := &c.entries[c.next]
	c.next = (c.next + 1) % uint8(len(c.entries))
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

// admit reports whether a missed value has been seen recently enough to merit
// retaining its materialized bitmap. One-off values stay out of the cache,
// avoiding retained memory and cache pollution on high-churn query streams.
func (c *valueBitmapCache[V]) admit(value optionalValue[V], equal func(V, V) bool) bool {
	if c.seen == nil {
		c.seen = &valueBitmapSeen[V]{}
	}
	for i := range c.seen.entries {
		entry := &c.seen.entries[i]
		if entry.initialized && entry.hasValue == value.ok && (!value.ok || equal(entry.value, value.value)) {
			entry.initialized = false
			var zero V
			entry.value = zero
			if !c.seen.entries[0].initialized && !c.seen.entries[1].initialized {
				c.seen = nil
			}
			return true
		}
	}
	entry := &c.seen.entries[c.seen.next]
	c.seen.next = (c.seen.next + 1) % uint8(len(c.seen.entries))
	entry.initialized = true
	entry.hasValue = value.ok
	var zero V
	entry.value = zero
	if value.ok {
		entry.value = value.value
	}
	return false
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
