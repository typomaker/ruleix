package ruleix

import (
	"cmp"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestLossyOrderedEstimateAndIDMatchAgreeWithSearch(t *testing.T) {
	wildcard := roaring.BitmapOf(8)
	rule := &lossyOrderedRule[lossyConstraint, int64]{
		get:       func(v lossyConstraint) (int64, bool) { return v.minimum, v.present },
		encoder:   mustCompileOrderedKeyEncoder[int64](),
		dir:       greaterThan,
		inclusive: true,
		wildcard:  wildcard,
		min:       orderedScalarKeyOrPanic(int64(0)),
		max:       orderedScalarKeyOrPanic(int64(29)),
		width:     10,
		buckets:   []*roaring.Bitmap{roaring.BitmapOf(0, 1), roaring.BitmapOf(2, 3), roaring.BitmapOf(4, 5)},
	}
	for _, query := range []lossyConstraint{{minimum: -1, present: true}, {minimum: 0, present: true}, {minimum: 15, present: true}, {minimum: 40, present: true}, {}} {
		bits := roaring.New()
		rule.search(query, bits, newBitmapPool())
		require.Equal(t, bits.GetCardinality(), rule.estimateCardinality(query))
		for id := uint32(0); id < 10; id++ {
			require.Equal(t, bits.Contains(id), rule.matchesID(query, id))
		}
	}
}

func orderedScalarKeyOrPanic[V any](value V) uint64 {
	key, ok := orderedScalarKey(any(value))
	if !ok {
		panic("unsupported test scalar")
	}
	return key
}

func BenchmarkLossyAllSelectiveOrderedPlanning(b *testing.B) {
	const entries = 100_000
	query := lossyConstraint{minimum: entries - 1, present: true}
	minKey := orderedScalarKeyOrPanic(int64(0))
	maxKey := orderedScalarKeyOrPanic(int64(entries - 1))
	broad := roaring.New()
	broad.AddRange(0, entries)
	selective := &lossyOrderedRule[lossyConstraint, int64]{
		get:       func(v lossyConstraint) (int64, bool) { return v.minimum, v.present },
		encoder:   mustCompileOrderedKeyEncoder[int64](),
		dir:       lessThan,
		inclusive: true,
		wildcard:  roaring.New(),
		min:       minKey,
		max:       maxKey,
		width:     1,
		buckets:   make([]*roaring.Bitmap, entries),
	}
	selective.buckets[entries-1] = roaring.BitmapOf(entries - 1)
	children := make([]Rule[lossyConstraint], 0, 8)
	for range 7 {
		children = append(children, &matchAllRule[lossyConstraint]{bits: broad})
	}
	children = append(children, selective)
	root := &allRule[lossyConstraint]{children: children}
	unknownChildren := append([]Rule[lossyConstraint](nil), children...)
	unknownChildren[len(unknownChildren)-1] = &unknownEstimateRule[lossyConstraint]{child: selective}
	unknownRoot := &allRule[lossyConstraint]{children: unknownChildren}

	for name, candidate := range map[string]*allRule[lossyConstraint]{"Adaptive": root, "UnknownEstimate": unknownRoot} {
		b.Run(name, func(b *testing.B) {
			pool := newBitmapPool()
			b.ReportAllocs()
			for range b.N {
				result := pool.get()
				candidate.search(query, result, pool)
				lossyPlannerBenchmarkCardinality = result.GetCardinality()
				pool.put(result)
			}
		})
	}
}

func TestLossyPolicyValidation(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, true }
	empty := Zip([]lossyConstraint{}, []int{})
	_, err := New[lossyConstraint, int](Lossy(Include(get))).Build(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "MemoryLimit")
	_, err = New[lossyConstraint, int](Lossy(Include(get), MemoryLimit(1), MemoryLimit(2))).Build(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one")
	_, err = New[lossyConstraint, int](Lossy(All(Lossy(Include(get), MemoryLimit(100))), MemoryLimit(100))).Build(empty)
	require.NoError(t, err)
}

func TestNestedLossyPoliciesRespectInnerAndOuterCaps(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	constraints := make([]lossyConstraint, 512)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{name: fmt.Sprintf("nested-%d", i), present: true}
		ids[i] = i
	}
	var exact Inspector
	_, err := New[lossyConstraint, int](
		Inspect(&exact, Lossy(Include(get), MemoryLimit(math.MaxUint64))),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)
	exactUsage, ok := exact.Snapshot().MemoryUsage()
	require.True(t, ok)
	require.Greater(t, exactUsage, uint64(1))

	for _, tc := range []struct {
		name         string
		inner, outer uint64
	}{
		{name: "inner-smaller", inner: exactUsage - 1, outer: exactUsage + 1},
		{name: "outer-smaller", inner: exactUsage + 1, outer: exactUsage - 1},
		{name: "equal", inner: exactUsage - 1, outer: exactUsage - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var inner, outer Inspector
			schema := Inspect(&outer, Lossy(
				Inspect(&inner, Lossy(Include(get), MemoryLimit(tc.inner))),
				MemoryLimit(tc.outer),
			))
			index, err := New[lossyConstraint, int](schema).Build(Zip(constraints, ids))
			require.NoError(t, err)
			innerUsage, ok := inner.Snapshot().MemoryUsage()
			require.True(t, ok)
			require.LessOrEqual(t, innerUsage, min(tc.inner, tc.outer))
			outerUsage, ok := outer.Snapshot().MemoryUsage()
			require.True(t, ok)
			require.Equal(t, innerUsage, outerUsage)
			require.LessOrEqual(t, outerUsage, tc.outer)
			innerLimit, ok := inner.Snapshot().MemoryLimit()
			require.True(t, ok)
			require.Equal(t, min(tc.inner, tc.outer), innerLimit)

			var got []int
			index.Search(lossyConstraint{name: "nested-17", present: true}, &got)
			require.Contains(t, got, 17)
		})
	}
}

