package ruleix

import "github.com/RoaringBitmap/roaring/v2"

type localNodeCache struct {
	equality  any
	ordered   any
	between   any
	exclusion any
}

type valueBitmapCache[V any] struct {
	entries [2]valueBitmapCacheEntry[V]
	next    uint8
}

type valueBitmapCacheEntry[V any] struct {
	initialized bool
	hasValue    bool
	value       V
	bits        *roaring.Bitmap
}

func (c *valueBitmapCache[V]) lookup(value *V, equal func(V, V) bool) (*roaring.Bitmap, bool) {
	hasValue := value != nil
	for i := range c.entries {
		entry := &c.entries[i]
		if entry.initialized && entry.hasValue == hasValue && (!hasValue || equal(entry.value, *value)) {
			c.next = uint8(1 - i)
			return entry.bits, true
		}
	}
	return nil, false
}

func (c *valueBitmapCache[V]) replace(value *V) *roaring.Bitmap {
	entry := &c.entries[c.next]
	c.next = (c.next + 1) % uint8(len(c.entries))
	if entry.bits == nil {
		entry.bits = roaring.New()
	} else {
		entry.bits.Clear()
	}
	entry.initialized = true
	entry.hasValue = value != nil
	if value != nil {
		entry.value = *value
	}
	return entry.bits
}

func comparableValueCacheLookup[V comparable](c *valueBitmapCache[V], value *V) (*roaring.Bitmap, bool) {
	return c.lookup(value, func(a, b V) bool { return a == b })
}

func comparedValueCacheLookup[V any](c *valueBitmapCache[V], value *V, compare Compare[V]) (*roaring.Bitmap, bool) {
	return c.lookup(value, func(a, b V) bool { return compare(a, b) == 0 })
}
