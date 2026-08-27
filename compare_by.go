package ruleix

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

// CompareBy evaluates the operator stored in each inserted rule against the
// concrete value supplied to Search. The query-side operator is ignored. A missing
// stored value is a wildcard and its operator is not validated. Build returns
// an error when a non-wildcard rule has no operator or an unsupported operator.
//
// For example, a stored value with OperatorGTE and value 5 matches a query
// whose value is 7:
//
//	ruleix.CompareBy(
//		func(c Constraint) (int, bool) { return c.Orders, true },
//		func(c Constraint) (ruleix.Operator, bool) { return c.Operator, true },
//		cmp.Compare[int],
//	)
func CompareBy[T any, V any](
	value Getter[T, V],
	operator Getter[T, Operator],
	compare Compare[V],
) Rule[T] {
	return &compareByRule[T, V]{value: value, operator: operator, compare: compare}
}

type compareByRule[T any, V any] struct {
	nodeID   nodeID
	value    Getter[T, V]
	operator Getter[T, Operator]
	compare  Compare[V]
	wildcard *roaring.Bitmap
	indexes  [5]*orderedIndex[V]
	hints    [5]orderedBuildStatistics
}

func (r *compareByRule[T, V]) executionFacts() ruleExecutionFacts {
	facts := ruleExecutionFacts{
		wildcard: wildcardMatchesQueries, wildcardCardinality: r.wildcard.GetCardinality(),
	}
	addExecutionPosting(&facts, facts.wildcardCardinality)
	for _, index := range r.indexes {
		if index == nil {
			continue
		}
		for _, block := range index.blocks {
			for _, item := range block.items {
				addExecutionPosting(&facts, item.bits.GetCardinality())
			}
		}
	}
	return facts
}

func (r *compareByRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (*compareByRule[T, V]) inspectionStrategy() string { return "compare-by" }

func (*compareByRule[T, V]) rule() {}
func (r *compareByRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &compareByRule[T, V]{
		nodeID:   id,
		value:    r.value,
		operator: r.operator,
		compare:  r.compare,
		wildcard: roaring.New(),
		hints:    hints.node(id).compareBy,
	}
}
func (r *compareByRule[T, V]) validate(v T) error {
	if _, ok := r.value(v); !ok {
		return nil
	}
	operator, ok := r.operator(v)
	if !ok {
		return fmt.Errorf("ruleix: CompareBy operator is nil")
	}
	if operator > OperatorGTE {
		return fmt.Errorf("ruleix: unsupported operator %d", operator)
	}
	return nil
}
func (r *compareByRule[T, V]) insert(v T, id uint32) {
	value, ok := r.value(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	operator, _ := r.operator(v)
	index := r.indexes[operator]
	if index == nil {
		created := newOrderedIndexWithHint(r.compare, r.hints[operator])
		index = &created
		r.indexes[operator] = index
	}
	index.insert(value, id)
}
func (r *compareByRule[T, V]) each(v T, visit func(*roaring.Bitmap)) {
	value, ok := r.value(v)
	visit(r.wildcard)
	if !ok {
		return
	}
	if index := r.indexes[OperatorEQ]; index != nil {
		if bits := index.exact(value); bits != nil {
			visit(bits)
		}
	}
	// query < stored / query <= stored
	if index := r.indexes[OperatorLT]; index != nil {
		index.walk(value, true, false, visit)
	}
	if index := r.indexes[OperatorLTE]; index != nil {
		index.walk(value, true, true, visit)
	}
	// query > stored / query >= stored
	if index := r.indexes[OperatorGT]; index != nil {
		index.walk(value, false, false, visit)
	}
	if index := r.indexes[OperatorGTE]; index != nil {
		index.walk(value, false, true, visit)
	}
}
func (r *compareByRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *compareByRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.value(v)
	if !ok {
		return n
	}
	if index := r.indexes[OperatorEQ]; index != nil {
		if bits := index.exact(value); bits != nil {
			n += bits.GetCardinality()
		}
	}
	if index := r.indexes[OperatorLT]; index != nil {
		n += index.estimateCardinality(value, true, false)
	}
	if index := r.indexes[OperatorLTE]; index != nil {
		n += index.estimateCardinality(value, true, true)
	}
	if index := r.indexes[OperatorGT]; index != nil {
		n += index.estimateCardinality(value, false, false)
	}
	if index := r.indexes[OperatorGTE]; index != nil {
		n += index.estimateCardinality(value, false, true)
	}
	return n
}
func (r *compareByRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	cache, _ := pool.local[int(r.nodeID)].compareBy.(*valueBitmapCache[V])
	if cache == nil {
		return 0, false
	}
	bits, found := comparedValueCachePeek(cache, getOptional(r.value, v), r.compare)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *compareByRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	if pool.local == nil {
		return nil, false
	}
	cache, _ := pool.local[int(r.nodeID)].compareBy.(*valueBitmapCache[V])
	if cache == nil {
		return nil, false
	}
	value := getOptional(r.value, v)
	if _, found := comparedValueCachePeek(cache, value, r.compare); !found {
		return nil, false
	}
	return comparedValueCacheLookup(cache, value, r.compare)
}
func (r *compareByRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *compareByRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.value, v)
	if pool.local == nil {
		r.each(v, dst.Or)
		return
	}
	node := &pool.local[int(r.nodeID)]
	cache, _ := node.compareBy.(*valueBitmapCache[V])
	if cache == nil {
		cache = newValueBitmapCache[V](pool, r.nodeID)
		node.compareBy = cache
	}
	if bits, found := comparedValueCacheLookup(cache, value, r.compare); found {
		dst.Or(bits)
		return
	}
	if !comparedValueCacheAdmit(cache, value, r.compare) {
		r.each(v, dst.Or)
		return
	}
	bits := cache.replace(value, pool)
	r.each(v, bits.Or)
	dst.Or(bits)
}
func (r *compareByRule[T, V]) matchesID(v T, id uint32) bool {
	found := false
	r.each(v, func(bits *roaring.Bitmap) {
		if !found && bits.Contains(id) {
			found = true
		}
	})
	return found
}
func (*compareByRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *compareByRule[T, V]) optimize(total uint64) Rule[T] {
	if r.wildcard.GetCardinality() == total {
		return newMatchAllRule[T](r.wildcard)
	}
	return r
}
func (r *compareByRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	for operator, index := range r.indexes {
		if index != nil {
			stats[r.nodeID].compareBy[operator] = index.buildStatistics()
		}
	}
}
func (r *compareByRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, index := range r.indexes {
		if index != nil {
			index.prepareSearch()
			index.prepareRangeSearch()
		}
	}
}
func (r *compareByRule[T, V]) internBitmaps(interner *bitmapInterner) {
	interner.intern(&r.wildcard)
	for _, index := range r.indexes {
		if index != nil {
			index.internBitmaps(interner)
		}
	}
}