func TestNestedLossyImpossibleBudgetReportsPolicyPath(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	constraints := []lossyConstraint{{name: "one", present: true}}
	_, err := New[lossyConstraint, int](Lossy(All(
		Lossy(Include(get), MemoryLimit(1)),
	), MemoryLimit(math.MaxUint64))).Build(Zip(constraints, []int{1}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "Lossy/child/All[0]")
	require.Contains(t, err.Error(), "cannot fit")
}

//nolint:lll // Full exact and lossy constructors are kept adjacent for comparison.
func TestLossyAllNeverDropsExactMatches(t *testing.T) {
	name := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	minimum := func(v lossyConstraint) (int64, bool) { return v.minimum, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{name: fmt.Sprintf("customer-%d", i), minimum: int64(i - 1000), present: true}
		ids[i] = i
	}
	exact, err := New[lossyConstraint, int](
		All(Include(name), GreaterOrEqual(minimum, cmp.Compare[int64])),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	approximate, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(
		All(Include(name), GreaterOrEqual(minimum, cmp.Compare[int64])),
		MemoryLimit(16000),
	))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for i := 0; i < 100; i++ {
		query := lossyConstraint{name: fmt.Sprintf("customer-%d", i*19), minimum: int64(i*23 - 1000), present: true}
		var want, got []int
		exact.Search(query, &want)
		approximate.Search(query, &got)
		requireSuperset(t, want, got)
	}
	snapshot := inspector.Snapshot()
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(16000))
	actualLimit, ok := snapshot.MemoryLimit()
	require.True(t, ok)
	require.Equal(t, uint64(16000), actualLimit)
	require.Equal(t, RuleModeLossy, snapshot.Mode())
	require.Equal(t, "all", snapshot.Strategy())
}

