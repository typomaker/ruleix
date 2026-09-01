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

type betweenLocalQueryKey[V any] struct {
	from, until       V
	hasFrom, hasUntil bool
}

func (r *betweenRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*betweenRule[T, V]) inspectionStrategy() string { return "between" }
func (r *betweenRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	ladder, err := r.newLossyAllPlanner().representationLadder()
	if err != nil || len(ladder) == 0 {
		return details
	}
	return ladder[0].details
}

func (r *betweenRule[T, V]) newLossyAllPlanner() lossyAllPlanner[T] {
	fromMemory, fromItems, fromDistinct, _ := orderedIndexLossyAccounting(&r.from.index, r.from.wildcard)
	untilMemory, untilItems, untilDistinct, _ := orderedIndexLossyAccounting(&r.until.index, r.until.wildcard)
	exact := Rule[T](&inspectionDetailsRule[T]{
		child: r,
		details: representationDetails(
			fromMemory+untilMemory,
			fromItems+untilItems,
			fromDistinct+untilDistinct,
			0,
			false,
		),
	})
	candidates := make([]Rule[T], 0, lossyMaxBucketBits+1)
	for bucketBits := uint(0); bucketBits <= lossyMaxBucketBits; bucketBits++ {
		candidate := &lossyBetweenRule[T, V]{
			nodeID: r.nodeID, fromGet: r.from.get, untilGet: r.until.get,
			fromWildcard: r.from.wildcard, untilWildcard: r.until.wildcard,
			from:  buildLossyComparedBuckets(&r.from.index, 1<<bucketBits),
			until: buildLossyComparedBuckets(&r.until.index, 1<<bucketBits),
		}
		usage := uint64(48) + bitmapBytes(candidate.fromWildcard) + bitmapBytes(candidate.untilWildcard) +
			candidate.from.memoryUsage() + candidate.until.memoryUsage()
		details := representationDetails(
			usage, fromItems+untilItems, fromDistinct+untilDistinct,
			uint64(len(candidate.from.buckets)+len(candidate.until.buckets)), true,
		)
		candidates = append(candidates, &inspectionDetailsRule[T]{child: candidate, details: details})
	}
	return fixedLossyAllPlanner[T]{ladder: buildLossyRepresentationLadder(exact, candidates)}
}

type lossyBetweenRule[T any, V any] struct {
	nodeID                      nodeID
	fromGet, untilGet           Getter[T, V]
	fromWildcard, untilWildcard *roaring.Bitmap
	from, until                 lossyComparedBuckets[V]
}

