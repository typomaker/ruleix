package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// GreaterOrEqual matches query >= stored. A nil stored value is a wildcard.
func GreaterOrEqual[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, true)
}

// LessOrEqual matches query <= stored. A nil stored value is a wildcard.
func LessOrEqual[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, true)
}

// Greater matches query > stored. A nil stored value is a wildcard.
func Greater[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, false)
}

// Less matches query < stored. A nil stored value is a wildcard.
func Less[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, false)
}

func newOrderedRule[T any, V any](get func(T) *V, compare Compare[V], dir direction, inclusive bool) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		get:       get,
		dir:       dir,
		inclusive: inclusive,
		wildcard:  roaring.New(),
		index:     newOrderedIndex(compare),
	}
}

type direction uint8

const (
	greaterThan direction = iota
	lessThan
)

type orderedRule[T any, V any] struct {
	get       func(T) *V
	dir       direction
	inclusive bool
	wildcard  *roaring.Bitmap
	index     orderedIndex[V]
}

func (*orderedRule[T, V]) rule()            {}
func (*orderedRule[T, V]) validate(T) error { return nil }
func (r *orderedRule[T, V]) insert(v T, id uint32) {
	value := r.get(v)
	if value == nil {
		r.wildcard.Add(id)
		return
	}
	r.index.insert(*value, id)
}
func (r *orderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	n := r.wildcard.GetCardinality()
	value := r.get(v)
	if value == nil {
		return n
	}
	r.index.walk(*value, r.dir == lessThan, r.inclusive, func(bits *roaring.Bitmap) {
		n += bits.GetCardinality()
	})
	return n
}
func (r *orderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	if value := r.get(v); value != nil {
		r.index.walk(*value, r.dir == lessThan, r.inclusive, dst.Or)
	}
}
func (*orderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