func TestLossyCompositeProductionValueTypesNeverDropExactMatches(t *testing.T) {
	type composite struct{ major, minor, patch int }
	type constraint struct {
		uuid        [16]byte
		ab          [2]string
		version     composite
		from, until time.Time
		op          Operator
	}
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	constraints := make([]constraint, 128)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = constraint{
			uuid: [16]byte{byte(i), byte(i >> 8)}, ab: [2]string{"experiment", fmt.Sprint(i % 7)},
			version: composite{major: i / 16, minor: i % 16},
			from:    base.Add(time.Duration(i) * time.Hour), until: base.Add(time.Duration(i+24) * time.Hour),
			op: OperatorGTE,
		}
		ids[i] = i
	}
	rules := []Rule[constraint]{
		Include(func(v constraint) ([16]byte, bool) { return v.uuid, true }),
		Include(func(v constraint) ([2]string, bool) { return v.ab, true }),
		GreaterOrEqual(func(v constraint) (composite, bool) { return v.version, true }, func(a, b composite) int {
			if result := cmp.Compare(a.major, b.major); result != 0 {
				return result
			}
			if result := cmp.Compare(a.minor, b.minor); result != 0 {
				return result
			}
			return cmp.Compare(a.patch, b.patch)
		}),
		Between(
			func(v constraint) (time.Time, bool) { return v.from, true },
			func(v constraint) (time.Time, bool) { return v.until, true },
			time.Time.Compare,
		),
		CompareBy(
			func(v constraint) (composite, bool) { return v.version, true },
			func(v constraint) (Operator, bool) { return v.op, true },
			func(a, b composite) int {
				if result := cmp.Compare(a.major, b.major); result != 0 {
					return result
				}
				return cmp.Compare(a.minor, b.minor)
			},
		),
	}
	for ruleIndex, exactRule := range rules {
		exact, err := New[constraint, int](exactRule).Build(Zip(constraints, ids))
		require.NoError(t, err)
		var inspector Inspector
		approximate, err := New[constraint, int](Inspect(
			&inspector,
			Lossy(rules[ruleIndex], MemoryLimit(2048)),
		)).Build(Zip(constraints, ids))
		require.NoError(t, err)
		_, modeAvailable := inspector.Snapshot().MemoryUsage()
		require.True(t, modeAvailable)
		for i := range constraints {
			var want, got []int
			exact.Search(constraints[i], &want)
			approximate.Search(constraints[i], &got)
			gotSet := make(map[int]struct{}, len(got))
			for _, id := range got {
				gotSet[id] = struct{}{}
			}
			for _, id := range want {
				_, found := gotSet[id]
				require.Truef(t, found, "rule %d query %d omitted ID %d", ruleIndex, i, id)
			}
		}
	}
}

func TestLossyBetweenOutwardBucketsNeverDropBoundaryMatches(t *testing.T) {
	type interval struct {
		from, until int
		present     bool
	}
	from := func(v interval) (int, bool) { return v.from, v.present }
	until := func(v interval) (int, bool) { return v.until, v.present }
	exactRule := Between(from, until, cmp.Compare[int])
	constraints := make([]interval, 1024)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = interval{from: i - 17, until: i + 31, present: i%97 != 0}
		ids[i] = i
	}
	queries := make([]interval, 0, 2200)
	for value := -50; value < 1050; value++ {
		queries = append(queries,
			interval{from: value, until: value, present: true},
			interval{from: value, until: value + 1, present: true},
		)
	}
	queries = append(queries, interval{})
	verifyLossySuperset(
		t, exactRule, Lossy(exactRule, MemoryLimit(8192)), constraints, ids, queries, true,
	)
}

func TestLossyCompareByBucketsNeverDropAnyOperatorMatch(t *testing.T) {
	type comparison struct {
		value   int
		op      Operator
		present bool
	}
	value := func(v comparison) (int, bool) { return v.value, v.present }
	operator := func(v comparison) (Operator, bool) { return v.op, v.present }
	exactRule := CompareBy(value, operator, cmp.Compare[int])
	constraints := make([]comparison, 2048)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = comparison{value: i%521 - 260, op: Operator(i % 5), present: i%101 != 0}
		ids[i] = i
	}
	queries := make([]comparison, 0, 602)
	for query := -300; query <= 300; query++ {
		queries = append(queries, comparison{value: query, present: true})
	}
	queries = append(queries, comparison{})
	verifyLossySuperset(
		t, exactRule, Lossy(exactRule, MemoryLimit(8192)), constraints, ids, queries, true,
	)
}

func TestLossyAllRetainsExactChildrenWhenCompositeFits(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, true }
	var inspector Inspector
	_, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(
		All(Include(get), Include(get)),
		MemoryLimit(1<<20),
	))).Build(Zip([]lossyConstraint{{name: "a"}, {name: "b"}}, []int{1, 2}))
	require.NoError(t, err)
	snapshot := inspector.Snapshot()
	require.Equal(t, RuleModeExact, snapshot.Mode())
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(1<<20))
}

func TestLossyAllRedistributesBudgetForMinimumViableChildren(t *testing.T) {
	stable := func(_ lossyConstraint) (string, bool) { return "stable", true }
	unique := func(v lossyConstraint) (string, bool) { return v.name, true }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i].name = fmt.Sprintf("customer-%d", i)
		ids[i] = i
	}

	var inspector Inspector
	index, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(
		All(Include(stable), Include(unique)),
		MemoryLimit(12000),
	))).Build(Zip(constraints, ids))
	require.NoError(t, err)

	var matches []int
	index.Search(lossyConstraint{name: "customer-17"}, &matches)
	require.Contains(t, matches, 17)
	usage, ok := inspector.Snapshot().MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(12000))
}
