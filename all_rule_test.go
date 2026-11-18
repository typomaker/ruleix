package ruleix

import (
	"fmt"
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

type underestimatedRule struct{ ids []uint32 }

func (*underestimatedRule) rule()                                                   {}
func (r *underestimatedRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] { return r }
func (*underestimatedRule) validate(int) error                                      { return nil }
func (*underestimatedRule) insert(int, uint32)                                      {}
func (*underestimatedRule) cardinality(int, *bitmapPool) uint64                     { return 1 }
func (*underestimatedRule) estimateCheapCardinality(int) uint64                     { return 1 }
func (r *underestimatedRule) search(_ int, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.AddMany(r.ids)
}
func (*underestimatedRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*underestimatedRule) collectBuildStatistics([]nodeBuildStatistics) {}

func TestIndexSearchMaterializesUnderestimatedWideAllChildren(t *testing.T) {
	ids := make([]uint32, 20)
	entries, externalIDs := make([]int, len(ids)), make([]int, len(ids))
	for i := range ids {
		ids[i], entries[i], externalIDs[i] = uint32(i), i, i
	}
	for _, childCount := range []int{2, 9} {
		t.Run(fmt.Sprintf("children=%d", childCount), func(t *testing.T) {
			children := make([]Rule[int], childCount)
			for i := range children {
				children[i] = &underestimatedRule{ids: ids}
			}
			children[childCount-1] = &underestimatedRule{ids: ids[5:15]}
			index, err := New[int, int](All(children...)).Build(Zip(entries, externalIDs))
			require.NoError(t, err)

			var got []int
			index.SearchOne(0, &got)
			require.Equal(t, []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14}, got)
		})
	}
}

func TestAllOptimizeFlattensNestedGroups(t *testing.T) {
	first := &countingRule{ids: []uint32{1}}
	second := &countingRule{ids: []uint32{1}}
	third := &countingRule{ids: []uint32{1}}

	optimized := (&allRule[int]{children: []Rule[int]{
		first,
		&allRule[int]{children: []Rule[int]{second, third}},
	}}).optimize(1)

	all, ok := optimized.(*allRule[int])
	require.True(t, ok)
	require.Equal(t, []Rule[int]{first, second, third}, all.children)
}

func TestAllOrdersCandidateValidationByRejectionPerCost(t *testing.T) {
	expensive := &countingRule{ids: []uint32{0, 1}}
	cheap := &countingRule{ids: []uint32{0, 1, 2, 3}}
	rule := &allRule[int]{children: []Rule[int]{expensive, cheap}}
	rule.prepareSearch()
	candidates := roaring.New()
	candidates.AddRange(0, 8)
	remaining := []rankedBitmap{
		{card: 2, childIdx: 0},
		{bits: roaring.BitmapOf(0, 1, 2, 3), card: 4, childIdx: 1},
	}

	require.True(t, rule.validateCandidateBitmap(0, candidates, newBitmapPool(), remaining))

	require.Equal(t, []uint32{0, 1}, candidates.ToArray())
	// The cheaper bitmap rejects half the batch before the more selective
	// representation matcher runs.
	require.Equal(t, 4, expensive.matchIDCalls)
}

func TestAllMaterializesUnsupportedCandidateRuleOncePerBatch(t *testing.T) {
	child := &countingRule{ids: []uint32{1}}
	unsupported := &checkerOnlyRule{child: child}
	rule := &allRule[int]{children: []Rule[int]{unsupported}}
	rule.prepareSearch()
	candidates := roaring.New()
	candidates.AddRange(0, 4)
	remaining := []rankedBitmap{{card: ^uint64(0), childIdx: 0}}

	require.True(t, rule.validateCandidateBitmap(0, candidates, newBitmapPool(), remaining))

	require.Equal(t, []uint32{1}, candidates.ToArray())
	require.Equal(t, 1, child.searchCalls)
}

type zeroCheckingRule struct{ *countingRule }

func (r *zeroCheckingRule) isCardinalityZero(int) bool { return len(r.ids) == 0 }

// checkerOnlyRule deliberately hides the wrapped rule's cardinality estimate.
// It models a rule that can prove emptiness more cheaply than it can estimate
// or materialize its complete result.
type checkerOnlyRule struct{ child *countingRule }

