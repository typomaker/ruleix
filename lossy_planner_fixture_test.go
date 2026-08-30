package ruleix

import (
	"cmp"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

type lossyPlannerFixtureConstraint struct {
	values  [16]int64
	present [16]bool
}

type lossyRepresentationFixture[T any] struct {
	name    string
	planner lossyAllPlanner[T]
}

func TestLossyLeafRepresentationFixtures(t *testing.T) {
	constraints := make([]lossyPlannerFixtureConstraint, 257)
	for i := range constraints {
		constraints[i].values[0] = int64(i)
		constraints[i].present[0] = i%17 != 0
	}
	ids := make([]int, len(constraints))
	for i := range ids {
		ids[i] = i
	}

	equality := func(v lossyPlannerFixtureConstraint) (int64, bool) { return v.values[0], v.present[0] }
	ordered := func(v lossyPlannerFixtureConstraint) (int64, bool) { return v.values[0], v.present[0] }
	stateIDs := &nodeIDAllocator{}
	statistics := &buildStatistics{}
	equalityState := Include(equality).newState(stateIDs, statistics).(*eqRule[lossyPlannerFixtureConstraint, int64])
	orderedState := GreaterOrEqual(ordered, cmp.Compare[int64]).newState(stateIDs, statistics).(*orderedRule[lossyPlannerFixtureConstraint, int64])
	for id, constraint := range constraints {
		equalityState.insert(constraint, uint32(id))
		orderedState.insert(constraint, uint32(id))
	}

	fixtures := []lossyRepresentationFixture[lossyPlannerFixtureConstraint]{
		{name: "equality", planner: equalityState.newLossyAllPlanner()},
		{name: "ordered", planner: orderedState.newLossyAllPlanner()},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			ladder, err := fixture.planner.representationLadder()
			require.NoError(t, err)
			repeated, err := fixture.planner.representationLadder()
			require.NoError(t, err)
			require.Equal(t, ladder, repeated)
			require.Greater(t, len(ladder), 2)

			exact, err := fixture.planner.compile(math.MaxUint64)
			require.NoError(t, err)
			exactDetails := inspectionDetailsOf(exact)
			require.Equal(t, RuleModeExact, inspectionModeOf(exact))
			require.NotZero(t, exactDetails.MemoryUsageBytes)
			require.Equal(t, exactDetails, ladder[0].details)

			minimumLimit, minimum, err := minimumLossyAllLimit(fixture.planner, exactDetails.MemoryUsageBytes)
			require.NoError(t, err)
			minimumDetails := inspectionDetailsOf(minimum)
			require.Equal(t, RuleModeLossy, inspectionModeOf(minimum))
			require.Equal(t, minimumLimit, minimumDetails.MemoryUsageBytes)
			if minimumLimit > 0 {
				_, err = fixture.planner.compile(minimumLimit - 1)
				require.Error(t, err)
			}
			require.Equal(t, minimumDetails, ladder[len(ladder)-1].details)
			for i, level := range ladder {
				selected, selectedErr := fixture.planner.compile(level.details.MemoryUsageBytes)
				require.NoError(t, selectedErr)
				require.Equal(t, level.details, inspectionDetailsOf(selected))
				if i == 0 {
					continue
				}
				require.Less(t, level.details.MemoryUsageBytes, ladder[i-1].details.MemoryUsageBytes)
				require.True(t, level.details.GranularityAvailable)
			}
		})
	}
}

