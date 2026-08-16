package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// GreaterOrEqual matches query >= stored. A nil stored value is a wildcard.
//
// For example, a stored minimum total of 100 matches a query total of 150:
//
//	ruleix.GreaterOrEqual(
//		func(c Constraint) *int { return c.MinimumTotal },
//		cmp.Compare[int],
//	)
func GreaterOrEqual[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, true)
}

// LessOrEqual matches query <= stored. A nil stored value is a wildcard.
//
// For example, a stored maximum total of 200 matches a query total of 150:
//
//	ruleix.LessOrEqual(
//		func(c Constraint) *int { return c.MaximumTotal },
//		cmp.Compare[int],
//	)
func LessOrEqual[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, true)
}

// Greater matches query > stored. A nil stored value is a wildcard.
//
// For example, a stored order-count threshold of 5 matches a query count of 6:
//
//	ruleix.Greater(
//		func(c Constraint) *int { return c.OrderCountThreshold },
//		cmp.Compare[int],
//	)
func Greater[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, greaterThan, false)
}

// Less matches query < stored. A nil stored value is a wildcard.
//
// For example, a stored upper limit of 10 matches a query value of 9:
//
//	ruleix.Less(
//		func(c Constraint) *int { return c.UpperLimit },
//		cmp.Compare[int],
//	)
func Less[T any, V any](get func(T) *V, compare Compare[V]) Rule[T] {
	return newOrderedRule(get, compare, lessThan, false)
}

func newOrderedRule[T any, V any](get func(T) *V, compare Compare[V], dir direction, inclusive bool) *orderedRule[T, V] {
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
	nodeID    nodeID
	get       func(T) *V
	compare   Compare[V]
	dir       direction
	inclusive bool
	wildcard  *roaring.Bitmap
	index     orderedIndex[V]
}

func (*orderedRule[T, V]) rule() {}
func (r *orderedRule[T, V]) newState(ids *nodeIDAllocator) Rule[T] {
	return r.newStateWithID(ids.allocate())
}
func (r *orderedRule[T, V]) newStateWithID(id nodeID) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		nodeID: id, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: roaring.New(), index: newOrderedIndex(r.compare),
	}
}
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