func (*checkerOnlyRule) rule() {}
func (r *checkerOnlyRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] {
	return r
}
func (*checkerOnlyRule) validate(int) error { return nil }
func (*checkerOnlyRule) insert(int, uint32) {}
func (r *checkerOnlyRule) cardinality(value int, pool *bitmapPool) uint64 {
	return r.child.cardinality(value, pool)
}
func (r *checkerOnlyRule) isCardinalityZero(int) bool { return len(r.child.ids) == 0 }
func (r *checkerOnlyRule) isCheapCardinalityZero(int) bool {
	return len(r.child.ids) == 0
}
func (r *checkerOnlyRule) search(value int, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(value, dst, pool)
}
func (*checkerOnlyRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*checkerOnlyRule) collectBuildStatistics([]nodeBuildStatistics) {}

type estimatedZeroCheckingRule struct{ *countingRule }

func (r *estimatedZeroCheckingRule) isCardinalityZero(_ int) bool {
	r.cardinalityCalls++
	return len(r.ids) == 0
}

type conservativeEstimateRule struct{ *countingRule }

func (*conservativeEstimateRule) estimateCardinality(int) uint64 { return ^uint64(0) }
func (*conservativeEstimateRule) estimateCheapCardinality(int) uint64 {
	return ^uint64(0)
}

type costlyEstimateRule struct {
	child         *countingRule
	estimateCalls int
}

type cachedEstimateRule struct {
	*costlyEstimateRule
	bits *roaring.Bitmap
}

type planningCountingRule struct {
	*countingRule
	bits *roaring.Bitmap
}

func (r *planningCountingRule) lookupPlanningBitmap(int) (*roaring.Bitmap, bool) {
	return r.bits, true
}

func (r *cachedEstimateRule) estimateCachedCardinality(int, *bitmapPool) (uint64, bool) {
	return r.bits.GetCardinality(), true
}
func (r *cachedEstimateRule) lookupCachedBitmap(int, *bitmapPool) (*roaring.Bitmap, bool) {
	return r.bits, true
}

func (*costlyEstimateRule) rule() {}
func (r *costlyEstimateRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] {
	return r
}
func (*costlyEstimateRule) validate(int) error { return nil }
func (*costlyEstimateRule) insert(int, uint32) {}
func (r *costlyEstimateRule) cardinality(value int, pool *bitmapPool) uint64 {
	return r.child.cardinality(value, pool)
}
func (r *costlyEstimateRule) estimateCardinality(int) uint64 {
	r.estimateCalls++
	return uint64(len(r.child.ids))
}
func (r *costlyEstimateRule) matchesID(value int, id uint32) bool {
	return r.child.matchesID(value, id)
}
func (r *costlyEstimateRule) search(value int, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(value, dst, pool)
}
func (*costlyEstimateRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*costlyEstimateRule) collectBuildStatistics([]nodeBuildStatistics) {}

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
func (r *countingRule) estimateCheapCardinality(value int) uint64 {
	return r.estimateCardinality(value)
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

func TestAllReusesCachedBitmapWithoutEstimatingOrMaterializingChild(t *testing.T) {
	cachedChild := &countingRule{ids: []uint32{1, 2, 3, 4, 5, 6, 7, 8}}
	cached := &cachedEstimateRule{
		costlyEstimateRule: &costlyEstimateRule{child: cachedChild},
		bits:               roaring.BitmapOf(1, 2, 3, 4, 5, 6, 7, 8),
	}
	other := &countingRule{ids: []uint32{7, 8, 9, 10, 11, 12, 13, 14}}
	rule := &allRule[int]{children: []Rule[int]{cached, other}}
	pool := newBitmapPool()
	pool.local = make([]localNodeCache, 1)
	dst := roaring.New()

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{7, 8}, dst.ToArray())
	require.Zero(t, cached.estimateCalls)
	require.Zero(t, cachedChild.searchCalls)
}

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
	require.Zero(t, empty.searchCalls)
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
	first := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8}}
	disjoint := &countingRule{ids: []uint32{9, 10, 11, 12, 13, 14, 15, 16, 17}}
	unreached := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8}}
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

func TestAllReplansAfterIntersectionBeforeLaterDisjointRange(t *testing.T) {
	first := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8}}
	second := &countingRule{ids: []uint32{4, 5, 6, 7, 8, 9, 10, 11, 12}}
	disjoint := &countingRule{ids: []uint32{13, 14, 15, 16, 17, 18, 19, 20, 21}}
	unreached := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8}}
	rule := All[int](first, second, disjoint, unreached)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Equal(t, 1, first.searchCalls)
	require.Equal(t, 1, second.searchCalls)
	require.Zero(t, disjoint.searchCalls)
	require.Equal(t, 5, disjoint.matchIDCalls)
	require.Zero(t, unreached.searchCalls)
	require.Zero(t, unreached.matchIDCalls)
}
