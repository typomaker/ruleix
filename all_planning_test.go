package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

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
