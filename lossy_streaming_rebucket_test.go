package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type streamingOrderedFixture struct{ value int }

func streamingOrderedValue(v streamingOrderedFixture) (int, bool) { return v.value, true }

func TestLossyOrderedStreamingRegridsExpandedRange(t *testing.T) {
	minimum, _ := orderedScalarKey(any(10))
	middle, _ := orderedScalarKey(any(19))
	rule := &lossyOrderedRule[streamingOrderedFixture, int]{
		get: streamingOrderedValue, dir: greaterThan, inclusive: true,
		min: minimum, max: middle, width: 5,
		buckets:  []*roaring.Bitmap{roaring.BitmapOf(0), roaring.BitmapOf(1)},
		wildcard: roaring.New(),
	}
	rule.insert(streamingOrderedFixture{value: 30}, 2)
	if len(rule.buckets) != 2 {
		t.Fatalf("expanded grid has %d buckets, want 2", len(rule.buckets))
	}
	for id, stored := range []int{10, 19, 30} {
		for query := stored; query <= 35; query++ {
			if !rule.matchesID(streamingOrderedFixture{value: query}, uint32(id)) {
				t.Fatalf("stored %d disappeared for query %d", stored, query)
			}
		}
	}
}

func TestLossyComparedStreamingRegridsBothEdges(t *testing.T) {
	buckets := lossyComparedBuckets[int]{
		compare: cmp.Compare[int], minimum: 10, maximum: 30, capacity: 3,
		boundaries: []int{10, 20, 30},
		buckets:    []*roaring.Bitmap{roaring.BitmapOf(0), roaring.BitmapOf(1), roaring.BitmapOf(2)},
	}
	buckets.insert(40, 3)
	buckets.insert(0, 4)
	if len(buckets.buckets) != 3 {
		t.Fatalf("expanded comparator grid has %d buckets, want 3", len(buckets.buckets))
	}
	for id, stored := range []int{10, 20, 30, 40, 0} {
		first, last, ok := buckets.matchingRange(stored, greaterThan, true)
		if !ok || !buckets.rangeContains(first, last, uint32(id)) {
			t.Fatalf("stored %d disappeared after comparator rebucketing", stored)
		}
	}
}

func TestLossyComparedStreamingFitsGradually(t *testing.T) {
	buckets := lossyComparedBuckets[int]{
		compare: cmp.Compare[int], minimum: 10, maximum: 40, capacity: 4,
		boundaries: []int{10, 20, 30, 40},
		buckets: []*roaring.Bitmap{
			roaring.BitmapOf(0), roaring.BitmapOf(1), roaring.BitmapOf(2), roaring.BitmapOf(3),
		},
	}
	before := buckets.memoryUsage()
	buckets.coarsenOne()
	if len(buckets.buckets) != 3 {
		t.Fatalf("one pressure step has %d buckets, want 3", len(buckets.buckets))
	}
	if after := buckets.memoryUsage(); after >= before {
		t.Fatalf("one pressure step used %d bytes, want less than %d", after, before)
	}
}

func TestLossyEqualityStreamingRebucketsWithoutDroppingIDs(t *testing.T) {
	rule := &lossyEqualityRule[streamingOrderedFixture, int]{
		bucketCount: 4,
		buckets: map[uint64]lossyEqualityPosting{
			0: {bits: roaring.BitmapOf(0)},
			1: {bits: roaring.BitmapOf(1)},
			2: {bits: roaring.BitmapOf(2)},
			3: {bits: roaring.BitmapOf(3)},
		},
	}
	rule.rebucket(3)
	if rule.bucketCount != 3 {
		t.Fatalf("bucket count is %d, want 3", rule.bucketCount)
	}
	seen := roaring.New()
	for _, posting := range rule.buckets {
		seen.Or(posting.bits)
	}
	if seen.GetCardinality() != 4 {
		t.Fatalf("rebucketing retained %d IDs, want 4", seen.GetCardinality())
	}
	if len(rule.buckets) != 3 {
		t.Fatalf("rebucketing produced %d classes, want 3", len(rule.buckets))
	}
}

type streamingSelectionFixture struct {
	usage, next uint64
	fits        int
}

func (*streamingSelectionFixture) rule() {}
func (r *streamingSelectionFixture) newState(*nodeIDAllocator, *buildStatistics) Rule[streamingOrderedFixture] {
	return r
}
func (*streamingSelectionFixture) validate(streamingOrderedFixture) error                        { return nil }
func (*streamingSelectionFixture) insert(streamingOrderedFixture, uint32)                        {}
func (*streamingSelectionFixture) cardinality(streamingOrderedFixture, *bitmapPool) uint64       { return 0 }
func (*streamingSelectionFixture) search(streamingOrderedFixture, *roaring.Bitmap, *bitmapPool)  {}
func (*streamingSelectionFixture) exclude(streamingOrderedFixture, *roaring.Bitmap, *bitmapPool) {}
func (*streamingSelectionFixture) collectBuildStatistics([]nodeBuildStatistics)                  {}
func (r *streamingSelectionFixture) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	details.MemoryUsageBytes, details.MemoryUsageAvailable = r.usage, true
	details.GranularityValue, details.GranularityAvailable = 2, true
	return details
}
func (r *streamingSelectionFixture) nextStreamingUsage() (uint64, bool) {
	return r.next, r.next < r.usage
}
func (r *streamingSelectionFixture) fitStreamingLimit(uint64) {
	r.usage = r.next
	r.fits++
}
func (r *streamingSelectionFixture) fitStreamingNext() { r.fitStreamingLimit(0) }
func (r *streamingSelectionFixture) prepareStreamingNext() (uint64, func(), bool) {
	return r.next, r.fitStreamingNext, r.next != r.usage
}

