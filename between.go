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

func (r *betweenRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*betweenRule[T, V]) inspectionStrategy() string { return "between" }

type betweenCache[V any] struct {
	entries   [2]betweenCacheEntry[V]
	seen      *betweenCacheSeen[V]
	overflow  *betweenCacheOverflow[V]
	next      uint8
	pressure  uint8
	misses    uint8
	observers *cacheObservers
}

type betweenCacheOverflow[V any] struct {
	entries [2]betweenCacheEntry[V]
	used    [4]uint64
	clock   uint64
}

func newBetweenCache[V any](pool *bitmapPool, id ...nodeID) *betweenCache[V] {
	if len(id) != 0 {
		return &betweenCache[V]{observers: pool.observersFor(id[0])}
	}
	return &betweenCache[V]{observers: pool.observers.clone()}
}

type betweenCacheSeen[V any] struct {
	entries  [2]betweenCacheKey[V]
	overflow *[2]betweenCacheKey[V]
	next     uint8
}

type betweenCacheKey[V any] struct {
	initialized bool
	hasFrom     bool
	hasUntil    bool
	from        V
	until       V
}

type betweenCacheEntry[V any] struct {
	initialized bool
	hasFrom     bool
	hasUntil    bool
	from        V
	until       V
	bits        *roaring.Bitmap
	bytes       uint64
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
func (r *betweenRule[T, V]) estimateCardinality(v T) uint64 {
	from := r.from.estimateCardinality(v)
	until := r.until.estimateCardinality(v)
	if from < until {
		return from
	}
	return until
}
func (r *betweenRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	cache, _ := pool.local[int(r.nodeID)].between.(*betweenCache[V])
	if cache == nil {
		return 0, false
	}
	from, until := getOptional(r.from.get, v), getOptional(r.until.get, v)
	for i := 0; i < cache.capacity(); i++ {
		entry := cache.entry(i)
		if entry.initialized && entry.hasFrom == from.ok && entry.hasUntil == until.ok &&
			(!from.ok || r.compare(entry.from, from.value) == 0) &&
			(!until.ok || r.compare(entry.until, until.value) == 0) {
			return entry.bits.GetCardinality(), true
		}
	}
	return 0, false
}
func (r *betweenRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].between.(*betweenCache[V])
	if cache == nil {
		return nil, false
	}
	from, until := getOptional(r.from.get, v), getOptional(r.until.get, v)
	for i := 0; i < cache.capacity(); i++ {
		entry := cache.entry(i)
		if entry.initialized && entry.hasFrom == from.ok && entry.hasUntil == until.ok &&
			(!from.ok || r.compare(entry.from, from.value) == 0) &&
			(!until.ok || r.compare(entry.until, until.value) == 0) {
			if cache.overflow == nil {
				cache.next = uint8(1 - i)
			} else {
				cache.overflow.clock++
				cache.overflow.used[i] = cache.overflow.clock
			}
			cache.observers.hit()
			return entry.bits, true
		}
	}
	return nil, false
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
	if bits, found := r.lookupCachedBitmap(v, pool); found {
		dst.Or(bits)
		return
	}
	cache.observers.miss()
	hasFrom, hasUntil := from.ok, until.ok
	if !cache.admit(from, until, r.compare) {
		r.searchUncached(v, dst, pool)
		return
	}

	if cache.overflow == nil && cache.entries[cache.next].initialized {
		cache.pressure++
		if cache.pressure >= 2 {
			cache.grow()
		}
	}
	index := int(cache.next)
	if cache.overflow != nil {
		index = cache.leastRecentlyUsed()
		cache.overflow.clock++
		cache.overflow.used[index] = cache.overflow.clock
	} else {
		cache.next = (cache.next + 1) % uint8(len(cache.entries))
	}
	cached := cache.entry(index)
	pool.invalidateResultCache()
	cache.observers.admission()
	if cached.initialized {
		cache.observers.eviction()
	}
	if cached.bits == nil {
		cached.bits = pool.get()
	} else {
		pool.childCacheBytes -= cached.bytes
		cached.bytes = 0
		cached.bits.Clear()
	}
	cached.bits.SetCopyOnWrite(true)
	r.searchUncached(v, cached.bits, pool)
	bytes := cached.bits.GetSizeInBytes()
	if bytes > uint64(maxLocalChildCacheBytes)-min(pool.childCacheBytes, uint64(maxLocalChildCacheBytes)) {
		dst.Or(cached.bits)
		pool.put(cached.bits)
		*cached = betweenCacheEntry[V]{}
		return
	}
	cached.bytes = bytes
	pool.childCacheBytes += bytes
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
	for i := 0; i < c.capacity(); i++ {
		entry := c.entry(i)
		if entry.bits != nil {
			pool.childCacheBytes -= entry.bytes
			pool.put(entry.bits)
		}
	}
	observers := c.observers
	*c = betweenCache[V]{observers: observers}
}

