package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Exclude indexes an optional forbidden value. ok == false means no forbidden value. A
// concrete match excludes the associated result ID even when another stored
// constraint with that ID matches.
//
// For example, to reject rules whose forbidden channel equals the query
// channel:
//
//	ruleix.Exclude(func(c Constraint) (string, bool) { return c.ExcludedChannel, true })
func Exclude[T any, V comparable](get Getter[T, V]) Rule[T] {
	return &notRule[T, V]{get: get}
}

type notRule[T any, V comparable] struct {
	nodeID nodeID
	get    Getter[T, V]
	values equalityIndex[V]
}

func (*notRule[T, V]) rule() {}
func (r *notRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &notRule[T, V]{
		nodeID: id,
		get:    r.get,
		values: newEqualityIndex[V](capacityHint(hints.node(id).equalityValues)),
	}
}
func (*notRule[T, V]) validate(T) error { return nil }
func (r *notRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		return
	}
	r.values.add(value, id)
}
func (r *notRule[T, V]) cardinality(T, *bitmapPool) uint64 {
	return 0
}
func (*notRule[T, V]) search(T, *roaring.Bitmap, *bitmapPool) {}
func (r *notRule[T, V]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local == nil {
		r.addExclusions(value, dst)
		return
	}

	node := &pool.local[int(r.nodeID)]
	cache, _ := node.exclusion.(*valueBitmapCache[V])
	if cache == nil {
		cache = &valueBitmapCache[V]{}
		node.exclusion = cache
	}
	if bits, found := comparableValueCacheLookup(cache, value); found {
		dst.Or(bits)
		return
	}
	if !comparableValueCacheAdmit(cache, value) {
		r.addExclusions(value, dst)
		return
	}

	bits := cache.replace(value)
	r.addExclusions(value, bits)
	dst.Or(bits)
}

func (r *notRule[T, V]) addExclusions(value optionalValue[V], dst *roaring.Bitmap) {
	if value.ok {
		if set := r.values.get(value.value); set != nil {
			set.addTo(dst)
		}
	}
}
func (r *notRule[T, V]) isExcluded(v T, id uint32) bool {
	value, ok := r.get(v)
	if !ok {
		return false
	}
	set := r.values.get(value)
	return set != nil && set.contains(id)
}
func (r *notRule[T, V]) hasExclusions() bool { return len(r.values.sets) != 0 }
func (r *notRule[T, V]) prepareSearch() {
	for i := range r.values.sets {
		r.values.sets[i].prepareSearch()
	}
}
func (r *notRule[T, V]) internBitmaps(interner *bitmapInterner) {
	for i := range r.values.sets {
		r.values.sets[i].internBitmaps(interner)
	}
}
func (r *notRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].equalityValues = len(r.values.sets)
}
