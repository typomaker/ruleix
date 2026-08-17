package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// CompareBy applies the operator supplied by a search value to all stored
// values. The operator of an inserted constraint is ignored. A nil search
// operator disables this filter, while a nil stored value is a wildcard.
// When the search operator is non-nil, a nil search value matches only stored
// wildcards.
//
// For example, a stored value of 5 matches a query with OperatorGTE and a
// value of 7:
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
	return &compareByRule[T, V]{
		value:    value,
		operator: operator,
		compare:  compare,
	}
}

type compareByRule[T any, V any] struct {
	nodeID   nodeID
	value    func(T) *V
	operator func(T) *Operator
	compare  Compare[V]
	all      *roaring.Bitmap
	wildcard *roaring.Bitmap
	index    orderedIndex[V]
}

func (*compareByRule[T, V]) rule() {}
func (r *compareByRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return &compareByRule[T, V]{
		nodeID:   id,
		value:    r.value,
		operator: r.operator,
		compare:  r.compare,
		all:      roaring.New(),
		wildcard: roaring.New(),
		index:    newOrderedIndexWithHint(r.compare, hints.node(id).compareBy),
	}
}
func (*compareByRule[T, V]) validate(T) error { return nil }
func (r *compareByRule[T, V]) insert(v T, id uint32) {
	r.all.Add(id)
	value := r.value(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	r.index.insert(*value, id)
}
func (r *compareByRule[T, V]) cardinality(v T, pool *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, pool)
}
func (r *compareByRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	operator := r.operator(v)
	if operator == nil {
		dst.Or(r.all)
		return
	}

	dst.Or(r.wildcard)
	value := r.value(v)
	if value == nil {
		return
	}

	switch *operator {
	case OperatorEQ:
		if bits := r.index.exact(*value); bits != nil {
			dst.Or(bits)
		}
	case OperatorLT:
		// query < stored
		r.index.walk(*value, true, false, dst.Or)
	case OperatorLTE:
		// query <= stored
		r.index.walk(*value, true, true, dst.Or)
	case OperatorGT:
		// query > stored
		r.index.walk(*value, false, false, dst.Or)
	case OperatorGTE:
		// query >= stored
		r.index.walk(*value, false, true, dst.Or)
	}
}
func (*compareByRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *compareByRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].compareBy = r.index.buildStatistics()
}
