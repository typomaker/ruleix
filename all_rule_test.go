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

func TestAllReusesEstimateForEmptyCheck(t *testing.T) {
	first := &estimatedZeroCheckingRule{countingRule: &countingRule{ids: []uint32{1}}}
	second := &estimatedZeroCheckingRule{countingRule: &countingRule{ids: []uint32{1, 2}}}
	rule := All[int](first, second)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{1}, dst.ToArray())
	require.Equal(t, 1, first.cardinalityCalls)
	require.Equal(t, 1, second.cardinalityCalls)
}

func TestNestedAllExposesCheapestEstimate(t *testing.T) {
	broad := &countingRule{ids: []uint32{1, 2, 3, 4}}
	selective := &countingRule{ids: []uint32{2}}
	unknown := &unknownEstimateRule[int]{child: &countingRule{ids: []uint32{2, 3}}}
	rule := All[int](unknown, All[int](broad, selective))

	estimator := rule.(cardinalityEstimator[int])
	require.Equal(t, uint64(1), estimator.estimateCardinality(0))
}

func TestAllSkipsCostlyEstimateAfterSmallCheapBound(t *testing.T) {
	selective := &countingRule{ids: []uint32{2}}
	costly := &costlyEstimateRule{child: &countingRule{ids: []uint32{1, 2, 3, 4, 5}}}
	rule := All[int](costly, selective)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{2}, dst.ToArray())
	require.Zero(t, costly.estimateCalls)
	require.Zero(t, costly.child.searchCalls)
	require.Equal(t, 1, costly.child.matchIDCalls)
}

func TestNestedAllSkipsCostlyEstimateAfterSmallCheapBound(t *testing.T) {
	selective := &countingRule{ids: []uint32{2}}
	costly := &costlyEstimateRule{child: &countingRule{ids: []uint32{1, 2, 3, 4, 5}}}
	rule := All[int](&countingRule{ids: []uint32{1, 2, 3, 4, 5}}, All[int](costly, selective))
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{2}, dst.ToArray())
	require.Zero(t, costly.estimateCalls)
	require.Zero(t, costly.child.searchCalls)
	require.Equal(t, 1, costly.child.matchIDCalls)
}

func TestNestedAllPropagatesCheapEmptyResult(t *testing.T) {
	expensive := &countingRule{ids: []uint32{1}}
	empty := &checkerOnlyRule{child: &countingRule{}}
	rule := All[int](expensive, All[int](&countingRule{ids: []uint32{1}}, empty))
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.True(t, dst.IsEmpty())
	require.Zero(t, expensive.searchCalls)
}

func BenchmarkNestedAllCheapEmptyCheck(b *testing.B) {
	ids := make([]uint32, 1_024)
	for i := range ids {
		ids[i] = uint32(i)
	}
	rule := All[int](
		&countingRule{ids: ids},
		All[int](&countingRule{ids: ids}, &checkerOnlyRule{child: &countingRule{}}),
	)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	b.ReportAllocs()
	for range b.N {
		dst.Clear()
		rule.search(0, dst, pool)
	}
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

func TestAllCostModelValidatesBeyondFallbackForBroadSibling(t *testing.T) {
	selectiveIDs := make([]uint32, allCandidateScanLimit+8)
	for i := range selectiveIDs {
		selectiveIDs[i] = uint32(i)
	}
	broadIDs := make([]uint32, 10_000)
	for i := range broadIDs {
		broadIDs[i] = uint32(i * 10)
	}
	selectiveCounter := &countingRule{ids: selectiveIDs}
	broadCounter := &countingRule{ids: broadIDs}
	selective := &planningCountingRule{countingRule: selectiveCounter, bits: roaring.BitmapOf(selectiveIDs...)}
	broad := &planningCountingRule{countingRule: broadCounter, bits: roaring.BitmapOf(broadIDs...)}
	rule := &allRule[int]{children: []Rule[int]{selective, broad}}
	rule.prepareSearch()
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)
	require.True(t, rule.shouldValidateCandidates([]rankedBitmap{
		{bits: selective.bits, card: selective.bits.GetCardinality(), childIdx: 0},
		{bits: broad.bits, card: broad.bits.GetCardinality(), childIdx: 1},
	}))

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{0, 10}, dst.ToArray())
	require.Zero(t, selectiveCounter.searchCalls)
	require.Zero(t, broadCounter.searchCalls)
}

func TestAllSwitchesToCandidateScanAfterSmallMaterializedResult(t *testing.T) {
	selective := &countingRule{ids: []uint32{2}}
	second := &countingRule{ids: []uint32{1, 2, 3, 4, 5}}
	third := &countingRule{ids: []uint32{2, 3, 4, 5, 6}}
	rule := All[int](
		&conservativeEstimateRule{countingRule: selective},
		&conservativeEstimateRule{countingRule: second},
		&conservativeEstimateRule{countingRule: third},
	)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{2}, dst.ToArray())
	require.Equal(t, 1, selective.searchCalls)
	require.Zero(t, second.searchCalls)
	require.Zero(t, third.searchCalls)
	require.Equal(t, 1, second.matchIDCalls)
	require.Equal(t, 1, third.matchIDCalls)
}

func TestAllReusesEarlierMaterializedResultsAfterCandidateSwitch(t *testing.T) {
	first := &countingRule{ids: []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9}}
	selective := &countingRule{ids: []uint32{2}}
	third := &countingRule{ids: []uint32{2, 3, 4, 5, 6}}
	rule := All[int](
		&conservativeEstimateRule{countingRule: first},
		&conservativeEstimateRule{countingRule: selective},
		&conservativeEstimateRule{countingRule: third},
	)
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{2}, dst.ToArray())
	require.Equal(t, 1, first.searchCalls)
	require.Zero(t, first.matchIDCalls)
	require.Equal(t, 1, selective.searchCalls)
	require.Zero(t, third.searchCalls)
	require.Equal(t, 1, third.matchIDCalls)
}

func TestAllReplansAfterIntersectionNarrowsCandidates(t *testing.T) {
	first := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}}
	second := &countingRule{ids: []uint32{14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29}}
	broadIDs := make([]uint32, 10_000)
	for i := range broadIDs {
		broadIDs[i] = uint32(i)
	}
	broadCounter := &countingRule{ids: broadIDs}
	broad := &planningCountingRule{countingRule: broadCounter, bits: roaring.BitmapOf(broadIDs...)}
	rule := &allRule[int]{children: []Rule[int]{
		&conservativeEstimateRule{countingRule: first},
		&conservativeEstimateRule{countingRule: second},
		broad,
	}}
	rule.prepareSearch()
	pool := newBitmapPool()
	dst := pool.get()
	defer pool.put(dst)

	rule.search(0, dst, pool)

	require.Equal(t, []uint32{14, 15}, dst.ToArray())
	require.Equal(t, 1, first.searchCalls)
	require.Equal(t, 1, second.searchCalls)
	require.Zero(t, broadCounter.searchCalls)
	require.Zero(t, broadCounter.matchIDCalls)
}
