package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type streamingOrderedFixture struct{ value int }

func testEqualityValues(postings map[uint64]*roaring.Bitmap) equalityIndex[uint64] {
	values := newEqualityIndex[uint64](len(postings))
	for key, bits := range postings {
		values.addSet(key, &equalitySet{bits: bits})
	}
	return values
}

func streamingOrderedValue(v streamingOrderedFixture) (int, bool) { return v.value, true }

func TestLossyOrderedStreamingRegridsExpandedRange(t *testing.T) {
	rule := &orderedRule[streamingOrderedFixture, int]{
		get: streamingOrderedValue, compare: cmp.Compare[int], dir: greaterThan, inclusive: true,
		index: newOrderedIndex(cmp.Compare[int]), wildcard: roaring.New(),
	}
	rule.index.insertPosting(10, roaring.BitmapOf(0))
	rule.index.insertPosting(19, roaring.BitmapOf(1))
	rule.insert(streamingOrderedFixture{value: 30}, 2)
	if got := rule.index.buildStatistics().uniqueValues; got != 3 {
		t.Fatalf("expanded index has %d classes, want 3", got)
	}
	for id, stored := range []int{10, 19, 30} {
		for query := stored; query <= 35; query++ {
			if !rule.matchesID(streamingOrderedFixture{value: query}, uint32(id)) {
				t.Fatalf("stored %d disappeared for query %d", stored, query)
			}
		}
	}
}

func TestLossyEqualityStreamingRoundsKeysWithoutDroppingIDs(t *testing.T) {
	rule := &eqRule[streamingOrderedFixture, int, uint64]{
		quantizer: newEqualityQuantizer(15),
		coarsen:   coarsenEqualityHash, less: lessEqualityHash,
		values: testEqualityValues(map[uint64]*roaring.Bitmap{
			0: roaring.BitmapOf(0), 1 << 62: roaring.BitmapOf(1),
			2 << 62: roaring.BitmapOf(2), 3 << 62: roaring.BitmapOf(3),
		}),
	}
	rule.rebuildEqualityNext()
	require.Equal(t, uint32(16), rule.quantizer.level)
	seen := roaring.New()
	for index := range rule.values.sets {
		rule.values.sets[index].addTo(seen)
	}
	if seen.GetCardinality() != 4 {
		t.Fatalf("rebuild retained %d IDs, want 4", seen.GetCardinality())
	}
	if len(rule.values.sets) != 2 {
		t.Fatalf("rebuild produced %d keys, want 2", len(rule.values.sets))
	}
}

func TestLossyEqualityRepeatedRebuildKeepsEveryPosting(t *testing.T) {
	codec, err := compileEqualityCodec[[16]byte]()
	require.NoError(t, err)
	rule := &eqRule[codecFixtureConstraint[[16]byte], [16]byte, uint64]{
		get:      func(v codecFixtureConstraint[[16]byte]) ([16]byte, bool) { return v.value, true },
		wildcard: roaring.New(), codec: codec, quantizer: newEqualityQuantizer(1),
		encode: hashedEqualityEncoder(codec), coarsen: coarsenEqualityHash, less: lessEqualityHash,
		values: newEqualityIndex[uint64](5000),
	}
	values := make([][16]byte, 5000)
	for id := range values {
		values[id][0] = byte(id)
		values[id][1] = byte(id >> 8)
		rule.insert(codecFixtureConstraint[[16]byte]{value: values[id]}, uint32(id))
	}
	for range 8 {
		rule.fitStreamingNext()
	}
	for id, value := range values {
		require.True(t, rule.matchesID(codecFixtureConstraint[[16]byte]{value: value}, uint32(id)), "id %d", id)
	}
}

func TestEqualityIndexBitmapInsertConvertsCompactPosting(t *testing.T) {
	values := newEqualityIndex[int](2)
	values.add(1, 1)
	values.addBitmap(1, 2)
	values.addBitmap(2, 3)
	require.Equal(t, []uint32{1, 2}, values.get(1).bits.ToArray())
	require.Equal(t, []uint32{3}, values.get(2).bits.ToArray())
}

