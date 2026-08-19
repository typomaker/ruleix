package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// All combines rules with logical AND: a stored constraint matches only when
// every child rule matches. All may be nested.
//
// For example, to match both country and customer tier:
//
//	ruleix.All(
//		ruleix.Include(func(c Constraint) *string { return c.Country }),
//		ruleix.Include(func(c Constraint) *string { return c.Tier }),
//	)
func All[T any](rules ...Rule[T]) Rule[T] { return &allRule[T]{children: rules} }

type allRule[T any] struct{ children []Rule[T] }

func (*allRule[T]) rule() {}
func (r *allRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	children := make([]Rule[T], len(r.children))
	for i, child := range r.children {
		children[i] = child.newState(ids, hints)
	}
	return &allRule[T]{children: children}
}
func (r *allRule[T]) validate(v T) error {
	for _, child := range r.children {
		if err := child.validate(v); err != nil {
			return err
		}
	}
	return nil
}
func (r *allRule[T]) insert(v T, id uint32) {
	for _, child := range r.children {
		child.insert(v, id)
	}
}
func (r *allRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	for _, child := range r.children {
		child.exclude(v, dst, pool)
	}
}
func (r *allRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	for _, child := range r.children {
		child.collectBuildStatistics(stats)
	}
}
func (r *allRule[T]) prepareSearch() {
	for _, child := range r.children {
		prepareRuleSearch(child)
	}
}
func (r *allRule[T]) optimize(total uint64) Rule[T] {
	if len(r.children) == 0 {
		return r
	}
	children := make([]Rule[T], 0, len(r.children))
	var universal *matchAllRule[T]
	for _, child := range r.children {
		optimized := optimizeRule(child, total)
		if matchAll, ok := optimized.(*matchAllRule[T]); ok {
			universal = matchAll
			continue
		}
		children = append(children, optimized)
	}
	if len(children) == 0 {
		return universal
	}
	if len(children) == 1 {
		return children[0]
	}
	return &allRule[T]{children: children}
}
func (r *allRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, pool)
}
func (r *allRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	// Most All groups are small. Keeping their ranking storage on the stack
	// avoids a service allocation without adding shared mutable state.
	var inline [8]rankedBitmap
	if len(r.children) <= len(inline) {
		r.searchRanked(v, dst, pool, inline[:len(r.children)])
		return
	}
	buffer := pool.getRanked(len(r.children))
	r.searchRanked(v, dst, pool, buffer.items)
	pool.putRanked(buffer)
}

func (r *allRule[T]) searchRanked(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) {
	for i, child := range r.children {
		bits := pool.get()
		child.search(v, bits, pool)
		card := bits.GetCardinality()
		if card == 0 {
			pool.put(bits)
			for j := 0; j < i; j++ {
				pool.put(rankedChildren[j].bits)
			}
			return
		}
		rankedChildren[i] = rankedBitmap{bits: bits, card: card}
	}
	// Filter groups are normally small; insertion sort avoids reflection and a
	// closure allocation while preserving schema order for equal cardinalities.
	for i := 1; i < len(rankedChildren); i++ {
		for j := i; j > 0 && rankedChildren[j].card < rankedChildren[j-1].card; j-- {
			rankedChildren[j], rankedChildren[j-1] = rankedChildren[j-1], rankedChildren[j]
		}
	}
	if len(rankedChildren) == 0 {
		return
	}
	dst.Or(rankedChildren[0].bits)
	for _, child := range rankedChildren[1:] {
		if dst.IsEmpty() {
			for _, rankedChild := range rankedChildren {
				pool.put(rankedChild.bits)
			}
			return
		}
		dst.And(child.bits)
	}
	for _, child := range rankedChildren {
		pool.put(child.bits)
	}
}