func TestStreamingAggregateSelectsLargestReleaseAcrossExactAndLossyCandidates(t *testing.T) {
	smallRelease := &streamingSelectionFixture{usage: 100, next: 90}
	largeRelease := &streamingSelectionFixture{usage: 80, next: 40}
	rule := All[streamingOrderedFixture](smallRelease, largeRelease)

	fitStreamingAggregate(rule, 150)

	require.Zero(t, smallRelease.fits)
	require.Equal(t, 1, largeRelease.fits)
	var candidates []streamingFitCandidate
	require.Equal(t, uint64(140), collectStreamingFitCandidates(rule, &candidates))
}

func TestStreamingAdaptiveLeafDelegatesCachesAndUnwraps(t *testing.T) {
	child := &streamingSelectionFixture{usage: 100, next: 60}
	adaptive := &streamingAdaptiveLeaf[streamingOrderedFixture]{child: child}
	value := streamingOrderedFixture{value: 1}
	require.NotNil(t, adaptive.newState(&nodeIDAllocator{}, &buildStatistics{}))
	require.NoError(t, adaptive.validate(value))
	adaptive.insert(value, 1)
	require.Zero(t, adaptive.cardinality(value, nil))
	adaptive.search(value, roaring.New(), nil)
	adaptive.exclude(value, roaring.New(), nil)
	adaptive.collectBuildStatistics(nil)
	require.Equal(t, RuleModeExact, adaptive.inspectionMode())
	require.Equal(t, "custom", adaptive.inspectionStrategy())
	require.Equal(t, uint64(100), adaptive.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes)
	require.True(t, adaptive.canFitStreaming())
	usage, ok := adaptive.nextStreamingUsage()
	require.True(t, ok)
	require.Equal(t, uint64(60), usage)
	adaptive.fitStreamingNext()
	require.Equal(t, 1, child.fits)

	wrapped := &inspectRule[streamingOrderedFixture]{child: &inspectionDetailsRule[streamingOrderedFixture]{
		child: adaptive, details: inspectionDetails{},
	}}
	unwrapped := unwrapStreamingAdaptiveLeaves[streamingOrderedFixture](All(wrapped))
	require.NotNil(t, unwrapped)
	require.False(t, streamingRuleCanFit(&lossyUniversalRule[streamingOrderedFixture]{bits: roaring.New()}))

	stuck := &streamingSelectionFixture{usage: 10, next: 10}
	fitStreamingAggregate[streamingOrderedFixture](stuck, 5)
	require.Equal(t, 1, stuck.fits)
}

func TestStreamingPlanningHelperWrapperPaths(t *testing.T) {
	child := &streamingSelectionFixture{usage: 20, next: 10}
	adaptive := &streamingAdaptiveLeaf[streamingOrderedFixture]{child: child}
	wrapped := &inspectionDetailsRule[streamingOrderedFixture]{child: adaptive, details: inspectionDetails{
		MemoryUsageBytes: 20, MemoryUsageAvailable: true,
	}}
	preparer, ok := streamingRuleNextPreparer[streamingOrderedFixture](wrapped)
	require.True(t, ok)
	_, _, ok = preparer.prepareStreamingNext()
	require.True(t, ok)
	require.Equal(t, uint64(20), refreshedStreamingRuleDetails[streamingOrderedFixture](adaptive, inspectionDetails{}).MemoryUsageBytes)
	require.Equal(t, uint64(20), refreshedStreamingRuleDetails[streamingOrderedFixture](wrapped, inspectionDetails{}).MemoryUsageBytes)
	require.True(t, streamingRuleCanFit[streamingOrderedFixture](wrapped))
	_, ok = nextStreamingRuleUsage[streamingOrderedFixture](wrapped)
	require.True(t, ok)
	fitStreamingRuleNext[streamingOrderedFixture](wrapped)
	require.Equal(t, 1, child.fits)

	uncached := &streamingAdaptiveLeaf[streamingOrderedFixture]{
		child: &streamingSelectionFixture{usage: 30, next: 15},
	}
	fitStreamingRuleNext[streamingOrderedFixture](uncached)
	require.Equal(t, 1, uncached.child.(*streamingSelectionFixture).fits)

	policy := &inspectionDetailsRule[streamingOrderedFixture]{
		child:   &streamingSelectionFixture{usage: 20, next: 10},
		details: inspectionDetails{MemoryLimitBytes: 8, MemoryLimitAvailable: true},
	}
	usage, target, available := lossyStreamingBuildPressure[streamingOrderedFixture](
		All(&inspectRule[streamingOrderedFixture]{child: policy}, &streamingSelectionFixture{}),
	)
	require.True(t, available)
	require.Equal(t, uint64(20), usage)
	require.Equal(t, lossyBuildTarget(8), target)
	require.False(t, streamingRuleCanFit[streamingOrderedFixture](&streamingSelectionFixture{usage: 1, next: 1}))
}

