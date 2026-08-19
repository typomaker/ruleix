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
	nodeID      nodeID
	get         Getter[T, V]
	wildcard    *roaring.Bitmap
	constrained *roaring.Bitmap
	values      map[V]*equalitySet
}

func (*notRule[T, V]) rule() {}
func (r *notRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &notRule[T, V]{
		nodeID:      id,
		get:         r.get,
		wildcard:    roaring.New(),
		constrained: roaring.New(),
		values:      make(map[V]*equalitySet, capacityHint(hints.node(id).equalityValues)),
	}
}
func (*notRule[T, V]) validate(T) error { return nil }
func (r *notRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	r.constrained.Add(id)
	set := r.values[value]
	if set == nil {
		r.values[value] = newEqualitySet(id)
		return
	}
	set.add(id)
}
func (r *notRule[T, V]) cardinality(T, *bitmapPool) uint64 {
	return r.wildcard.GetCardinality() + r.constrained.GetCardinality()
}
func (r *notRule[T, V]) search(_ T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	dst.Or(r.constrained)
}
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

	bits := cache.replace(value)
	r.addExclusions(value, bits)
	dst.Or(bits)
}

func (r *notRule[T, V]) addExclusions(value optionalValue[V], dst *roaring.Bitmap) {
	if value.ok {
		if set := r.values[value.value]; set != nil {
			set.addTo(dst)
		}
	}
}
func (r *notRule[T, V]) isExcluded(v T, id uint32) bool {
	value, ok := r.get(v)
	if !ok {
		return false
	}
	set := r.values[value]
	return set != nil && set.contains(id)
}
func (r *notRule[T, V]) optimize(total uint64) Rule[T] {
	if len(r.values) == 0 && r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}
func (r *notRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	prepareBitmapForSearch(r.constrained)
	for _, set := range r.values {
		set.prepareSearch()
	}
}
func (r *notRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	interner.intern(&r.constrained)
	for _, set := range r.values {
		set.internBitmaps(interner)
	}
}
func (r *notRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].equalityValues = len(r.values)
}
