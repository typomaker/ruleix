// Package ruleix implements a strongly typed in-memory rule index.
package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Compare defines an ordering for values used by ordered filters. It returns a
// negative number when a < b, zero when a == b, and a positive number when
// a > b. The standard library's cmp.Compare is suitable for ordered types.
type Compare[V any] func(a, b V) int

// Rule describes how constraints and query values of T are matched. Construct
// rules with Include, Exclude, the ordered filters, Between, CompareBy, and All.
// Its implementation is sealed so an index can rely on all rule invariants.
type Rule[T any] interface {
	rule()
	newState(*nodeIDAllocator, *buildStatistics) Rule[T]
	validate(T) error
	insert(T, uint32)
	cardinality(T, *bitmapPool) uint64
	search(T, *roaring.Bitmap, *bitmapPool)
	exclude(T, *roaring.Bitmap, *bitmapPool)
	collectBuildStatistics([]nodeBuildStatistics)
}

type ruleOptimizer[T any] interface {
	optimize(uint64) Rule[T]
}

type ruleSearchPreparer interface {
	prepareSearch()
}

func prepareRuleSearch[T any](rule Rule[T]) {
	if preparer, ok := rule.(ruleSearchPreparer); ok {
		preparer.prepareSearch()
	}
}

// prepareBitmapForSearch enables cheap sharing of immutable posting-list
// containers. Clone marks every existing container copy-on-write up front;
// doing it during Build avoids both allocation and mutation (and therefore a
// potential data race) on the first concurrent Search.
func prepareBitmapForSearch(bits *roaring.Bitmap) {
	bits.SetCopyOnWrite(true)
	_ = bits.Clone()
}

func optimizeRule[T any](rule Rule[T], total uint64) Rule[T] {
	if optimizer, ok := rule.(ruleOptimizer[T]); ok {
		return optimizer.optimize(total)
	}
	return rule
}

type matchAllRule[T any] struct{ bits *roaring.Bitmap }

func (*matchAllRule[T]) rule()                                                 {}
func (r *matchAllRule[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*matchAllRule[T]) validate(T) error                                      { return nil }
func (*matchAllRule[T]) insert(T, uint32)                                      {}
func (r *matchAllRule[T]) cardinality(T, *bitmapPool) uint64                   { return r.bits.GetCardinality() }
func (r *matchAllRule[T]) search(_ T, dst *roaring.Bitmap, _ *bitmapPool)      { dst.Or(r.bits) }
func (*matchAllRule[T]) exclude(T, *roaring.Bitmap, *bitmapPool)               {}
func (*matchAllRule[T]) collectBuildStatistics([]nodeBuildStatistics)          {}
func (r *matchAllRule[T]) prepareSearch()                                      { prepareBitmapForSearch(r.bits) }

func newMatchAllRule[T any](bits *roaring.Bitmap) Rule[T] {
	return &matchAllRule[T]{bits: bits}
}

type nodeID uint32

type nodeIDAllocator struct{ next nodeID }

func (a *nodeIDAllocator) allocate() nodeID {
	id := a.next
	a.next++
	return id
}

type orderedBuildStatistics struct {
	uniqueValues int
	blocks       int
}

type nodeBuildStatistics struct {
	equalityValues int
	ordered        orderedBuildStatistics
	betweenIDs     int
	between        [2]orderedBuildStatistics
	compareBy      [5]orderedBuildStatistics
}

type buildStatistics struct {
	entries   int
	uniqueIDs int
	nodes     []nodeBuildStatistics
}

// capacityHint adds five percent to the last observed size. It saturates at
// the largest int so corrupt or unusually large statistics cannot overflow
// an allocation size. Zero remains zero: there is no useful history yet.
func capacityHint(previous int) int {
	if previous <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	growth := previous / 20
	if growth < 1 {
		growth = 1
	}
	if previous > maxInt-growth {
		return maxInt
	}
	return previous + growth
}

func (s *buildStatistics) node(id nodeID) nodeBuildStatistics {
	if s == nil || int(id) >= len(s.nodes) {
		return nodeBuildStatistics{}
	}
	return s.nodes[id]
}

func measuredCardinality[T any](r Rule[T], value T, pool *bitmapPool) uint64 {
	bm := pool.get()
	r.search(value, bm, pool)
	n := bm.GetCardinality()
	pool.put(bm)
	return n
}
