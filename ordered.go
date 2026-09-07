package ruleix

import (
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
)

// GreaterOrEqual matches query >= stored. A missing stored value is a wildcard.
//
// For example, a stored minimum total of 100 matches a query total of 150:
//
//	ruleix.GreaterOrEqual(
//		func(c Constraint) (int, bool) { return c.MinimumTotal, true },
//		cmp.Compare[int],
//	)
func GreaterOrEqual[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, true)
}

// LessOrEqual matches query <= stored. A missing stored value is a wildcard.
//
// For example, a stored maximum total of 200 matches a query total of 150:
//
//	ruleix.LessOrEqual(
//		func(c Constraint) (int, bool) { return c.MaximumTotal, true },
//		cmp.Compare[int],
//	)
func LessOrEqual[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, true)
}

// Greater matches query > stored. A missing stored value is a wildcard.
//
// For example, a stored order-count threshold of 5 matches a query count of 6:
//
//	ruleix.Greater(
//		func(c Constraint) (int, bool) { return c.OrderCountThreshold, true },
//		cmp.Compare[int],
//	)
func Greater[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, false)
}

// Less matches query < stored. A missing stored value is a wildcard.
//
// For example, a stored upper limit of 10 matches a query value of 9:
//
//	ruleix.Less(
//		func(c Constraint) (int, bool) { return c.UpperLimit, true },
//		cmp.Compare[int],
//	)
func Less[T any, V any](get Getter[T, V], compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, false)
}

func newOrderedRule[T any, V any](
	get Getter[T, V],
	compare Compare[V],
	dir direction,
	inclusive bool,
) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		get:       get,
		dir:       dir,
		inclusive: inclusive,
		compare:   compare,
	}
}

type direction uint8

const (
	greaterThan direction = iota
	lessThan
)

type orderedRule[T any, V any] struct {
	nodeID        nodeID
	get           Getter[T, V]
	compare       Compare[V]
	dir           direction
	inclusive     bool
	wildcard      *roaring.Bitmap
	index         orderedIndex[V]
	build         *orderedRuleBuildState
	rebuildBlocks []orderedBlock[V]
}

// orderedRuleBuildState contains precision-controller state needed only while
// Build is still accepting values. It is removed before the rule is published.
type orderedRuleBuildState struct{ quantized bool }

func (r *orderedRule[T, V]) markBuildQuantized() {
	if r.build == nil {
		r.build = &orderedRuleBuildState{}
	}
	r.build.quantized = true
}

type orderedLocalQueryKey[V any] struct {
	value V
	ok    bool
}

func (r *orderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func orderedIndexLossyAccounting[V any](index *orderedIndex[V], wildcard *roaring.Bitmap) (
	memory, items, distinct uint64,
	all *roaring.Bitmap,
) {
	memory = uint64(24) + bitmapBytes(wildcard)
	items = wildcard.GetCardinality()
	all = wildcard.Clone()
	if index == nil {
		return memory, items, 0, all
	}
	for _, block := range index.blocks {
		memory += bitmapBytes(block.bits) + 8
		all.Or(block.bits)
		for _, item := range block.items {
			items += item.bits.GetCardinality()
			distinct++
			memory += comparableValueBytes(item.value) + 8 + bitmapBytes(item.bits)
		}
	}
	for _, block := range index.rangeBlocks {
		memory += 8 + bitmapBytes(block.bits)
	}
	memory += uint64(len(index.blockPrefix))*8 + uint64(len(index.routing.blocks))*8
	return memory, items, distinct, all
}

func quantizedOrderedAccounting[V any](index *orderedIndex[V], wildcard *roaring.Bitmap) (
	memory, items, distinct uint64,
) {
	memory = uint64(24) + bitmapBytes(wildcard)
	items = wildcard.GetCardinality()
	if index == nil {
		return memory, items, 0
	}
	accounting := orderedQuantizedBuildAccounting(index)
	memory += accounting.memory
	items += accounting.items
	distinct = accounting.distinct
	for _, block := range index.rangeBlocks {
		memory += 8 + bitmapBytes(block.bits)
	}
	return memory, items, distinct
}

func orderedQuantizedBuildAccounting[V any](index *orderedIndex[V]) orderedBuildAccounting {
	if index.accountingValid {
		return index.buildAccounting
	}
	accounting := orderedBuildAccounting{}
	for _, block := range index.blocks {
		accounting = addOrderedBuildAccounting(accounting, orderedBlockBuildAccounting(block))
	}
	index.buildAccounting, index.accountingValid = accounting, true
	return accounting
}

func orderedBlockBuildAccounting[V any](block orderedBlock[V]) orderedBuildAccounting {
	accounting := orderedBuildAccounting{distinct: uint64(len(block.items))}
	if len(block.items) > 1 {
		accounting.memory += bitmapBytes(block.bits) + 8
	}
	for _, item := range block.items {
		accounting.memory += comparableValueBytes(item.value) + 8 + bitmapBytes(item.bits)
		accounting.items += item.bits.GetCardinality()
	}
	return accounting
}

func addOrderedBuildAccounting(left, right orderedBuildAccounting) orderedBuildAccounting {
	return orderedBuildAccounting{
		memory: left.memory + right.memory, items: left.items + right.items, distinct: left.distinct + right.distinct,
	}
}

func subtractOrderedBuildAccounting(left, right orderedBuildAccounting) orderedBuildAccounting {
	return orderedBuildAccounting{
		memory: left.memory - right.memory, items: left.items - right.items, distinct: left.distinct - right.distinct,
	}
}

func (r *orderedRule[T, V]) inspectionStrategy() string {
	return "ordered"
}
func (r *orderedRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	memory, items, distinct := quantizedOrderedAccounting(&r.index, r.wildcard)
	details.MemoryUsageBytes, details.MemoryUsageAvailable = memory, true
	details.Items, details.ItemsAvailable = items, true
	details.DistinctValues, details.DistinctValuesAvailable = distinct, true
	return details
}

func (*orderedRule[T, V]) rule() {}
func (r *orderedRule[T, V]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{
		representation: canonicalOrdered,
		schema:         r,
		operations: [5]canonicalOperationID{{
			representation: canonicalOrdered,
			owner:          r, queryBound: r, role: canonicalWholeValue,
			direction: r.dir, inclusive: r.inclusive,
			wildcard: canonicalMissingStoredMatches, comparator: canonicalOpaqueComparator,
		}},
		operationCount: 1,
	}
}
func (r *orderedRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return r.newStateWithID(id, hints.node(id).ordered)
}
func (r *orderedRule[T, V]) newStateWithID(id nodeID, hint orderedBuildStatistics) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		nodeID: id, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: roaring.New(), index: newOrderedIndexWithHint(r.compare, hint), build: &orderedRuleBuildState{},
	}
}
func (*orderedRule[T, V]) validate(T) error { return nil }
func (r *orderedRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	r.index.insertOrdered(value, id, r.dir, r.build != nil && r.build.quantized)
}

