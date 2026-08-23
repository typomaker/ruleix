package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type countingRule struct {
	ids              []uint32
	searchCalls      int
	cardinalityCalls int
	matchIDCalls     int
}

type zeroCheckingRule struct{ *countingRule }

func (r *zeroCheckingRule) isCardinalityZero(int) bool { return len(r.ids) == 0 }

func (*countingRule) rule()                                                   {}
func (r *countingRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] { return r }
func (*countingRule) validate(int) error                                      { return nil }
func (*countingRule) insert(int, uint32)                                      {}
func (r *countingRule) cardinality(int, *bitmapPool) uint64 {
	r.cardinalityCalls++
	return uint64(len(r.ids))
}
func (r *countingRule) estimateCardinality(int) uint64 {
	r.cardinalityCalls++
	return uint64(len(r.ids))
}
func (r *countingRule) matchesID(_ int, id uint32) bool {
	r.matchIDCalls++
	for _, candidate := range r.ids {
		if candidate == id {
			return true
		}
	}
	return false
}
func (r *countingRule) search(_ int, dst *roaring.Bitmap, _ *bitmapPool) {
	r.searchCalls++
	dst.AddMany(r.ids)
}
func (*countingRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*countingRule) collectBuildStatistics([]nodeBuildStatistics) {}

func TestAllEstimatesAndMaterializesEachChildOnce(t *testing.T) {
	first := &countingRule{ids: []uint32{1, 2, 3}}
	second := &countingRule{ids: []uint32{2}}
	third := &countingRule{ids: []uint32{2, 3}}
	rule := All[int](first, second, third)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{2}, dst.ToArray())
	require.Zero(t, first.searchCalls)
	require.Equal(t, 1, second.searchCalls)
	require.Zero(t, third.searchCalls)
	for _, child := range []*countingRule{first, second, third} {
		require.Equal(t, 1, child.cardinalityCalls)
	}
	require.Equal(t, 1, first.matchIDCalls)
	require.Zero(t, second.matchIDCalls)
	require.Equal(t, 1, third.matchIDCalls)
}

func TestAllStopsMaterializingAfterEmptyChild(t *testing.T) {
	first := &countingRule{ids: []uint32{1}}
	empty := &countingRule{}
	unreached := &countingRule{ids: []uint32{1}}
	rule := All[int](first, empty, unreached)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Zero(t, first.searchCalls)
	require.Equal(t, 1, empty.searchCalls)
	require.Zero(t, unreached.searchCalls)
}

func TestAllStopsAfterEmptyIntersection(t *testing.T) {
	first := &countingRule{ids: []uint32{1}}
	disjoint := &countingRule{ids: []uint32{2}}
	unreached := &countingRule{ids: []uint32{1, 2, 3}}
	rule := All[int](first, disjoint, unreached)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Equal(t, 1, first.searchCalls)
	require.Zero(t, disjoint.searchCalls)
	require.Equal(t, 1, disjoint.matchIDCalls)
	require.Zero(t, unreached.searchCalls)
	require.Zero(t, unreached.matchIDCalls)
}

func TestAllBitmapPathStopsAfterDisjointLeadingPair(t *testing.T) {
	first := &countingRule{ids: []uint32{0, 1, 2, 3, 4}}
	disjoint := &countingRule{ids: []uint32{5, 6, 7, 8, 9}}
	unreached := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5}}
	rule := All[int](first, disjoint, unreached)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Equal(t, 1, first.searchCalls)
	require.Equal(t, 1, disjoint.searchCalls)
	require.Zero(t, unreached.searchCalls)
}

func TestAllChecksCheapEmptyChildBeforeMaterializing(t *testing.T) {
	expensive := &countingRule{ids: []uint32{1}}
	empty := &zeroCheckingRule{countingRule: &countingRule{}}
	rule := All[int](expensive, empty)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Zero(t, expensive.searchCalls)
	require.Zero(t, empty.searchCalls)
}

func TestNestedAllExposesCheapestEstimate(t *testing.T) {
	broad := &countingRule{ids: []uint32{1, 2, 3, 4}}
	selective := &countingRule{ids: []uint32{2}}
	unknown := &unknownEstimateRule[int]{child: &countingRule{ids: []uint32{2, 3}}}
	rule := All[int](unknown, All[int](broad, selective))

	estimator := rule.(cardinalityEstimator[int])
	require.Equal(t, uint64(1), estimator.estimateCardinality(0))
}

func TestNestedAllPropagatesCheapEmptyResult(t *testing.T) {
	expensive := &countingRule{ids: []uint32{1}}
	empty := &zeroCheckingRule{countingRule: &countingRule{}}
	rule := All[int](expensive, All[int](&countingRule{ids: []uint32{1}}, empty))
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Zero(t, expensive.searchCalls)
}

func TestAllCandidateScanLimit(t *testing.T) {
	for _, test := range []struct {
		name             string
		candidates       int
		wantSecondSearch int
		wantMatchCalls   int
	}{
		{name: "scan at limit", candidates: allCandidateScanLimit, wantMatchCalls: allCandidateScanLimit},
		{name: "bitmap above limit", candidates: allCandidateScanLimit + 1, wantSecondSearch: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ids := make([]uint32, test.candidates)
			for i := range ids {
				ids[i] = uint32(i)
			}
			first := &countingRule{ids: ids}
			second := &countingRule{ids: ids}
			rule := All[int](first, second)
			pool := newBitmapPool()
			dst := pool.get()
			defer pool.put(dst)

			rule.search(0, dst, pool)

			require.Equal(t, ids, dst.ToArray())
			require.Equal(t, 1, first.searchCalls)
			require.Equal(t, test.wantSecondSearch, second.searchCalls)
			require.Equal(t, test.wantMatchCalls, second.matchIDCalls)
		})
	}
}