func (r *lossyBetweenRule[T, V]) runtimeNodeID() nodeID                               { return r.nodeID }
func (*lossyBetweenRule[T, V]) rule()                                                 {}
func (r *lossyBetweenRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyBetweenRule[T, V]) validate(T) error                                      { return nil }
func (*lossyBetweenRule[T, V]) streamingLossyAccumulator()                            {}
func (r *lossyBetweenRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(48) + bitmapBytes(r.fromWildcard) + bitmapBytes(r.untilWildcard) +
		r.from.memoryUsage() + r.until.memoryUsage()
	items := r.fromWildcard.GetCardinality() + r.untilWildcard.GetCardinality()
	for _, bucket := range r.from.buckets {
		items += bucket.GetCardinality()
	}
	for _, bucket := range r.until.buckets {
		items += bucket.GetCardinality()
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue = uint64(len(r.from.buckets) + len(r.until.buckets))
	details.GranularityAvailable = true
	return details
}
func (r *lossyBetweenRule[T, V]) fitStreamingLimit(limit uint64) {
	if r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes <= limit {
		return
	}
	r.from.collapse()
	r.until.collapse()
}
func (r *lossyBetweenRule[T, V]) insert(v T, id uint32) {
	if value, ok := r.fromGet(v); ok {
		r.from.insert(value, id)
	} else {
		r.fromWildcard.Add(id)
	}
	if value, ok := r.untilGet(v); ok {
		r.until.insert(value, id)
	} else {
		r.untilWildcard.Add(id)
	}
}
func (r *lossyBetweenRule[T, V]) localQueryKey(v T) (any, uint64) {
	from, hasFrom := r.fromGet(v)
	until, hasUntil := r.untilGet(v)
	key := betweenLocalQueryKey[V]{from: from, until: until, hasFrom: hasFrom, hasUntil: hasUntil}
	return key, uint64(16 + unsafe.Sizeof(key))
}
func (r *lossyBetweenRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(betweenLocalQueryKey[V])
	if !ok {
		return false
	}
	from, hasFrom := r.fromGet(v)
	until, hasUntil := r.untilGet(v)
	return want.hasFrom == hasFrom && want.hasUntil == hasUntil &&
		(!hasFrom || r.from.compare(want.from, from) == 0) &&
		(!hasUntil || r.until.compare(want.until, until) == 0)
}
func (r *lossyBetweenRule[T, V]) addSideMatches(
	v T,
	get Getter[T, V],
	wildcard *roaring.Bitmap,
	buckets *lossyComparedBuckets[V],
	dir direction,
	dst *roaring.Bitmap,
) {
	dst.Or(wildcard)
	value, ok := get(v)
	if !ok {
		return
	}
	first, last, matched := buckets.matchingRange(value, dir, true)
	if matched {
		buckets.addRange(first, last, dst)
	}
}
func (r *lossyBetweenRule[T, V]) sideCardinality(
	v T,
	get Getter[T, V],
	wildcard *roaring.Bitmap,
	buckets *lossyComparedBuckets[V],
	dir direction,
) uint64 {
	n := wildcard.GetCardinality()
	value, ok := get(v)
	if !ok {
		return n
	}
	first, last, matched := buckets.matchingRange(value, dir, true)
	if matched {
		n += buckets.rangeCardinality(first, last)
	}
	return n
}
func (r *lossyBetweenRule[T, V]) sideMatchesID(
	v T,
	id uint32,
	get Getter[T, V],
	wildcard *roaring.Bitmap,
	buckets *lossyComparedBuckets[V],
	dir direction,
) bool {
	if wildcard.Contains(id) {
		return true
	}
	value, ok := get(v)
	if !ok {
		return false
	}
	first, last, matched := buckets.matchingRange(value, dir, true)
	return matched && buckets.rangeContains(first, last, id)
}
func (r *lossyBetweenRule[T, V]) searchUncached(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	fromCardinality := r.sideCardinality(v, r.fromGet, r.fromWildcard, &r.from, greaterThan)
	untilCardinality := r.sideCardinality(v, r.untilGet, r.untilWildcard, &r.until, lessThan)
	baseGet, otherGet := r.fromGet, r.untilGet
	baseWildcard, otherWildcard := r.fromWildcard, r.untilWildcard
	baseBuckets, otherBuckets := &r.from, &r.until
	baseDirection, otherDirection := greaterThan, lessThan
	if untilCardinality < fromCardinality {
		baseGet, otherGet = otherGet, baseGet
		baseWildcard, otherWildcard = otherWildcard, baseWildcard
		baseBuckets, otherBuckets = otherBuckets, baseBuckets
		baseDirection, otherDirection = otherDirection, baseDirection
	}
	candidates := pool.get()
	r.addSideMatches(v, baseGet, baseWildcard, baseBuckets, baseDirection, candidates)
	if candidates.GetCardinality() <= allCheapDirectIDScanLimit {
		iterator := candidates.Iterator()
		for iterator.HasNext() {
			id := iterator.Next()
			if r.sideMatchesID(v, id, otherGet, otherWildcard, otherBuckets, otherDirection) {
				dst.Add(id)
			}
		}
		pool.put(candidates)
		return
	}
	other := pool.get()
	r.addSideMatches(v, otherGet, otherWildcard, otherBuckets, otherDirection, other)
	candidates.And(other)
	dst.Or(candidates)
	pool.put(other)
	pool.put(candidates)
}
func (r *lossyBetweenRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.peekCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *lossyBetweenRule[T, V]) peekCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].between.(*betweenCache[V])
	if cache == nil {
		return nil, false
	}
	from, until := getOptional(r.fromGet, v), getOptional(r.untilGet, v)
	for i := 0; i < cache.capacity(); i++ {
		entry := cache.entry(i)
		if entry.initialized && entry.hasFrom == from.ok && entry.hasUntil == until.ok &&
			(!from.ok || r.from.compare(entry.from, from.value) == 0) &&
			(!until.ok || r.from.compare(entry.until, until.value) == 0) {
			return entry.bits, true
		}
	}
	return nil, false
}
func (r *lossyBetweenRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	bits, found := r.peekCachedBitmap(v, pool)
	if !found {
		return nil, false
	}
	cache := pool.local[int(r.nodeID)].between.(*betweenCache[V])
	for i := 0; i < cache.capacity(); i++ {
		if cache.entry(i).bits != bits {
			continue
		}
		if cache.overflow == nil {
			cache.next = uint8(1 - i)
		} else {
			cache.overflow.clock++
			cache.overflow.used[i] = cache.overflow.clock
		}
		cache.observers.hit()
		break
	}
	return bits, true
}
func (r *lossyBetweenRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	from, until := getOptional(r.fromGet, v), getOptional(r.untilGet, v)
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
	if !cache.admit(from, until, r.from.compare) {
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
	cached.hasFrom, cached.hasUntil = from.ok, until.ok
	var zero V
	cached.from, cached.until = zero, zero
	if from.ok {
		cached.from = from.value
	}
	if until.ok {
		cached.until = until.value
	}
	dst.Or(cached.bits)
}
func (r *lossyBetweenRule[T, V]) estimateCardinality(v T) uint64 {
	return min(
		r.sideCardinality(v, r.fromGet, r.fromWildcard, &r.from, greaterThan),
		r.sideCardinality(v, r.untilGet, r.untilWildcard, &r.until, lessThan),
	)
}
func (r *lossyBetweenRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *lossyBetweenRule[T, V]) isCardinalityZero(v T) bool { return r.estimateCardinality(v) == 0 }
func (r *lossyBetweenRule[T, V]) matchesID(v T, id uint32) bool {
	return r.sideMatchesID(v, id, r.fromGet, r.fromWildcard, &r.from, greaterThan) &&
		r.sideMatchesID(v, id, r.untilGet, r.untilWildcard, &r.until, lessThan)
}
func (*lossyBetweenRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyBetweenRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyBetweenRule[T, V]) inspectionStrategy() string                   { return "lossy-between" }
func (*lossyBetweenRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyBetweenRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.fromWildcard)
	prepareBitmapForSearch(r.untilWildcard)
	r.from.prepareSearch()
	r.until.prepareSearch()
}

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
		(!hasFrom || r.compare(want.from, from) == 0) &&
		(!hasUntil || r.compare(want.until, until) == 0)
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