type streamingSelectionFixture struct {
	usage, next uint64
	granularity uint64
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
	details.GranularityValue, details.GranularityAvailable = r.granularity, true
	if details.GranularityValue == 0 {
		details.GranularityValue = 2
	}
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

func TestStreamingAggregateTieBreaksBySchemaOrderAfterCurrentUsage(t *testing.T) {
	first := &streamingSelectionFixture{usage: 100, next: 80, granularity: 2}
	second := &streamingSelectionFixture{usage: 100, next: 80, granularity: 99}

	fitStreamingAggregate(All[streamingOrderedFixture](first, second), 180)

	require.Equal(t, 1, first.fits)
	require.Zero(t, second.fits)
}

func TestStreamingPressureStopsAtSoftTarget(t *testing.T) {
	leaf := &streamingSelectionFixture{usage: 130, next: 110}
	policy := &inspectionDetailsRule[streamingOrderedFixture]{
		child: leaf,
		details: inspectionDetails{
			MemoryLimitBytes: 100, MemoryLimitAvailable: true,
		},
	}

	fitStreamingPolicies(policy)

	require.Equal(t, uint64(110), leaf.usage)
	require.Equal(t, 1, leaf.fits)
}

func TestStreamingPressureHonorsNestedPolicyBeforeAncestorPool(t *testing.T) {
	innerLeaf := &streamingSelectionFixture{usage: 130, next: 100}
	inner := &inspectionDetailsRule[streamingOrderedFixture]{
		child: innerLeaf,
		details: inspectionDetails{
			MemoryLimitBytes: 100, MemoryLimitAvailable: true,
		},
	}
	sibling := &streamingSelectionFixture{usage: 150, next: 120}
	outer := &inspectionDetailsRule[streamingOrderedFixture]{
		child: All[streamingOrderedFixture](inner, sibling),
		details: inspectionDetails{
			MemoryLimitBytes: 200, MemoryLimitAvailable: true,
		},
	}

	fitStreamingPolicies(outer)

	require.Equal(t, 1, innerLeaf.fits)
	require.Zero(t, sibling.fits, "the descendant transition reaches the ancestor soft target")
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

	stuck := &streamingSelectionFixture{usage: 10, next: 10}
	fitStreamingAggregate[streamingOrderedFixture](stuck, 5)
	require.Zero(t, stuck.fits, "a terminal leaf must not trigger an emergency fallback")
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
	constraints, ids := lossyAllBenchmarkData(lossyBuildPressureInterval*3 + 512)
	exactBytes := lossyAllBenchmarkExactBytes(t, constraints, ids, 4)
	// Include three pressure intervals so stable hashing still exercises a
	// second downgrade after the initial exact-to-streaming transition.
	limit := exactBytes / 5
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
	// Nested rebuilding releases more memory than the old overlapping grids,
	// so the final plan may preserve some exact siblings. More than one lossy
	// leaf proves that a leaf which was exact after initial compilation was
	// subsequently downgraded.
	require.Greater(t, lossyLeaves, 1)
}

func TestEveryStreamingRepresentationPreparesAndAppliesOneDowngrade(t *testing.T) {
	orderedSide := func(dir direction) *orderedRule[streamingOrderedFixture, int] {
		side := &orderedRule[streamingOrderedFixture, int]{
			get: streamingOrderedValue, compare: cmp.Compare[int], dir: dir,
			wildcard: roaring.New(), index: newOrderedIndex(cmp.Compare[int]),
		}
		for value := 1; value <= 3; value++ {
			side.index.insertPosting(value, roaring.BitmapOf(uint32(value)))
		}
		return side
	}
	t.Run("equality", func(t *testing.T) {
		rule := &eqRule[streamingOrderedFixture, int, uint64]{
			get: streamingOrderedValue, wildcard: roaring.New(), quantizer: newEqualityQuantizer(8),
			coarsen: coarsenEqualityHash, less: lessEqualityHash,
			values: testEqualityValues(map[uint64]*roaring.Bitmap{
				0: roaring.BitmapOf(0), 1: roaring.BitmapOf(1),
				2: roaring.BitmapOf(2), 3: roaring.BitmapOf(3),
			}),
		}
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Equal(t, uint32(9), rule.quantizer.level)
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
	})
	t.Run("numeric ordered", func(t *testing.T) {
		common := &orderedRule[streamingOrderedFixture, int]{
			get: streamingOrderedValue, compare: cmp.Compare[int], wildcard: roaring.New(),
			index: newOrderedIndex(cmp.Compare[int]),
		}
		common.index.insertPosting(1, roaring.BitmapOf(1))
		common.index.insertPosting(2, roaring.BitmapOf(2))
		common.index.insertPosting(3, roaring.BitmapOf(3))
		_, first, ok := common.prepareStreamingFirstGeneration()
		require.True(t, ok)
		rule := first.(*orderedRule[streamingOrderedFixture, int])
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Less(t, rule.index.buildStatistics().uniqueValues, 3)
		_, ok = rule.nextStreamingUsage()
		require.False(t, ok)
		rule.fitStreamingLimit(0)
		require.Equal(t, 1, rule.index.buildStatistics().uniqueValues)
		_, _, ok = rule.prepareStreamingNext()
		require.False(t, ok)
		require.IsType(t, rule, rule.newState(&nodeIDAllocator{}, &buildStatistics{}))
	})
	t.Run("between", func(t *testing.T) {
		rule := &betweenRule[streamingOrderedFixture, int]{
			from: orderedSide(greaterThan), until: orderedSide(lessThan), compare: cmp.Compare[int],
		}
		require.IsType(t, rule, rule.newState(&nodeIDAllocator{}, &buildStatistics{}))
		require.Equal(t, canonicalBetween, rule.canonicalDescriptor().representation)
		require.IsType(t, &matchAllRule[streamingOrderedFixture]{}, rule.optimize(0))
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Equal(t, 5, rule.from.index.buildStatistics().uniqueValues+
			rule.until.index.buildStatistics().uniqueValues)
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
		rule.from = orderedSide(greaterThan)
		rule.from.fitStreamingNext()
		rule.from.fitStreamingNext()
		rule.until = orderedSide(lessThan)
		rule.fitStreamingNext()
		require.Equal(t, 2, rule.until.index.buildStatistics().uniqueValues)
		rule.fitStreamingLimit(0)
		require.Equal(t, 2, rule.from.index.buildStatistics().uniqueValues+
			rule.until.index.buildStatistics().uniqueValues)
		rule.fitStreamingNext()
	})
	t.Run("compare by", func(t *testing.T) {
		common := &compareByRule[streamingOrderedFixture, int]{
			value: streamingOrderedValue, compare: cmp.Compare[int], wildcard: roaring.New(),
		}
		common.indexes[0] = &orderedSide(lessThan).index
		rule := common
		require.IsType(t, rule, rule.newState(&nodeIDAllocator{}, &buildStatistics{}))
		require.Equal(t, canonicalCompareBy, rule.canonicalDescriptor().representation)
		require.IsType(t, &matchAllRule[streamingOrderedFixture]{}, rule.optimize(0))
		_, apply, ok := rule.prepareStreamingNext()
		require.True(t, ok)
		apply()
		require.Equal(t, 2, rule.indexes[0].buildStatistics().uniqueValues)
		_, ok = rule.nextStreamingUsage()
		require.True(t, ok)
		rule.fitStreamingNext()
		rule.fitStreamingLimit(0)
		require.Equal(t, 1, rule.indexes[0].buildStatistics().uniqueValues)
		rule.fitStreamingNext()
	})
}
