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
	var n uint64
	r.each(v, func(bits *roaring.Bitmap) { n += bits.GetCardinality() })
	return n
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
		cache = &valueBitmapCache[V]{}
		node.compareBy = cache
	}
	if bits, found := comparedValueCacheLookup(cache, value, r.compare); found {
		dst.Or(bits)
		return
	}
	bits := cache.replace(value)
	r.each(v, bits.Or)
	dst.Or(bits)
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
		}
	}
}
