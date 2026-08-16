package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// CompareBy evaluates the operator stored in each inserted rule against the
// concrete value supplied to Search. Search-side operator text is ignored. A
// nil stored value is a wildcard and its operator is not validated. Build
// returns an error when a non-wildcard rule contains an unsupported operator.
//
// For example, a stored pair {Operator: ">=", Orders: 5} matches a query with
// Orders equal to 7:
//
//	ruleix.CompareBy(
//		func(c Constraint) string { return c.Operator },
//		func(c Constraint) *int { return c.Orders },
//		cmp.Compare[int],
//	)
func CompareBy[T any, V any](
	operator func(T) string,
	value func(T) *V,
	compare Compare[V],
) Rule[T] {
	return &compareByRule[T, V]{
		operator: operator,
		value:    value,
		compare:  compare,
	}
}

type compareByRule[T any, V any] struct {
	nodeID   nodeID
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

type compareByCache[V any] struct {
	entries [2]compareByCacheEntry[V]
	next    uint8
}

type compareByCacheEntry[V any] struct {
	initialized bool
	hasValue    bool
	value       V
	bits        *roaring.Bitmap
}

func (*compareByRule[T, V]) rule() {}
func (r *compareByRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	id := ids.allocate()
	hint := hints.node(id).compareBy
	return &compareByRule[T, V]{
		nodeID: id, operator: r.operator, value: r.value, compare: r.compare,
		wildcard: roaring.New(), eq: newOrderedIndexWithHint(r.compare, hint[0]), lt: newOrderedIndexWithHint(r.compare, hint[1]),
		lte: newOrderedIndexWithHint(r.compare, hint[2]), gt: newOrderedIndexWithHint(r.compare, hint[3]), gte: newOrderedIndexWithHint(r.compare, hint[4]),
	}
}
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
func (r *compareByRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := r.value(v)
	if pool.compareBy == nil {
		r.addMatches(v, dst)
		return
	}

	cache, _ := pool.compareBy[int(r.nodeID)].(*compareByCache[V])
	if cache == nil {
		cache = &compareByCache[V]{}
		pool.compareBy[int(r.nodeID)] = cache
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
	r.addMatches(v, cached.bits)
	cached.initialized = true
	cached.hasValue = hasValue
	if hasValue {
		cached.value = *value
	}
	dst.Or(cached.bits)
}

func (r *compareByRule[T, V]) addMatches(v T, dst *roaring.Bitmap) {
	r.each(v, dst.Or)
}
func (*compareByRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *compareByRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	statistics := &stats[r.nodeID]
	statistics.compareBy = [5]orderedBuildStatistics{
		r.eq.buildStatistics(),
		r.lt.buildStatistics(),
		r.lte.buildStatistics(),
		r.gt.buildStatistics(),
		r.gte.buildStatistics(),
	}
}