func (r *orderedRule[T, V]) finalizeBuild() {
	r.build = nil
	r.rebuildBlocks = nil
}

func (r *orderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *orderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	return n + r.index.estimateCardinality(value, r.dir == lessThan, r.inclusive)
}
func (r *orderedRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	cache, _ := pool.local[int(r.nodeID)].ordered.(*valueBitmapCache[V])
	if cache == nil {
		return 0, false
	}
	bits, found := comparedValueCachePeek(cache, getOptional(r.get, v), r.compare)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *orderedRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].ordered.(*valueBitmapCache[V])
	if cache == nil {
		return nil, false
	}
	value := getOptional(r.get, v)
	if _, found := comparedValueCachePeek(cache, value, r.compare); !found {
		return nil, false
	}
	return comparedValueCacheLookup(cache, value, r.compare)
}
func (r *orderedRule[T, V]) localQueryKey(v T) (any, uint64) {
	value, ok := r.get(v)
	return orderedLocalQueryKey[V]{value: value, ok: ok}, uint64(16 + unsafe.Sizeof(orderedLocalQueryKey[V]{}))
}
func (r *orderedRule[T, V]) localQueryKeyMatches(v T, key any) bool {
	want, ok := key.(orderedLocalQueryKey[V])
	if !ok {
		return false
	}
	value, present := r.get(v)
	return want.ok == present && (!present || r.compare(want.value, value) == 0)
}
func (r *orderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *orderedRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local == nil {
		r.addMatches(value, dst)
		return
	}

	node := &pool.local[int(r.nodeID)]
	cache, _ := node.ordered.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool, r.nodeID)
		node.ordered = cache
	}
	if bits, found := comparedValueCacheLookup(cache, value, r.compare); found {
		dst.Or(bits)
		return
	}
	if !comparedValueCacheAdmit(cache, value, r.compare) {
		r.addMatches(value, dst)
		return
	}

	bits := cache.replace(value, pool)
	r.addMatches(value, bits)
	dst.Or(bits)
}
func (r *orderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	return ok && r.index.matches(value, r.dir == lessThan, r.inclusive, id)
}

func (r *orderedRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if value.ok {
		r.index.walk(value.value, r.dir == lessThan, r.inclusive, dst.Or)
	}
}
func (r *orderedRule[T, V]) appendMatchingBitmaps(value optionalValue[V], dst []*roaring.Bitmap) []*roaring.Bitmap {
	if !r.wildcard.IsEmpty() {
		dst = append(dst, r.wildcard)
	}
	if value.ok {
		r.index.walk(value.value, r.dir == lessThan, r.inclusive, func(bits *roaring.Bitmap) {
			dst = append(dst, bits)
		})
	}
	return dst
}
func (r *orderedRule[T, V]) filterCandidates(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	var inline [16]*roaring.Bitmap
	postings := r.appendMatchingBitmaps(getOptional(r.get, v), inline[:0])
	if len(postings) == 0 {
		dst.Clear()
		return
	}
	dst.AndAny(postings...)
}
func (*orderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *orderedRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}
func (r *orderedRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].ordered = r.index.buildStatistics()
}
func (r *orderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	r.index.prepareSearch()
}
func (r *orderedRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	r.index.internBitmaps(interner)
}
