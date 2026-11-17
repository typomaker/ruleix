package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type unsupportedBatchRule struct {
	bits        *roaring.Bitmap
	searchCalls int
}

func (*unsupportedBatchRule) rule() {}
func (r *unsupportedBatchRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] {
	return r
}
func (*unsupportedBatchRule) validate(int) error { return nil }
func (*unsupportedBatchRule) insert(int, uint32) {}
func (r *unsupportedBatchRule) cardinality(int, *bitmapPool) uint64 {
	return r.bits.GetCardinality()
}
func (r *unsupportedBatchRule) estimateCheapCardinality(int) uint64 {
	return r.bits.GetCardinality()
}
func (r *unsupportedBatchRule) search(_ int, dst *roaring.Bitmap, _ *bitmapPool) {
	r.searchCalls++
	dst.Or(r.bits)
}
func (*unsupportedBatchRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*unsupportedBatchRule) collectBuildStatistics([]nodeBuildStatistics) {}

func TestAllChildExecutionUnwrapsDirectIDOperationsTruthfully(t *testing.T) {
	leaf := &countingRule{ids: []uint32{1, 2}}
	wrapped := &inspectionDetailsRule[int]{
		child: &lossyRule[int]{child: leaf},
	}
	capability := describeAllChildExecution[int](wrapped)
	require.NotNil(t, capability.directID)
	require.True(t, capability.directID.matchesID(0, 2))

	unsupported := &inspectionDetailsRule[int]{child: &unsupportedBatchRule{bits: roaring.BitmapOf(1)}}
	require.Nil(t, describeAllChildExecution[int](unsupported).directID)
}

func TestAllChildExecutionCompilesOuterCachedBitmapProvider(t *testing.T) {
	leaf := &cachedEstimateRule{
		costlyEstimateRule: &costlyEstimateRule{child: &countingRule{ids: []uint32{1}}},
		bits:               roaring.BitmapOf(1),
	}
	metrics := &inspectorRuntime{}
	wrapped := &inspectedRuntimeRule[int]{child: leaf, metrics: metrics}

	capability := describeAllChildExecution[int](wrapped)
	require.Equal(t, cachedBitmapProvider[int](wrapped), capability.cached)
	bits, found := capability.cached.lookupCachedBitmap(0, newLocalBitmapPool(0))
	require.True(t, found)
	require.Equal(t, []uint32{1}, bits.ToArray())
}

func TestEqualityDirectIDWorkIsBoundedButLargeCandidateSetsStayOnBitmapPath(t *testing.T) {
	equality := &eqRule[int, int, int]{wildcard: roaring.BitmapOf(1)}
	capability := describeAllChildExecution[int](equality)
	require.Equal(t, uint64(32), capability.directIDWork)

	rule := &allRule[int]{children: []Rule[int]{
		&matchAllRule[int]{bits: roaring.BitmapOf(1)},
		equality,
	}}
	rule.prepareSearch()
	remaining := []rankedBitmap{{card: 50_000, childIdx: 1}}
	require.True(t, rule.shouldValidateRemaining(128, remaining))
	require.False(t, rule.shouldValidateRemaining(allCheapDirectIDScanLimit+1, remaining))
}

func TestNestedAllAdvertisesDirectIDOnlyWhenEveryChildSupportsIt(t *testing.T) {
	for _, state := range []struct {
		name     string
		prepared bool
	}{
		{name: "Unprepared"},
		{name: "Prepared", prepared: true},
	} {
		t.Run(state.name, func(t *testing.T) {
			supported := &allRule[int]{children: []Rule[int]{
				&countingRule{ids: []uint32{1}},
				&inspectionDetailsRule[int]{child: &countingRule{ids: []uint32{1}}},
			}}
			unsupported := &allRule[int]{children: []Rule[int]{
				&countingRule{ids: []uint32{1}},
				&unsupportedBatchRule{bits: roaring.BitmapOf(1)},
			}}
			if state.prepared {
				supported.prepareSearch()
				unsupported.prepareSearch()
			}

			require.NotNil(t, describeAllChildExecution[int](supported).directID)
			require.Nil(t, describeAllChildExecution[int](unsupported).directID)
		})
	}
}

func TestAllMaterializesUnsupportedRuleOncePerCandidateBatch(t *testing.T) {
	unsupported := &unsupportedBatchRule{bits: roaring.BitmapOf(2, 4, 6)}
	rule := &allRule[int]{children: []Rule[int]{
		&countingRule{ids: []uint32{1, 2, 3, 4, 5, 6}},
		unsupported,
	}}
	rule.prepareSearch()

	dst := roaring.New()
	rule.search(0, dst, newBitmapPool())

	require.Equal(t, []uint32{2, 4, 6}, dst.ToArray())
	require.Equal(t, 1, unsupported.searchCalls)
}

func TestLocalValidatesReadyMediumCandidatePosting(t *testing.T) {
	const candidateCount = 2 * allCandidateScanLimit
	ids := make([]uint32, candidateCount)
	for i := range ids {
		ids[i] = uint32(i)
	}
	source := roaring.BitmapOf(ids...)
	filter := &countingRule{ids: []uint32{2, 4, 20}}
	root := &allRule[int]{children: []Rule[int]{&countingRule{ids: ids}, filter}}
	root.prepareSearch()
	ranked := []rankedBitmap{
		{bits: source, card: candidateCount, childIdx: 0},
		{card: 100, childIdx: 1},
	}
	pool := newLocalBitmapPool(0)
	result := roaring.New()

	require.True(t, prepareRankedAllCandidates(root, 0, pool, ranked, result, nil))
	require.Equal(t, []uint32{2, 4}, result.ToArray())
	require.Equal(t, ids, source.ToArray())
	require.Equal(t, candidateCount, filter.matchIDCalls)
	require.Zero(t, filter.searchCalls)
}

