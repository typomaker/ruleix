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

type orderedCache[V any] struct {
	entries [2]orderedCacheEntry[V]
	next    uint8
}

type orderedCacheEntry[V any] struct {
	initialized bool
	hasValue    bool
	value       V
	bits        *roaring.Bitmap
}

func (*orderedRule[T, V]) rule() {}
func (r *orderedRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	return r.newStateWithID(id, hints.node(id).ordered)
}
func (r *orderedRule[T, V]) newStateWithID(id nodeID, hint orderedBuildStatistics) *orderedRule[T, V] {
	return &orderedRule[T, V]{
		nodeID: id, get: r.get, compare: r.compare, dir: r.dir, inclusive: r.inclusive,
		wildcard: roaring.New(), index: newOrderedIndexWithHint(r.compare, hint),
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
func (r *orderedRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := r.get(v)
	if pool.ordered == nil {
		r.addMatches(value, dst)
		return
	}

	cache, _ := pool.ordered[int(r.nodeID)].(*orderedCache[V])
	if cache == nil {
		cache = &orderedCache[V]{}
		pool.ordered[int(r.nodeID)] = cache
	}
	hasValue := value != nil
	for i := range cache.entries {
		cached := &cache.entries[i]
		if cached.initialized && cached.hasValue == hasValue && (!hasValue || r.compare(cached.value, *value) == 0) {
			dst.Or(cached.bits)
			return
		}
	}

	cached := &cache.entries[cache.next]
	cache.next = (cache.next + 1) % uint8(len(cache.entries))
	if cached.bits == nil {
		cached.bits = roaring.New()
	} else {
		cached.bits.Clear()
	}
	r.addMatches(value, cached.bits)
	cached.initialized = true
	cached.hasValue = hasValue
	if hasValue {
		cached.value = *value
	}
	dst.Or(cached.bits)
}

func (r *orderedRule[T, V]) addMatches(value *V, dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if value != nil {
		r.index.walk(*value, r.dir == lessThan, r.inclusive, dst.Or)
	}
}
func (*orderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *orderedRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	stats[r.nodeID].ordered = r.index.buildStatistics()
}
