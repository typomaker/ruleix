package ruleix

import (
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

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
		from:  newOrderedRule(from, compare, greaterThan, true),
		until: newOrderedRule(until, compare, lessThan, true),
	}
}

type betweenRule[T any, V any] struct {
	nodeID  nodeID
	from    *orderedRule[T, V]
	until   *orderedRule[T, V]
	compact *betweenCompact
}

type betweenLocalQueryKey[V any] struct {
	from, until       V
	hasFrom, hasUntil bool
}

type betweenSearchSide[T any, V any] struct {
	rule  *orderedRule[T, V]
	value optionalValue[V]
}

func (r *betweenRule[T, V]) runtimeNodeID() nodeID    { return r.nodeID }
func (*betweenRule[T, V]) inspectionStrategy() string { return "between" }

func (r *betweenRule[T, V]) finalizeBuild() {
	r.from.finalizeBuild()
	r.until.finalizeBuild()
}
func (r *betweenRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	return r.quantizedStreamingDetails(details)
}

func (*betweenRule[T, V]) rule() {}
func (r *betweenRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{
		representation: canonicalBetween,
		schema:         r,
		operations: [5]canonicalOperationID{
			{
				representation: canonicalBetween,
				owner:          r, queryBound: r.from, role: canonicalLowerBound,
				direction: r.from.dir, inclusive: r.from.inclusive,
				wildcard: canonicalMissingStoredMatches, comparator: canonicalOpaqueComparator,
			},
			{
				representation: canonicalBetween,
				owner:          r, queryBound: r.until, role: canonicalUpperBound,
				direction: r.until.dir, inclusive: r.until.inclusive,
				wildcard: canonicalMissingStoredMatches, comparator: canonicalOpaqueComparator,
			},
		},
		operationCount: 2,
	}
}
func (r *betweenRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	// Between owns one stateful node. Its two ordered indexes are internal
	// components whose future statistics belong to this node.
	id := ids.allocate()
	hint := hints.node(id)
	return &betweenRule[T, V]{
		nodeID: id,
		from:   r.from.newStateWithID(id, hint.between[0]),
		until:  r.until.newStateWithID(id, hint.between[1]),
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
func (r *betweenRule[T, V]) estimateCardinality(v T) uint64 {
	from := r.estimateSide(r.from, v)
	until := r.estimateSide(r.until, v)
	if from < until {
		return from
	}
	return until
}
func (r *betweenRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	from, until := getOptional(r.from.get, v), getOptional(r.until.get, v)
	_, entry := r.findCachedEntry(pool, from, until)
	if entry == nil {
		return 0, false
	}
	return entry.bits.GetCardinality(), true
}
func (r *betweenRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	from, until := getOptional(r.from.get, v), getOptional(r.until.get, v)
	return r.lookupCachedBitmapFor(pool, from, until)
}
func (r *betweenRule[T, V]) lookupCachedBitmapFor(
	pool *bitmapPool,
	from, until optionalValue[V],
) (*roaring.Bitmap, bool) {
	cache, entry := r.findCachedEntry(pool, from, until)
	if entry == nil {
		return nil, false
	}
	cache.observers.hit()
	return entry.bits, true
}
func (r *betweenRule[T, V]) findCachedEntry(
	pool *bitmapPool,
	from, until optionalValue[V],
) (*betweenCache[V], *betweenCacheEntry[V]) {
	if pool.local == nil {
		return nil, nil
	}
	cache, _ := pool.local[int(r.nodeID)].between.(*betweenCache[V])
	if cache == nil {
		return nil, nil
	}
	return cache, cache.find(from, until, r.from.compare)
}
func (r *betweenRule[T, V]) localQueryKey(v T) (any, uint64) {
	from, hasFrom := r.from.get(v)
	until, hasUntil := r.until.get(v)
	key := betweenLocalQueryKey[V]{from: from, until: until, hasFrom: hasFrom, hasUntil: hasUntil}
	return key, uint64(16 + unsafe.Sizeof(key))
}
func (r *betweenRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(betweenLocalQueryKey[V])
	if !ok {
		return false
	}
	from, hasFrom := r.from.get(v)
	until, hasUntil := r.until.get(v)
	return want.hasFrom == hasFrom && want.hasUntil == hasUntil &&
		(!hasFrom || r.from.compare(want.from, from) == 0) &&
		(!hasUntil || r.from.compare(want.until, until) == 0)
}
func (r *betweenRule[T, V]) isCardinalityZero(v T) bool {
	return r.from.isCardinalityZero(v) || r.until.isCardinalityZero(v)
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
		cache = newBetweenCache[V](pool, r.nodeID)
		node.between = cache
	}
	if bits, found := r.lookupCachedBitmapFor(pool, from, until); found {
		dst.Or(bits)
		return
	}
	cache.observers.miss()
	hasFrom, hasUntil := from.ok, until.ok
	pool.invalidateResultCache()
	var cached *betweenCacheEntry[V]
	if int(cache.count) < len(cache.entries) {
		cached = &cache.entries[cache.count]
		cache.count++
		cached.bits = pool.get()
	} else {
		cached = &betweenCacheEntry[V]{bits: pool.get()}
		cache.overflow = append(cache.overflow, cached)
	}
	cache.observers.admission()
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

func (c *betweenCache[V]) reset(pool *bitmapPool) {
	for i := range int(c.count) {
		entry := &c.entries[i]
		if entry.bits != nil {
			pool.put(entry.bits)
		}
	}
	for _, entry := range c.overflow {
		pool.put(entry.bits)
	}
	observers := c.observers
	*c = betweenCache[V]{observers: observers}
}

func (c *betweenCache[V]) entryCount() int { return int(c.count) + len(c.overflow) }

func (c *betweenCache[V]) entry(index int) *betweenCacheEntry[V] {
	if index < int(c.count) {
		return &c.entries[index]
	}
	return c.overflow[index-int(c.count)]
}
func (r *betweenRule[T, V]) matchesID(v T, id uint32) bool {
	if r.compact != nil {
		return compactMatches(r.from, r.compact.from.positions, v, id) &&
			compactMatches(r.until, r.compact.until.positions, v, id)
	}
	return r.from.matchesID(v, id) && r.until.matchesID(v, id)
}

func (r *betweenRule[T, V]) filterCandidates(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	first, second := r.rankedSearchSides(v)
	var inline [16]*roaring.Bitmap
	for _, side := range [...]betweenSearchSide[T, V]{first, second} {
		r.intersectSide(side, dst, inline[:0])
		if dst.IsEmpty() {
			return
		}
	}
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
	if r.compact == nil {
		r.from.prepareSearch()
		r.until.prepareSearch()
	}
}
func (r *betweenRule[T, V]) internBitmaps(interner *bitmapInterner) {
	r.from.internBitmaps(interner)
	r.until.internBitmaps(interner)
}

func (r *betweenRule[T, V]) searchBitmaps(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	base, other := r.rankedSearchSides(v)

	candidates := pool.get()
	r.addSide(base, candidates)
	if candidates.GetCardinality() <= allCandidateScanLimit {
		iterator := candidates.Iterator()
		for iterator.HasNext() {
			id := iterator.Next()
			if r.matchesSide(other.rule, v, id) {
				dst.Add(id)
			}
		}
		pool.put(candidates)
		return
	}
	var inline [16]*roaring.Bitmap
	r.intersectSide(other, candidates, inline[:0])
	dst.Or(candidates)
	pool.put(candidates)
}

func (r *betweenRule[T, V]) rankedSearchSides(v T) (
	betweenSearchSide[T, V],
	betweenSearchSide[T, V],
) {
	from := betweenSearchSide[T, V]{rule: r.from, value: getOptional(r.from.get, v)}
	until := betweenSearchSide[T, V]{rule: r.until, value: getOptional(r.until.get, v)}
	if r.estimateSide(r.until, v) < r.estimateSide(r.from, v) {
		return until, from
	}
	return from, until
}