func TestLocalReusesSelectivePostingPairUntilClose(t *testing.T) {
	source := roaring.New()
	source.AddRange(0, 2*allCandidateScanLimit)
	first := rankedBitmap{bits: source, card: source.GetCardinality()}
	posting := roaring.BitmapOf(2, 4, 6)
	posting.AddRange(32, 64)
	other := rankedBitmap{bits: posting, card: posting.GetCardinality(), childIdx: 1}
	pool := newLocalBitmapPool(0)

	require.Nil(t, pool.partial.candidates(first, []rankedBitmap{{}, first, other}))
	require.Equal(t, []uint32{2, 4, 6}, pool.partial.candidates(first, []rankedBitmap{other}))
	require.Equal(t, []uint32{2, 4, 6}, pool.partial.candidates(first, []rankedBitmap{other}))
	require.Equal(t, uint64(2*allCandidateScanLimit), source.GetCardinality())

	filter := &countingRule{ids: []uint32{2, 6, 7}}
	root := &allRule[int]{children: []Rule[int]{&countingRule{}, &countingRule{}, filter}}
	root.prepareSearch()
	ranked := []rankedBitmap{first, other, {card: 100, childIdx: 2}}
	result := roaring.New()
	require.True(t, prepareRankedAllCandidates(root, 0, pool, ranked, result, nil))
	require.Equal(t, []uint32{2, 6}, result.ToArray())
	require.Equal(t, 3, filter.matchIDCalls)

	wider := roaring.New()
	wider.AddRange(0, 64)
	wide := rankedBitmap{bits: wider, card: wider.GetCardinality()}
	require.Nil(t, pool.partial.candidates(first, []rankedBitmap{wide}))
	require.Nil(t, pool.partial.candidates(first, []rankedBitmap{wide}))

	empty := roaring.New()
	empty.AddRange(32, 64)
	disjoint := rankedBitmap{bits: empty, card: empty.GetCardinality()}
	require.Nil(t, pool.partial.candidates(first, []rankedBitmap{disjoint}))
	require.NotNil(t, pool.partial.candidates(first, []rankedBitmap{disjoint}))
	require.Empty(t, pool.partial.candidates(first, []rankedBitmap{disjoint}))

	pool.resetLocal()
	require.Nil(t, pool.partial.candidates(first, []rankedBitmap{disjoint}))
}

func TestLocalResolvesCompactIncludeOncePerCandidateBatch(t *testing.T) {
	type constraint struct {
		key     int
		present bool
	}
	var getterCalls int
	schema := Include(func(c constraint) (int, bool) {
		getterCalls++
		return c.key, c.present
	})
	rows := []constraint{{2, true}, {3, true}, {0, false}, {2, true}}
	index, err := New[constraint, int](schema).Build(Zip(rows, []int{0, 1, 2, 3}))
	require.NoError(t, err)
	root := &allRule[constraint]{children: []Rule[constraint]{index.root}}
	root.prepareSearch()
	pool := newLocalBitmapPool(0)
	source := roaring.BitmapOf(0, 1, 2, 3)
	remaining := []rankedBitmap{{card: 4}}

	for _, tc := range []struct {
		query constraint
		want  []uint32
	}{
		{constraint{2, true}, []uint32{0, 2, 3}},
		{constraint{99, true}, []uint32{2}},
		{constraint{}, []uint32{2}},
	} {
		getterCalls = 0
		result := roaring.New()
		root.collectValidatedCandidates(tc.query, source, result, nil, pool, remaining)
		require.Equal(t, tc.want, result.ToArray())
		require.Equal(t, 1, getterCalls)
	}
}

type localPlanCacheProbe struct {
	*countingRule
	calls int
}

func (p *localPlanCacheProbe) lookupCachedBitmap(int, *bitmapPool) (*roaring.Bitmap, bool) {
	p.calls++
	return nil, false
}

func TestLocalDefersCacheLookupsAfterReadySmallCandidatePosting(t *testing.T) {
	first := &planningCountingRule{countingRule: &countingRule{}, bits: roaring.BitmapOf(2, 3)}
	other := &localPlanCacheProbe{countingRule: &countingRule{}}
	root := &allRule[int]{children: []Rule[int]{first, other}}
	root.prepareSearch()
	pool := newLocalBitmapPool(0)
	plan := &localAllPlan{order: []int{0, 1}, firstCard: 2}
	ranked := make([]rankedBitmap, 2)

	require.True(t, root.populatePlanningLocalPlan(0, pool, plan, ranked))
	require.Same(t, first.bits, ranked[0].bits)
	require.Equal(t, 1, ranked[1].childIdx)
	require.Zero(t, other.calls)

	pool.observeRuntime = true
	require.True(t, root.populatePlanningLocalPlan(0, pool, plan, ranked))
	require.Equal(t, 1, other.calls)
}