func TestLossyStreamingRechecksBudgetAndDowngradesRemainingExactLeaves(t *testing.T) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	exactBytes := lossyAllBenchmarkExactBytes(t, constraints, ids, 4)
	limit := exactBytes / 4
	inspectors := make([]Inspector, 4)
	var aggregate Inspector
	schema := Inspect(&aggregate, Lossy(
		lossySelectionBenchmarkSchema("Equality", 4, inspectors),
		MemoryLimit(limit),
	))
	checkpoints, pressured := 0, 0
	index, _, err := buildIndexPhysicalAliases(schema, Zip(constraints, ids), false, nil, buildOptions{
		compilePhysicalAliases: true,
		enableStreaming:        true,
		observeWorkingUsage: func(usage, target uint64) {
			checkpoints++
			if usage > target {
				pressured++
			}
		},
	})
	require.NoError(t, err)
	require.NotNil(t, index)
	require.GreaterOrEqual(t, checkpoints, 2)
	require.GreaterOrEqual(t, pressured, 2, "fixture must require another downgrade after streaming starts")
	usage, ok := aggregate.Snapshot().MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, limit)
	lossyLeaves := 0
	for field := range inspectors {
		if inspectors[field].Snapshot().Mode() == RuleModeLossy {
			lossyLeaves++
		}
	}
	// Nested rebucketing releases more memory than the old overlapping grids,
	// so the final plan may preserve some exact siblings. More than one lossy
	// leaf proves that a leaf which was exact after initial compilation was
	// subsequently downgraded.
	require.Greater(t, lossyLeaves, 1)
}

func TestEveryStreamingRepresentationPreparesAndAppliesOneDowngrade(t *testing.T) {
	comparedBuckets := func() lossyComparedBuckets[int] {
		return lossyComparedBuckets[int]{
			compare: cmp.Compare[int], minimum: 1, maximum: 3, capacity: 3,
			boundaries: []int{1, 2, 3},
			buckets: []*roaring.Bitmap{
				roaring.BitmapOf(1), roaring.BitmapOf(2), roaring.BitmapOf(3),
			},
		}
	}
	t.Run("equality", func(t *testing.T) {
		rule := &lossyEqualityRule[streamingOrderedFixture, int]{
			get: streamingOrderedValue, wildcard: roaring.New(), bucketCount: 8,
			buckets: map[uint64]lossyEqualityPosting{
				0: {bits: roaring.BitmapOf(0)}, 1: {bits: roaring.BitmapOf(1)},
				2: {bits: roaring.BitmapOf(2)}, 3: {bits: roaring.BitmapOf(3)},
			},
		}
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Less(t, rule.bucketCount, uint64(8))
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
	})
	t.Run("numeric ordered", func(t *testing.T) {
		rule := &lossyOrderedRule[streamingOrderedFixture, int]{
			get: streamingOrderedValue, wildcard: roaring.New(), min: 1, max: 3, width: 1,
			buckets: []*roaring.Bitmap{roaring.BitmapOf(1), roaring.BitmapOf(2), roaring.BitmapOf(3)},
		}
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Less(t, len(rule.buckets), 3)
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
	})
	t.Run("comparator ordered", func(t *testing.T) {
		buckets := comparedBuckets()
		rule := &lossyComparedOrderedRule[streamingOrderedFixture, int]{
			get: streamingOrderedValue, compare: cmp.Compare[int], wildcard: roaring.New(),
			minimum: buckets.minimum, maximum: buckets.maximum, capacity: buckets.capacity,
			boundaries: buckets.boundaries, buckets: buckets.buckets,
		}
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Len(t, rule.buckets, 2)
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
	})
	t.Run("between", func(t *testing.T) {
		rule := &lossyBetweenRule[streamingOrderedFixture, int]{
			fromWildcard: roaring.New(), untilWildcard: roaring.New(),
			from: comparedBuckets(), until: comparedBuckets(),
		}
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Equal(t, 5, len(rule.from.buckets)+len(rule.until.buckets))
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
		rule.from = lossyComparedBuckets[int]{compare: cmp.Compare[int], capacity: 1,
			boundaries: []int{1}, buckets: []*roaring.Bitmap{roaring.BitmapOf(1)}}
		rule.until = comparedBuckets()
		rule.fitStreamingNext()
		require.Len(t, rule.until.buckets, 2)
	})
	t.Run("compare by", func(t *testing.T) {
		rule := &lossyCompareByRule[streamingOrderedFixture, int]{wildcard: roaring.New()}
		rule.present[0], rule.indexes[0] = true, comparedBuckets()
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Len(t, rule.indexes[0].buckets, 2)
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
	})
}
