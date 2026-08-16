package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Between matches an interval fully covered by a stored interval:
// stored.from <= query.from AND query.until <= stored.until. Either nil stored
// bound is a wildcard for that side of the interval; a nil query bound matches
// only a stored wildcard on the same side.
//
// For example, to find stored validity windows that cover a query window:
//
//	ruleix.Between(
//		func(c Constraint) *time.Time { return c.ValidFrom },
//		func(c Constraint) *time.Time { return c.ValidUntil },
//		time.Time.Compare,
//	)
func Between[T any, V any](from, until func(T) *V, compare Compare[V]) Rule[T] {
	return &betweenRule[T, V]{
		from:    newOrderedRule(from, compare, greaterThan, true),
		until:   newOrderedRule(until, compare, lessThan, true),
		compare: compare,
	}
}

// boundState stores the single extremum needed to test one side of Between
// for an internal ID. Repeated constraints with the same external ID retain
// min(from) and max(until), preserving the independent existential semantics
// of the former All(GreaterOrEqual, LessOrEqual) implementation.
type boundState[V any] struct {
	value    V
	set      bool
	wildcard bool
}

type betweenRule[T any, V any] struct {
	nodeID       nodeID
	from         *orderedRule[T, V]
	until        *orderedRule[T, V]
	compare      Compare[V]
	minimumFrom  []boundState[V]
	maximumUntil []boundState[V]
}

// betweenCandidateScanLimit caps scalar bound checks. Above this point,
// Roaring's container-wise bitmap intersection is faster than walking IDs.
const betweenCandidateScanLimit = 256

func (*betweenRule[T, V]) rule() {}
func (r *betweenRule[T, V]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	// Between owns one stateful node. Its two ordered indexes are internal
	// components whose future statistics belong to this node.
	id := ids.allocate()
	hint := hints.node(id)
	idCapacity := capacityHint(hint.betweenIDs)
	return &betweenRule[T, V]{
		nodeID: id,
		from:   r.from.newStateWithID(id, hint.between[0]), until: r.until.newStateWithID(id, hint.between[1]), compare: r.compare,
		minimumFrom: make([]boundState[V], 0, idCapacity), maximumUntil: make([]boundState[V], 0, idCapacity),
	}
}
func (*betweenRule[T, V]) validate(T) error { return nil }
func (r *betweenRule[T, V]) insert(v T, id uint32) {
	r.from.insert(v, id)
	r.until.insert(v, id)
	r.ensureID(id)
	r.updateMinimumFrom(id, r.from.get(v))
	r.updateMaximumUntil(id, r.until.get(v))
}
func (r *betweenRule[T, V]) cardinality(v T, pool *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, pool)
}
func (r *betweenRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	fromCardinality := r.from.cardinality(v, pool)
	untilCardinality := r.until.cardinality(v, pool)
	if fromCardinality == 0 || untilCardinality == 0 {
		return
	}
	if fromCardinality > betweenCandidateScanLimit && untilCardinality > betweenCandidateScanLimit {
		r.searchBitmaps(v, dst, pool)
		return
	}

	candidates := pool.get()
	defer pool.put(candidates)

	if fromCardinality <= untilCardinality {
		r.from.search(v, candidates, pool)
		query := r.until.get(v)
		it := candidates.Iterator()
		for it.HasNext() {
			id := it.Next()
			if r.matchesUntil(id, query) {
				dst.Add(id)
			}
		}
		return
	}

	r.until.search(v, candidates, pool)
	query := r.from.get(v)
	it := candidates.Iterator()
	for it.HasNext() {
		id := it.Next()
		if r.matchesFrom(id, query) {
			dst.Add(id)
		}
	}
}
func (*betweenRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *betweenRule[T, V]) collectBuildStatistics(stats []nodeBuildStatistics) {
	statistics := &stats[r.nodeID]
	statistics.betweenIDs = len(r.minimumFrom)
	statistics.between[0] = r.from.index.buildStatistics()
	statistics.between[1] = r.until.index.buildStatistics()
}

func (r *betweenRule[T, V]) searchBitmaps(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	left := pool.get()
	right := pool.get()
	r.from.search(v, left, pool)
	r.until.search(v, right, pool)
	left.And(right)
	dst.Or(left)
	pool.put(right)
	pool.put(left)
}

func (r *betweenRule[T, V]) ensureID(id uint32) {
	needed := int(id) + 1
	if len(r.minimumFrom) < needed {
		r.minimumFrom = append(r.minimumFrom, make([]boundState[V], needed-len(r.minimumFrom))...)
		r.maximumUntil = append(r.maximumUntil, make([]boundState[V], needed-len(r.maximumUntil))...)
	}
}

func (r *betweenRule[T, V]) updateMinimumFrom(id uint32, value *V) {
	state := &r.minimumFrom[id]
	if value == nil {
		state.wildcard = true
		return
	}
	if !state.set || r.compare(*value, state.value) < 0 {
		state.value = *value
		state.set = true
	}
}

func (r *betweenRule[T, V]) updateMaximumUntil(id uint32, value *V) {
	state := &r.maximumUntil[id]
	if value == nil {
		state.wildcard = true
		return
	}
	if !state.set || r.compare(*value, state.value) > 0 {
		state.value = *value
		state.set = true
	}
}

func (r *betweenRule[T, V]) matchesFrom(id uint32, query *V) bool {
	state := r.minimumFrom[id]
	if query == nil {
		return state.wildcard
	}
	return state.wildcard || state.set && r.compare(*query, state.value) >= 0
}

func (r *betweenRule[T, V]) matchesUntil(id uint32, query *V) bool {
	state := r.maximumUntil[id]
	if query == nil {
		return state.wildcard
	}
	return state.wildcard || state.set && r.compare(*query, state.value) <= 0
}

// BetweenConstraint applies Between to two fields of an optional nested
// interval. A nil interval is propagated to both bounds as a wildcard.
//
// For example:
//
//	ruleix.BetweenConstraint(
//		func(c Constraint) *Window { return c.Validity },
//		func(w Window) *time.Time { return w.From },
//		func(w Window) *time.Time { return w.Until },
//		time.Time.Compare,
//	)
func BetweenConstraint[T any, R any, V any](
	get func(T) *R,
	from, until func(R) *V,
	compare Compare[V],
) Rule[T] {
	return Between(Path(get, from), Path(get, until), compare)
}
