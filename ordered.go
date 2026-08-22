package ruleix

import "github.com/RoaringBitmap/roaring/v2"

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
	nodeID    nodeID
	get       Getter[T, V]
	compare   Compare[V]
	dir       direction
	inclusive bool
	wildcard  *roaring.Bitmap
	index     orderedIndex[V]
}

func (*orderedRule[T, V]) inspectionStrategy() string { return "ordered" }

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
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	r.index.insert(value, id)
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
		cache = &valueBitmapCache[V]{}
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

	bits := cache.replace(value)
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
