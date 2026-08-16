package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// CompareBy evaluates the operator stored in each inserted rule against the
// concrete value supplied to Search. Search-side operator text is ignored. A
// nil stored value is a wildcard and its operator is not validated.
func CompareBy[T any, V any](
	operator func(T) string,
	value func(T) *V,
	compare Compare[V],
) Rule[T] {
	return &compareByRule[T, V]{
		operator: operator,
		value:    value,
		compare:  compare,
		wildcard: roaring.New(),
		eq:       newOrderedIndex(compare),
		lt:       newOrderedIndex(compare),
		lte:      newOrderedIndex(compare),
		gt:       newOrderedIndex(compare),
		gte:      newOrderedIndex(compare),
	}
}

type compareByRule[T any, V any] struct {
	operator func(T) string
	value    func(T) *V
	compare  Compare[V]
	wildcard *roaring.Bitmap
	eq       orderedIndex[V]
	lt       orderedIndex[V]
	lte      orderedIndex[V]
	gt       orderedIndex[V]
	gte      orderedIndex[V]
}

func (*compareByRule[T, V]) rule() {}
func (r *compareByRule[T, V]) validate(v T) error {
	if r.value(v) == nil {
		return nil
	}
	_, err := ParseOperator(r.operator(v))
	return err
}
func (r *compareByRule[T, V]) insert(v T, id uint32) {
	value := r.value(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	op, _ := ParseOperator(r.operator(v))
	switch op {
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
func (r *compareByRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	r.each(v, dst.Or)
}
func (*compareByRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