func TestLossyLeafRepresentationUnsupportedScalarErrors(t *testing.T) {
	type unsupported struct{ value int }
	type constraint struct{ value unsupported }
	get := func(v constraint) (unsupported, bool) { return v.value, true }
	data := []constraint{{value: unsupported{value: 1}}}

	_, err := New[constraint, int](Lossy(Include(get), MemoryLimit(1024))).Build(Zip(data, []int{1}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "supported scalar")
	_, err = New[constraint, int](Lossy(GreaterOrEqual(get, func(a, b unsupported) int {
		return cmp.Compare(a.value, b.value)
	}), MemoryLimit(1024))).Build(Zip(data, []int{1}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "supported scalar")
}

func TestLossyMemoryAccountingOverflow(t *testing.T) {
	total, ok := addLossyMemory(math.MaxUint64-1, 1)
	require.True(t, ok)
	require.Equal(t, uint64(math.MaxUint64), total)
	_, ok = addLossyMemory(total, 1)
	require.False(t, ok)
}

type lossyAggregateFixture struct {
	name          string
	counts        [16]int
	wildcardEvery int
	nested        bool
}

func TestLossyAllPlannerFixtures(t *testing.T) {
	var skewed [16]int
	skewed[0] = 1024
	for i := 1; i < len(skewed); i++ {
		skewed[i] = 2
	}
	var equal [16]int
	for i := range equal {
		equal[i] = 64
	}

	fixtures := []lossyAggregateFixture{
		{name: "single-heavy", counts: skewed},
		{name: "equal-size-tie", counts: equal},
		{name: "nested-all", counts: skewed, nested: true},
		{name: "wildcard-heavy", counts: equal, wildcardEvery: 2},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			constraints := makeLossyAggregateFixtureData(fixture)
			ids := make([]int, len(constraints))
			for i := range ids {
				ids[i] = i
			}
			exactRule, inspectedRule, inspectors := makeLossyAggregateFixtureRules(fixture)
			exact, err := New[lossyPlannerFixtureConstraint, int](exactRule).Build(Zip(constraints, ids))
			require.NoError(t, err)

			_, err = New[lossyPlannerFixtureConstraint, int](Lossy(inspectedRule, MemoryLimit(math.MaxUint64))).Build(Zip(constraints, ids))
			require.NoError(t, err)
			var exactUsage uint64
			for i := range inspectors {
				usage, ok := inspectors[i].Snapshot().MemoryUsage()
				require.True(t, ok)
				exactUsage += usage
			}
			require.Greater(t, exactUsage, uint64(1))

			exactRule, inspectedRule, inspectors = makeLossyAggregateFixtureRules(fixture)
			limit := exactUsage - 1
			var aggregate Inspector
			approximate, err := New[lossyPlannerFixtureConstraint, int](Inspect(&aggregate, Lossy(inspectedRule, MemoryLimit(limit)))).Build(Zip(constraints, ids))
			require.NoError(t, err)
			usage, ok := aggregate.Snapshot().MemoryUsage()
			require.True(t, ok)
			require.LessOrEqual(t, usage, limit)

			lossyLeaves := 0
			var lossyIndexes []int
			for i := range inspectors {
				if inspectors[i].Snapshot().Mode() == RuleModeLossy {
					lossyLeaves++
					lossyIndexes = append(lossyIndexes, i)
				}
			}
			t.Logf("proportional migration baseline: lossy leaves=%v at limit=%d exact=%d retained=%d", lossyIndexes, limit, exactUsage, usage)
			require.Positive(t, lossyLeaves)

			for i := 0; i < len(constraints); i += 29 {
				var want, got []int
				exact.Search(constraints[i], &want)
				approximate.Search(constraints[i], &got)
				requireSupersetComparable(t, want, got)
			}
		})
	}
}

func TestLossyAllMinimumBudgetFixtures(t *testing.T) {
	fixture := lossyAggregateFixture{name: "minimum", wildcardEvery: 3}
	for i := range fixture.counts {
		fixture.counts[i] = 32 + i
	}
	constraints := makeLossyAggregateFixtureData(fixture)
	ids := make([]int, len(constraints))
	for i := range ids {
		ids[i] = i
	}
	_, rule, _ := makeLossyAggregateFixtureRules(fixture)
	state := rule.newState(&nodeIDAllocator{}, &buildStatistics{})
	for i, constraint := range constraints {
		state.insert(constraint, uint32(i))
	}
	leaves := []lossyAllLeaf[lossyPlannerFixtureConstraint]{}
	require.NoError(t, collectLossyAllLeaves(state, &leaves))
	var minimum uint64
	for _, leaf := range leaves {
		value, _, err := minimumLossyAllLimit(leaf.planner, leaf.exact)
		require.NoError(t, err)
		minimum += value
	}

	_, rule, _ = makeLossyAggregateFixtureRules(fixture)
	_, err := New[lossyPlannerFixtureConstraint, int](Lossy(rule, MemoryLimit(minimum))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	_, rule, _ = makeLossyAggregateFixtureRules(fixture)
	_, err = New[lossyPlannerFixtureConstraint, int](Lossy(rule, MemoryLimit(minimum-1))).Build(Zip(constraints, ids))
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot fit")
}

func makeLossyAggregateFixtureData(fixture lossyAggregateFixture) []lossyPlannerFixtureConstraint {
	maximum := 0
	for _, count := range fixture.counts {
		maximum = max(maximum, count)
	}
	constraints := make([]lossyPlannerFixtureConstraint, maximum)
	for row := range constraints {
		for column, count := range fixture.counts {
			constraints[row].values[column] = int64(row % max(count, 1))
			constraints[row].present[column] = fixture.wildcardEvery == 0 || row%fixture.wildcardEvery != 0
		}
	}
	return constraints
}

func makeLossyAggregateFixtureRules(fixture lossyAggregateFixture) (Rule[lossyPlannerFixtureConstraint], Rule[lossyPlannerFixtureConstraint], [16]Inspector) {
	exactChildren := make([]Rule[lossyPlannerFixtureConstraint], 16)
	inspectedChildren := make([]Rule[lossyPlannerFixtureConstraint], 16)
	inspectors := [16]Inspector{}
	for column := range exactChildren {
		column := column
		get := func(v lossyPlannerFixtureConstraint) (int64, bool) { return v.values[column], v.present[column] }
		exactChildren[column] = Include(get)
		inspectedChildren[column] = Inspect(&inspectors[column], Include(get))
	}
	if !fixture.nested {
		return All(exactChildren...), All(inspectedChildren...), inspectors
	}
	return All(All(exactChildren[:8]...), All(exactChildren[8:]...)),
		All(All(inspectedChildren[:8]...), All(inspectedChildren[8:]...)), inspectors
}
