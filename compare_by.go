package ruleix

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

// CompareBy evaluates the operator stored in each inserted rule against the
// concrete value supplied to Search. The query-side operator is ignored. A nil
// stored value is a wildcard and its operator is not validated. Build returns
// an error when a non-wildcard rule has no operator or an unsupported operator.
//
// For example, a stored value with OperatorGTE and value 5 matches a query
// whose value is 7:
//
//	ruleix.CompareBy(
//		func(c Constraint) *int { return c.Orders },
//		func(c Constraint) *ruleix.Operator { return c.Operator },
//		cmp.Compare[int],
//	)
func CompareBy[T any, V any](
	value func(T) *V,
	operator func(T) *Operator,
	compare Compare[V],
) Rule[T] {
	return &compareByRule[T, V]{value: value, operator: operator, compare: compare}
}

type compareByRule[T any, V any] struct {
	nodeID   nodeID
	value    func(T) *V
	operator func(T) *Operator
	compare  Compare[V]
	wildcard *roaring.Bitmap
	eq       orderedIndex[V]
	lt       orderedIndex[V]
	lte      orderedIndex[V]
	gt       orderedIndex[V]
	gte      orderedIndex[V]
}

func (*compareByRule[T, V]) rule() {}
func (r *compareByRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	hint := hints.node(id).compareBy
	return &compareByRule[T, V]{
		nodeID:   id,
		value:    r.value,
		operator: r.operator,
		compare:  r.compare,
		wildcard: roaring.New(),
		eq:       newOrderedIndexWithHint(r.compare, hint[0]),
		lt:       newOrderedIndexWithHint(r.compare, hint[1]),
		lte:      newOrderedIndexWithHint(r.compare, hint[2]),
		gt:       newOrderedIndexWithHint(r.compare, hint[3]),
		gte:      newOrderedIndexWithHint(r.compare, hint[4]),
	}
}
func (r *compareByRule[T, V]) validate(v T) error {
	if r.value(v) == nil {
		return nil
	}
	operator := r.operator(v)
	if operator == nil {
		return fmt.Errorf("ruleix: CompareBy operator is nil")
	}
	if *operator > OperatorGTE {
		return fmt.Errorf("ruleix: unsupported operator %d", *operator)
	}
	return nil
}
func (r *compareByRule[T, V]) insert(v T, id uint32) {
	value := r.value(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	switch *r.operator(v) {
	case OperatorEQ:
		r.eq.insert(*value, id)
	case OperatorLT:
		r.lt.insert(*value, id)
	case OperatorLTE:
		r.lte.insert(*value, id)
	case OperatorGT:
		r.gt.insert(*value, id)
	case OperatorGTE:
		r.gte.insert(*value, id)
	}
}
func (r *compareByRule[T, V]) each(v T, visit func(*roaring.Bitmap)) {
	value := r.value(v)
	visit(r.wildcard)
	if value == nil {
		return
	}
	if bits := r.eq.exact(*value); bits != nil {
		visit(bits)
	}
	// query < stored / query <= stored
	r.lt.walk(*value, true, false, visit)
	r.lte.walk(*value, true, true, visit)
	// query > stored / query >= stored
	r.gt.walk(*value, false, false, visit)
	r.gte.walk(*value, false, true, visit)
}
func (r *compareByRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	var n uint64
	r.each(v, func(bits *roaring.Bitmap) { n += bits.GetCardinality() })
	return n
}
func (r *compareByRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := r.value(v)
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
	stats[r.nodeID].compareBy = [5]orderedBuildStatistics{
		r.eq.buildStatistics(), r.lt.buildStatistics(), r.lte.buildStatistics(),
		r.gt.buildStatistics(), r.gte.buildStatistics(),
	}
}
