package ruleix

import "github.com/RoaringBitmap/roaring/v2"

func equalityCache[V comparable](pool *bitmapPool, id nodeID) *valueBitmapCache[V] {
	node := &pool.local[int(id)]
	cache, _ := node.equality.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool, id)
		node.equality = cache
	}
	return cache
}

func lookupEqualityCachedBitmap[V comparable](
	pool *bitmapPool,
	id nodeID,
	value optionalValue[V],
) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(id)].equality.(*valueBitmapCache[V])
	if cache == nil {
		return nil, false
	}
	return comparableValueCacheLookup(cache, value)
}

func equalitySetBitmap(set *equalitySet) (*roaring.Bitmap, bool) {
	if set == nil || set.bits == nil {
		return nil, false
	}
	return set.bits, true
}
