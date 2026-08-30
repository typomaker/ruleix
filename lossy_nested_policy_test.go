package ruleix

import (
	"cmp"
	"fmt"
	"math"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

type nestedLossyConstraint struct {
	name                        string
	minimum, maximum, threshold int64
	namePresent                 bool
	minimumPresent              bool
	maximumPresent              bool
	thresholdPresent            bool
}

type nestedLossySnapshot struct {
	mode        RuleMode
	strategy    string
	usage       uint64
	limit       uint64
	effective   uint64
	granularity uint64
	hasLimit    bool
	hasGrain    bool
}

func snapshotNestedLossy(t *testing.T, inspector Inspector) nestedLossySnapshot {
	t.Helper()
	snapshot := inspector.Snapshot()
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	limit, hasLimit := snapshot.MemoryLimit()
	effective, hasEffective := snapshot.EffectiveMemoryLimit()
	require.Equal(t, hasLimit, hasEffective)
	granularity, hasGrain := snapshot.Granularity()
	return nestedLossySnapshot{
		mode: snapshot.Mode(), strategy: snapshot.Strategy(), usage: usage,
		limit: limit, effective: effective, granularity: granularity, hasLimit: hasLimit, hasGrain: hasGrain,
	}
}

func nestedLossyData(entries int) ([]nestedLossyConstraint, []int) {
	constraints := make([]nestedLossyConstraint, entries)
	ids := make([]int, entries)
	for i := range constraints {
		constraints[i] = nestedLossyConstraint{
			name: fmt.Sprintf("name-%03d", i%173), minimum: int64(i%97 - 48),
			maximum: int64(i%89 - 40), threshold: int64(i%67 - 30),
			namePresent: i%11 != 0, minimumPresent: i%13 != 0,
			maximumPresent: i%17 != 0, thresholdPresent: i%19 != 0,
		}
		ids[i] = (i * 7) % max(1, entries/3) // Deliberately repeat external IDs.
	}
	return constraints, ids
}

func nestedLossyGetters() (
	Getter[nestedLossyConstraint, string],
	Getter[nestedLossyConstraint, int64],
	Getter[nestedLossyConstraint, int64],
	Getter[nestedLossyConstraint, int64],
) {
	name := func(v nestedLossyConstraint) (string, bool) { return v.name, v.namePresent }
	minimum := func(v nestedLossyConstraint) (int64, bool) { return v.minimum, v.minimumPresent }
	maximum := func(v nestedLossyConstraint) (int64, bool) { return v.maximum, v.maximumPresent }
	threshold := func(v nestedLossyConstraint) (int64, bool) { return v.threshold, v.thresholdPresent }
	return name, minimum, maximum, threshold
}

func TestNestedLossyPolicyShapesRespectEveryLocalCap(t *testing.T) {
	constraints, ids := nestedLossyData(512)
	name, minimum, maximum, _ := nestedLossyGetters()

	var probe [3]Inspector
	_, err := New[nestedLossyConstraint, int](Lossy(All(
		Inspect(&probe[0], Include(name)),
		Inspect(&probe[1], GreaterOrEqual(minimum, cmp.Compare[int64])),
		Inspect(&probe[2], LessOrEqual(maximum, cmp.Compare[int64])),
	), MemoryLimit(math.MaxUint64))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	exact := [3]uint64{}
	for i := range probe {
		exact[i] = snapshotNestedLossy(t, probe[i]).usage
		require.Greater(t, exact[i], uint64(1))
	}

	tests := []struct {
		name   string
		build  func(*[3]Inspector, *Inspector) Rule[nestedLossyConstraint]
		limits [3]uint64
		outer  uint64
	}{
		{
			name: "direct-lossy-lossy",
			build: func(leaves *[3]Inspector, outer *Inspector) Rule[nestedLossyConstraint] {
				return Inspect(outer, Lossy(Lossy(Inspect(&leaves[0], Include(name)), MemoryLimit(exact[0]-1)), MemoryLimit(exact[0]+1)))
			},
			limits: [3]uint64{exact[0] - 1}, outer: exact[0] + 1,
		},
		{
			name: "all-containing-local-policy",
			build: func(leaves *[3]Inspector, outer *Inspector) Rule[nestedLossyConstraint] {
				return Inspect(outer, Lossy(All(
					Lossy(Inspect(&leaves[0], Include(name)), MemoryLimit(exact[0]-1)),
					Inspect(&leaves[1], GreaterOrEqual(minimum, cmp.Compare[int64])),
				), MemoryLimit(exact[0]+exact[1]-2)))
			},
			limits: [3]uint64{exact[0] - 1}, outer: exact[0] + exact[1] - 2,
		},
		{
			name: "nested-groups-and-sibling-caps",
			build: func(leaves *[3]Inspector, outer *Inspector) Rule[nestedLossyConstraint] {
				return Inspect(outer, Lossy(All(
					All(Lossy(Inspect(&leaves[0], Include(name)), MemoryLimit(exact[0]-1))),
					All(Lossy(Inspect(&leaves[1], GreaterOrEqual(minimum, cmp.Compare[int64])), MemoryLimit(exact[1]-1))),
				), MemoryLimit(exact[0]+exact[1]-3)))
			},
			limits: [3]uint64{exact[0] - 1, exact[1] - 1}, outer: exact[0] + exact[1] - 3,
		},
		{
			name: "three-policy-levels",
			build: func(leaves *[3]Inspector, outer *Inspector) Rule[nestedLossyConstraint] {
				middle := Lossy(All(
					Lossy(Inspect(&leaves[0], Include(name)), MemoryLimit(exact[0]-1)),
					Inspect(&leaves[1], GreaterOrEqual(minimum, cmp.Compare[int64])),
				), MemoryLimit(exact[0]+exact[1]-2))
				return Inspect(outer, Lossy(All(middle, Inspect(&leaves[2], LessOrEqual(maximum, cmp.Compare[int64]))),
					MemoryLimit(exact[0]+exact[1]+exact[2]-3)))
			},
			limits: [3]uint64{exact[0] - 1}, outer: exact[0] + exact[1] + exact[2] - 3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var leaves [3]Inspector
			var outer Inspector
			_, err := New[nestedLossyConstraint, int](tc.build(&leaves, &outer)).Build(Zip(constraints, ids))
			require.NoError(t, err)
			outerSnapshot := snapshotNestedLossy(t, outer)
			require.LessOrEqual(t, outerSnapshot.usage, tc.outer)
			for i, localLimit := range tc.limits {
				if localLimit == 0 {
					continue
				}
				leaf := snapshotNestedLossy(t, leaves[i])
				require.LessOrEqual(t, leaf.usage, localLimit)
			}
		})
	}
}

func TestNestedLossyBoundaryAndPolicyPathMatrix(t *testing.T) {
	constraints, ids := nestedLossyData(257)
	for i := range ids {
		ids[i] = i
	}
	name, _, _, _ := nestedLossyGetters()
	state := Include(name).newState(&nodeIDAllocator{}, &buildStatistics{})
	for i, constraint := range constraints {
		state.insert(constraint, uint32(i))
	}
	planner := state.(lossyAllCompiler[nestedLossyConstraint]).newLossyAllPlanner()
	ladder, err := planner.representationLadder()
	require.NoError(t, err)
	exact := ladder[0].details.MemoryUsageBytes
	minimum := ladder[len(ladder)-1].details.MemoryUsageBytes
	require.Greater(t, exact, minimum)

	for _, tc := range []struct {
		name       string
		innerLimit uint64
		outerLimit uint64
	}{
		{name: "exact-fit", innerLimit: exact, outerLimit: exact},
		{name: "one-byte-under", innerLimit: exact, outerLimit: exact - 1},
		{name: "minimum-fit", innerLimit: minimum, outerLimit: minimum},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inner, outer Inspector
			rule := Inspect(&outer, Lossy(Inspect(&inner, Lossy(Include(name), MemoryLimit(tc.innerLimit))), MemoryLimit(tc.outerLimit)))
			_, err := New[nestedLossyConstraint, int](rule).Build(Zip(constraints, ids))
			require.NoError(t, err)
			require.LessOrEqual(t, snapshotNestedLossy(t, inner).usage, min(tc.innerLimit, tc.outerLimit))
			require.LessOrEqual(t, snapshotNestedLossy(t, outer).usage, tc.outerLimit)
		})
	}

	_, err = New[nestedLossyConstraint, int](Lossy(All(
		Lossy(Include(name), MemoryLimit(minimum-1)),
	), MemoryLimit(exact))).Build(Zip(constraints, ids))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Lossy/child/All[0]")
	require.NotContains(t, err.Error(), fmt.Sprint(minimum))

	_, err = New[nestedLossyConstraint, int](Lossy(All(
		Lossy(Include(name)),
	), MemoryLimit(exact))).Build(Zip(constraints, ids))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Lossy/child/All[0]")
	require.Contains(t, err.Error(), "MemoryLimit")
}