func (c *betweenCache[V]) admit(from, until optionalValue[V], compare Compare[V]) bool {
	if c.seen == nil {
		c.seen = &betweenCacheSeen[V]{}
	}
	seenCapacity := c.seenCapacity()
	for i := 0; i < seenCapacity; i++ {
		entry := c.seenEntry(i)
		if entry.initialized && entry.hasFrom == from.ok && entry.hasUntil == until.ok &&
			(!from.ok || compare(entry.from, from.value) == 0) &&
			(!until.ok || compare(entry.until, until.value) == 0) {
			entry.initialized = false
			var zero V
			entry.from, entry.until = zero, zero
			if c.seenEmpty() {
				c.seen = nil
			}
			return true
		}
	}
	c.misses++
	if c.misses >= 8 && c.seen.overflow == nil {
		c.seen.overflow = &[2]betweenCacheKey[V]{}
		seenCapacity = 4
	}
	entry := c.seenEntry(int(c.seen.next))
	c.seen.next = (c.seen.next + 1) % uint8(seenCapacity)
	entry.initialized = true
	entry.hasFrom, entry.hasUntil = from.ok, until.ok
	var zero V
	entry.from, entry.until = zero, zero
	if from.ok {
		entry.from = from.value
	}
	if until.ok {
		entry.until = until.value
	}
	return false
}

func (c *betweenCache[V]) capacity() int {
	if c.overflow != nil {
		return 4
	}
	return 2
}

func (c *betweenCache[V]) entry(index int) *betweenCacheEntry[V] {
	if index < len(c.entries) {
		return &c.entries[index]
	}
	return &c.overflow.entries[index-len(c.entries)]
}

func (c *betweenCache[V]) grow() {
	c.overflow = &betweenCacheOverflow[V]{clock: 2}
	// next is the least recently used entry in the two-slot cache.
	c.overflow.used[c.next] = 1
	c.overflow.used[1-c.next] = 2
	c.next = 0
	c.observers.expansion()
}

func (c *betweenCache[V]) leastRecentlyUsed() int {
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

func (c *betweenCache[V]) seenCapacity() int {
	if c.seen.overflow != nil {
		return 4
	}
	return 2
}

func (c *betweenCache[V]) seenEntry(index int) *betweenCacheKey[V] {
	if index < len(c.seen.entries) {
		return &c.seen.entries[index]
	}
	return &c.seen.overflow[index-len(c.seen.entries)]
}

func (c *betweenCache[V]) seenEmpty() bool {
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
func (r *betweenRule[T, V]) matchesID(v T, id uint32) bool {
	return r.from.matchesID(v, id) && r.until.matchesID(v, id)
}

func (r *betweenRule[T, V]) filterCandidates(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	fromValue := getOptional(r.from.get, v)
	untilValue := getOptional(r.until.get, v)
	first, second := r.from, r.until
	firstValue, secondValue := fromValue, untilValue
	if r.until.estimateCardinality(v) < r.from.estimateCardinality(v) {
		first, second = r.until, r.from
		firstValue, secondValue = untilValue, fromValue
	}

	var inline [16]*roaring.Bitmap
	for _, side := range []struct {
		rule  *orderedRule[T, V]
		value optionalValue[V]
	}{{first, firstValue}, {second, secondValue}} {
		postings := side.rule.appendMatchingBitmaps(side.value, inline[:0])
		if len(postings) == 0 {
			dst.Clear()
			return
		}
		dst.AndAny(postings...)
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
	if r.until.estimateCardinality(v) < r.from.estimateCardinality(v) {
		base, other = r.until, r.from
		baseValue, otherValue = untilValue, fromValue
	}

	candidates := pool.get()
	base.addMatches(baseValue, candidates)
	if candidates.GetCardinality() <= allCandidateScanLimit {
		iterator := candidates.Iterator()
		for iterator.HasNext() {
			id := iterator.Next()
			if other.matchesID(v, id) {
				dst.Add(id)
			}
		}
		pool.put(candidates)
		return
	}
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
