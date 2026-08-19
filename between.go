package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Between matches an interval fully covered by a stored interval:
// stored.from <= query.from AND query.until <= stored.until. Either missing stored
// bound is a wildcard for that side of the interval; a missing query bound matches
// only a stored wildcard on the same side.
//
// For example, to find stored validity windows that cover a query window:
//
//	ruleix.Between(
//		func(c Constraint) (time.Time, bool) { return c.ValidFrom, true },
//		func(c Constraint) (time.Time, bool) { return c.ValidUntil, true },
//		time.Time.Compare,
//	)
func Between[T any, V any](from, until Getter[T, V], compare Compare[V]) Rule[T] {
	return &betweenRule[T, V]{
		from:    newOrderedRule(from, compare, greaterThan, true),
		until:   newOrderedRule(until, compare, lessThan, true),
		compare: compare,
	}
}

type betweenRule[T any, V any] struct {
	nodeID  nodeID
	from    *orderedRule[T, V]
	until   *orderedRule[T, V]
	compare Compare[V]
}

type betweenCache[V any] struct {
	entries [2]betweenCacheEntry[V]
	next    uint8
}

type betweenCacheEntry[V any] struct {
	initialized bool
	hasFrom     bool
	hasUntil    bool
	from        V
	until       V
	bits        *roaring.Bitmap
}

func (*betweenRule[T, V]) rule() {}
func (r *betweenRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	// Between owns one stateful node. Its two ordered indexes are internal
	// components whose future statistics belong to this node.
	id := ids.allocate()
	hint := hints.node(id)
	return &betweenRule[T, V]{
		nodeID:  id,
		from:    r.from.newStateWithID(id, hint.between[0]),
		until:   r.until.newStateWithID(id, hint.between[1]),
		compare: r.compare,
	}
}
func (*betweenRule[T, V]) validate(T) error { return nil }
func (r *betweenRule[T, V]) insert(v T, id uint32) {
	r.from.insert(v, id)
	r.until.insert(v, id)
}
func (r *betweenRule[T, V]) cardinality(v T, pool *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, pool)
}
func (r *betweenRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	from, until := getOptional(r.from.get, v), getOptional(r.until.get, v)
	if pool.local == nil {
		r.searchUncached(v, dst, pool)
		return
	}

	node := &pool.local[int(r.nodeID)]
	cache, _ := node.between.(*betweenCache[V])
	if cache == nil {
		cache = &betweenCache[V]{}
		node.between = cache
	}
	hasFrom, hasUntil := from.ok, until.ok
	for i := range cache.entries {
		cached := &cache.entries[i]
		if cached.initialized && cached.hasFrom == hasFrom && cached.hasUntil == hasUntil &&
			(!hasFrom || r.compare(cached.from, from.value) == 0) &&
			(!hasUntil || r.compare(cached.until, until.value) == 0) {
			cache.next = uint8(1 - i)
			dst.Or(cached.bits)
			return
		}
	}

	cached := &cache.entries[cache.next]
	cache.next = (cache.next + 1) % uint8(len(cache.entries))
	if cached.bits == nil {
		cached.bits = roaring.New()
	} else {
		cached.bits.Clear()
	}
	cached.bits.SetCopyOnWrite(true)
	r.searchUncached(v, cached.bits, pool)
	cached.initialized = true
	cached.hasFrom, cached.hasUntil = hasFrom, hasUntil
	var zero V
	cached.from, cached.until = zero, zero
	if hasFrom {
		cached.from = from.value
	}
	if hasUntil {
		cached.until = until.value
	}
	dst.Or(cached.bits)
}

func (r *betweenRule[T, V]) searchUncached(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.searchBitmaps(v, dst, pool)
}
func (*betweenRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *betweenRule[T, V]) optimize(total uint64) Rule[T] {
	if r.from.wildcard.GetCardinality() == total && r.until.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.from.wildcard)
	}
	return r
}
func (r *betweenRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	statistics := &stats[r.nodeID]
	statistics.between[0] = r.from.index.buildStatistics()
	statistics.between[1] = r.until.index.buildStatistics()
}
func (r *betweenRule[T, V]) prepareSearch() {
	r.from.prepareSearch()
	r.until.prepareSearch()
}
func (r *betweenRule[T, V]) internBitmaps(interner *bitmapInterner) {
	r.from.internBitmaps(interner)
	r.until.internBitmaps(interner)
}

func (r *betweenRule[T, V]) searchBitmaps(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	fromValue := getOptional(r.from.get, v)
	untilValue := getOptional(r.until.get, v)
	base, other := r.from, r.until
	baseValue, otherValue := fromValue, untilValue
	if r.until.cardinality(v, pool) < r.from.cardinality(v, pool) {
		base, other = r.until, r.from
		baseValue, otherValue = untilValue, fromValue
	}

	candidates := pool.get()
	base.addMatches(baseValue, candidates)
	var inline [16]*roaring.Bitmap
	postings := other.appendMatchingBitmaps(otherValue, inline[:0])
	if len(postings) == 0 {
		candidates.Clear()
	} else {
		candidates.AndAny(postings...)
	}
	dst.Or(candidates)
	pool.put(candidates)
}