func TestInspectReportsConfiguredAndAncestorEffectiveLossyLimits(t *testing.T) {
	constraints, ids := nestedLossyData(257)
	for i := range ids {
		ids[i] = i
	}
	name, _, _, _ := nestedLossyGetters()

	var exact Inspector
	_, err := New[nestedLossyConstraint, int](Inspect(&exact,
		Lossy(Include(name), MemoryLimit(math.MaxUint64)))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	exactUsage := snapshotNestedLossy(t, exact).usage

	var inner, outer Inspector
	innerLimit := exactUsage
	outerLimit := exactUsage - 1
	rule := Inspect(&outer, Lossy(
		Inspect(&inner, Lossy(Include(name), MemoryLimit(innerLimit))),
		MemoryLimit(outerLimit)))
	_, err = New[nestedLossyConstraint, int](rule).Build(Zip(constraints, ids))
	require.NoError(t, err)

	innerSnapshot := snapshotNestedLossy(t, inner)
	outerSnapshot := snapshotNestedLossy(t, outer)
	require.Equal(t, innerLimit, innerSnapshot.limit)
	require.Less(t, innerSnapshot.effective, innerSnapshot.limit)
	require.GreaterOrEqual(t, innerSnapshot.effective, innerSnapshot.usage)
	require.Equal(t, outerLimit, outerSnapshot.limit)
	require.Equal(t, outerLimit, outerSnapshot.effective)
}

func TestNestedLossyAccountingOverflowReportsStablePolicyPath(t *testing.T) {
	leaves := []lossyAllLeaf[int]{
		{ladder: []lossyRepresentation[int]{{details: representationDetails(math.MaxUint64, 0, 0, 0, false)}}},
		{ladder: []lossyRepresentation[int]{{details: representationDetails(1, 0, 0, 0, false)}}},
	}
	plan := &lossyPolicyPlan[int]{kind: lossyPlanPolicy, first: 0, end: 2, limit: math.MaxUint64, path: "Lossy/child/All[1]/child"}
	err := enforceLossyPolicyCaps(plan, leaves)
	require.EqualError(t, err, "ruleix: Lossy/child/All[1]/child: memory accounting overflow")
}

func TestNestedLossyRandomizedSupersetAndDeterministicDiagnostics(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			random := rand.New(rand.NewSource(seed))
			constraints, ids := nestedLossyData(384)
			for i := range constraints {
				constraints[i].name = fmt.Sprintf("value-%02d", random.Intn(41))
				constraints[i].minimum = int64(random.Intn(65) - 32)
				constraints[i].maximum = int64(random.Intn(65) - 32)
				constraints[i].threshold = int64(random.Intn(65) - 32)
				constraints[i].namePresent = random.Intn(5) != 0
				constraints[i].minimumPresent = random.Intn(7) != 0
				constraints[i].maximumPresent = random.Intn(7) != 0
				constraints[i].thresholdPresent = random.Intn(6) != 0
				ids[i] = random.Intn(91)
			}
			name, minimum, maximum, threshold := nestedLossyGetters()
			exactRule := All(Include(name), GreaterOrEqual(minimum, cmp.Compare[int64]),
				LessOrEqual(maximum, cmp.Compare[int64]), Greater(threshold, cmp.Compare[int64]))
			exactIndex, err := New[nestedLossyConstraint, int](exactRule).Build(Zip(constraints, ids))
			require.NoError(t, err)

			var probe Inspector
			_, err = New[nestedLossyConstraint, int](Inspect(&probe, Lossy(exactRule, MemoryLimit(math.MaxUint64)))).Build(Zip(constraints, ids))
			require.NoError(t, err)
			exactUsage := snapshotNestedLossy(t, probe).usage
			limit := exactUsage * 3 / 4

			var baseline []nestedLossySnapshot
			for repetition := 0; repetition < 5; repetition++ {
				var leaves [4]Inspector
				var inner, middle, outer Inspector
				innerRule := Inspect(&inner, Lossy(All(
					Inspect(&leaves[0], Include(name)),
					Inspect(&leaves[1], GreaterOrEqual(minimum, cmp.Compare[int64])),
				), MemoryLimit(exactUsage)))
				middleRule := Inspect(&middle, Lossy(All(innerRule,
					Inspect(&leaves[2], LessOrEqual(maximum, cmp.Compare[int64]))), MemoryLimit(exactUsage)))
				rule := Inspect(&outer, Lossy(All(middleRule,
					Inspect(&leaves[3], Greater(threshold, cmp.Compare[int64]))), MemoryLimit(limit)))
				approximate, err := New[nestedLossyConstraint, int](rule).Build(Zip(constraints, ids))
				require.NoError(t, err)
				current := make([]nestedLossySnapshot, 0, 7)
				for i := range leaves {
					current = append(current, snapshotNestedLossy(t, leaves[i]))
				}
				current = append(current, snapshotNestedLossy(t, inner), snapshotNestedLossy(t, middle), snapshotNestedLossy(t, outer))
				require.LessOrEqual(t, current[4].usage, exactUsage)
				require.LessOrEqual(t, current[5].usage, exactUsage)
				require.LessOrEqual(t, current[6].usage, limit)
				if repetition == 0 {
					baseline = current
				} else {
					require.Equal(t, baseline, current)
				}

				for queryIndex := 0; queryIndex < 80; queryIndex++ {
					query := nestedLossyConstraint{
						name:    fmt.Sprintf("value-%02d", random.Intn(48)),
						minimum: int64(random.Intn(81) - 40), maximum: int64(random.Intn(81) - 40),
						threshold:   int64(random.Intn(81) - 40),
						namePresent: random.Intn(4) != 0, minimumPresent: random.Intn(4) != 0,
						maximumPresent: random.Intn(4) != 0, thresholdPresent: random.Intn(4) != 0,
					}
					var want, got []int
					exactIndex.Search(query, &want)
					approximate.Search(query, &got)
					requireSupersetComparable(t, want, got)
				}
			}
		})
	}
}
