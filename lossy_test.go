package ruleix

import (
	"cmp"
	"fmt"
	"math"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type lossyScalarConstraint[V any] struct {
	value   V
	present bool
}

func verifyLossySuperset[T any, ID comparable](
	t *testing.T,
	exactRule, lossyRule Rule[T],
	constraints []T,
	ids []ID,
	queries []T,
	requireLossy bool,
) {
	t.Helper()
	exact, err := New[T, ID](exactRule).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	approximate, err := New[T, ID](Inspect(&inspector, lossyRule)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	if requireLossy {
		require.Equal(t, RuleModeLossy, inspector.Snapshot().Mode(), "property case must exercise a lossy representation")
	}
	for _, query := range queries {
		var want, got []ID
		exact.Search(query, &want)
		approximate.Search(query, &got)
		requireSupersetComparable(t, want, got)
	}
}

func requireSupersetComparable[ID comparable](t *testing.T, exact, approximate []ID) {
	t.Helper()
	got := make(map[ID]bool, len(approximate))
	for _, id := range approximate {
		got[id] = true
	}
	for _, id := range exact {
		require.True(t, got[id], "lossy result dropped id %v", id)
	}
}

type lossyConstraint struct {
	name    string
	minimum int64
	present bool
}

func requireSuperset(t *testing.T, exact, approximate []int) {
	requireSupersetComparable(t, exact, approximate)
}

//nolint:lll // Boundary sets stay inline so their ordering is immediately visible.
func TestLossyEqualityScalarProperties(t *testing.T) {
	testLossyEqualityScalar(t, "int8", []int8{math.MinInt8, -1, 0, 1, math.MaxInt8})
	testLossyEqualityScalar(t, "uint64", []uint64{0, 1, 1 << 32, math.MaxUint64 - 1, math.MaxUint64})
	testLossyEqualityScalar(t, "float64", []float64{math.NaN(), math.Inf(-1), -math.MaxFloat64, math.Copysign(0, -1), 0, math.SmallestNonzeroFloat64, math.MaxFloat64, math.Inf(1)})
	testLossyEqualityScalar(t, "string", []string{"", "a", "customer-17", "\x00", "世界"})
}

func TestLossyEqualityMinimumGranularityReportsCompleteCollisionRate(t *testing.T) {
	get := func(value lossyConstraint) (string, bool) { return value.name, value.present }
	rule := Include(get).newState(&nodeIDAllocator{}, &buildStatistics{}).(*eqRule[lossyConstraint, string])
	for id, name := range []string{"alpha", "beta", "gamma"} {
		rule.insert(lossyConstraint{name: name, present: true}, uint32(id))
	}

	planner := rule.newLossyAllPlanner()
	exact, err := planner.compile(math.MaxUint64)
	require.NoError(t, err)
	_, compiled, err := minimumLossyAllLimit(planner, inspectionDetailsOf(exact).MemoryUsageBytes)
	require.NoError(t, err)

	details := inspectionDetailsOf(compiled)
	require.True(t, details.EstimatedFalsePositiveRateAvailable)
	require.Equal(t, 1.0, details.EstimatedFalsePositiveRateValue)
}

func testLossyEqualityScalar[V comparable](t *testing.T, name string, values []V) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		get := func(value lossyScalarConstraint[V]) (V, bool) { return value.value, value.present }
		constraints := make([]lossyScalarConstraint[V], 0, len(values)*64)
		ids := make([]int, 0, len(values)*64)
		for repetition := 0; repetition < 64; repetition++ {
			for _, value := range values {
				constraints = append(constraints, lossyScalarConstraint[V]{value: value, present: repetition%19 != 0})
				ids = append(ids, len(ids))
			}
		}
		queries := make([]lossyScalarConstraint[V], 0, len(values)+1)
		for _, value := range values {
			queries = append(queries, lossyScalarConstraint[V]{value: value, present: true})
		}
		queries = append(queries, lossyScalarConstraint[V]{})
		verifyLossySuperset(t, Include(get), Lossy(Include(get), MemoryLimit(2048)), constraints, ids, queries, false)
	})
}

//nolint:lll // Boundary sets stay inline so their ordering is immediately visible.
func TestLossyOrderedOperatorAndBoundaryProperties(t *testing.T) {
	testLossyOrderedScalar(t, "int64", []int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64})
	testLossyOrderedScalar(t, "uint64", []uint64{0, 1, 1 << 63, math.MaxUint64 - 1, math.MaxUint64})
	testLossyOrderedScalar(t, "float64", []float64{math.NaN(), math.Inf(-1), -math.MaxFloat64, -math.SmallestNonzeroFloat64, math.Copysign(0, -1), 0, math.SmallestNonzeroFloat64, math.MaxFloat64, math.Inf(1)})
}

//nolint:lll // Full exact and lossy constructors are kept adjacent for comparison.
func testLossyOrderedScalar[V cmp.Ordered](t *testing.T, name string, values []V) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		get := func(value lossyScalarConstraint[V]) (V, bool) { return value.value, value.present }
		constraints := make([]lossyScalarConstraint[V], 0, len(values)*80)
		ids := make([]int, 0, len(values)*80)
		for repetition := 0; repetition < 80; repetition++ {
			for _, value := range values {
				constraints = append(constraints, lossyScalarConstraint[V]{value: value, present: repetition%23 != 0})
				ids = append(ids, len(ids))
			}
		}
		queries := make([]lossyScalarConstraint[V], 0, len(values)+1)
		for _, value := range values {
			queries = append(queries, lossyScalarConstraint[V]{value: value, present: true})
		}
		queries = append(queries, lossyScalarConstraint[V]{})

		operators := []struct {
			name  string
			build func() Rule[lossyScalarConstraint[V]]
		}{
			{"greater", func() Rule[lossyScalarConstraint[V]] { return Greater(get, cmp.Compare[V]) }},
			{"greater_or_equal", func() Rule[lossyScalarConstraint[V]] { return GreaterOrEqual(get, cmp.Compare[V]) }},
			{"less", func() Rule[lossyScalarConstraint[V]] { return Less(get, cmp.Compare[V]) }},
			{"less_or_equal", func() Rule[lossyScalarConstraint[V]] { return LessOrEqual(get, cmp.Compare[V]) }},
		}
		for _, operator := range operators {
			t.Run(operator.name, func(t *testing.T) {
				verifyLossySuperset(t, operator.build(), Lossy(operator.build(), MemoryLimit(1536)), constraints, ids, queries, true)
			})
		}
	})
}

//nolint:lll // Full exact and lossy constructors are kept adjacent for comparison.
func TestLossyEqualityNeverDropsExactMatches(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{name: fmt.Sprintf("customer-%d", i), present: true}
		ids[i] = i
	}
	constraints[0].present = false
	exact, err := New[lossyConstraint, int](Include(get)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	approximate, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(Include(get), MemoryLimit(5000)))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for i := 0; i < len(constraints); i += 37 {
		var want, got []int
		exact.Search(constraints[i], &want)
		approximate.Search(constraints[i], &got)
		requireSuperset(t, want, got)
	}
	require.Equal(t, RuleModeLossy, inspector.Snapshot().Mode())
	require.Equal(t, "lossy-grouped-hash", inspector.Snapshot().Strategy())
}

//nolint:lll // Full exact and lossy constructors are kept adjacent for comparison.
func TestLossyOrderedNeverDropsExactMatches(t *testing.T) {
	get := func(v lossyConstraint) (int64, bool) { return v.minimum, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{minimum: int64(i - 1000), present: true}
		ids[i] = i
	}
	constraints[0].present = false
	exact, err := New[lossyConstraint, int](GreaterOrEqual(get, cmp.Compare[int64])).Build(Zip(constraints, ids))
	require.NoError(t, err)
	approximate, err := New[lossyConstraint, int](Lossy(GreaterOrEqual(get, cmp.Compare[int64]), MemoryLimit(5000))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for q := int64(-1100); q <= 1100; q += 53 {
		query := lossyConstraint{minimum: q, present: true}
		var want, got []int
		exact.Search(query, &want)
		approximate.Search(query, &got)
		requireSuperset(t, want, got)
	}
}

var lossyPlannerBenchmarkCardinality uint64

func TestLossyAllReusesPlanningBucket(t *testing.T) {
	query := lossyConstraint{name: "customer-7", present: true}
	hash, ok := hashScalar(query.name)
	require.True(t, ok)
	getterCalls := [2]int{}
	children := make([]Rule[lossyConstraint], 2)
	for i := range children {
		i := i
		children[i] = &lossyEqualityRule[lossyConstraint, string]{
			get: func(v lossyConstraint) (string, bool) {
				getterCalls[i]++
				return v.name, v.present
			},
			wildcard: roaring.New(),
			buckets:  map[uint64]lossyEqualityPosting{hash: {bits: roaring.BitmapOf(7)}},
		}
	}

	result := roaring.New()
	(&allRule[lossyConstraint]{children: children}).search(query, result, newBitmapPool())

	require.Equal(t, []uint32{7}, result.ToArray())
	require.Equal(t, [2]int{1, 1}, getterCalls)
}

func TestLossyAllLocalPlanReusesPlanningBucket(t *testing.T) {
	query := lossyConstraint{name: "customer-7", present: true}
	hash, ok := hashScalar(query.name)
	require.True(t, ok)
	getterCalls := [2]int{}
	children := make([]Rule[lossyConstraint], 2)
	for i := range children {
		i := i
		children[i] = &lossyEqualityRule[lossyConstraint, string]{
			get: func(v lossyConstraint) (string, bool) {
				getterCalls[i]++
				return v.name, v.present
			},
			wildcard: roaring.New(),
			buckets:  map[uint64]lossyEqualityPosting{hash: {bits: roaring.BitmapOf(7)}},
		}
	}

	root := &allRule[lossyConstraint]{children: children}
	pool := newLocalBitmapPool(0)
	for range 2 {
		result := roaring.New()
		root.search(query, result, pool)
		require.Equal(t, []uint32{7}, result.ToArray())
	}
	require.Equal(t, [2]int{2, 2}, getterCalls)
}

func TestLossyEqualityLocalCachesRepeatedValue(t *testing.T) {
	value := "customer-7"
	hash, ok := hashScalar(any(value))
	require.True(t, ok)
	rule := &lossyEqualityRule[lossyConstraint, string]{
		nodeID:   0,
		get:      func(v lossyConstraint) (string, bool) { return v.name, v.present },
		wildcard: roaring.New(),
		shift:    56,
		buckets:  map[uint64]lossyEqualityPosting{hash >> 56: {bits: roaring.BitmapOf(7)}},
	}
	pool := newLocalBitmapPool(1)
	query := lossyConstraint{name: value, present: true}

	for range 2 {
		result := pool.get()
		rule.search(query, result, pool)
		require.Equal(t, []uint32{7}, result.ToArray())
		pool.put(result)
	}

	cached, found := rule.lookupCachedBitmap(query, pool)
	require.True(t, found)
	require.Equal(t, []uint32{7}, cached.ToArray())
}

type unknownEstimateRule[T any] struct{ child Rule[T] }

func (*unknownEstimateRule[T]) rule() {}
func (r *unknownEstimateRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &unknownEstimateRule[T]{child: r.child.newState(ids, hints)}
}
func (r *unknownEstimateRule[T]) validate(v T) error { return r.child.validate(v) }
func (r *unknownEstimateRule[T]) insert(v T, id uint32) {
	r.child.insert(v, id)
}
func (r *unknownEstimateRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return r.child.cardinality(v, pool)
}
func (r *unknownEstimateRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(v, dst, pool)
}
func (r *unknownEstimateRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.exclude(v, dst, pool)
}
func (r *unknownEstimateRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	r.child.collectBuildStatistics(stats)
}

func BenchmarkLossyAllSelectivePlanning(b *testing.B) {
	// Apple M1 Max, Go 1.26.0: Adaptive 3678 ns/op, 3113 B/op, 69 allocs/op;
	// UnknownEstimate 5241 ns/op, 3914 B/op, 106 allocs/op (medians).
	// Reproduce: go test -run '^$' -bench '^BenchmarkLossyAllSelectivePlanning$'
	// -benchmem -benchtime=1s -count=5 .
	const entries = 100_000
	query := lossyConstraint{name: "customer-7", present: true}
	hash, ok := hashScalar(query.name)
	if !ok {
		b.Fatal("failed to hash benchmark query")
	}
	broad := roaring.New()
	broad.AddRange(0, entries)
	selective := &lossyEqualityRule[lossyConstraint, string]{
		get:      func(v lossyConstraint) (string, bool) { return v.name, v.present },
		wildcard: roaring.New(),
		buckets:  map[uint64]lossyEqualityPosting{hash: {bits: roaring.BitmapOf(7)}},
	}
	children := make([]Rule[lossyConstraint], 0, 8)
	for range 7 {
		children = append(children, &matchAllRule[lossyConstraint]{bits: broad})
	}
	children = append(children, selective)
	root := &allRule[lossyConstraint]{children: children}
	unknownChildren := append([]Rule[lossyConstraint](nil), children...)
	unknownChildren[len(unknownChildren)-1] = &unknownEstimateRule[lossyConstraint]{child: selective}
	unknownRoot := &allRule[lossyConstraint]{children: unknownChildren}

	b.Run("Adaptive", func(b *testing.B) {
		pool := newBitmapPool()
		b.ReportAllocs()
		for range b.N {
			result := pool.get()
			root.search(query, result, pool)
			lossyPlannerBenchmarkCardinality = result.GetCardinality()
			pool.put(result)
		}
	})
	b.Run("UnknownEstimate", func(b *testing.B) {
		pool := newBitmapPool()
		b.ReportAllocs()
		for range b.N {
			result := pool.get()
			unknownRoot.search(query, result, pool)
			lossyPlannerBenchmarkCardinality = result.GetCardinality()
			pool.put(result)
		}
	})
}

//nolint:lll // The compact query matrix is easier to audit inline.
func TestLossyOrderedEstimateAndIDMatchAgreeWithSearch(t *testing.T) {
	wildcard := roaring.BitmapOf(8)
	rule := &lossyOrderedRule[lossyConstraint, int64]{
		get:       func(v lossyConstraint) (int64, bool) { return v.minimum, v.present },
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
	_, err := New[lossyConstraint, int](Inspect(&exact, Lossy(Include(get), MemoryLimit(math.MaxUint64)))).Build(Zip(constraints, ids))
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
	exact, err := New[lossyConstraint, int](All(Include(name), GreaterOrEqual(minimum, cmp.Compare[int64]))).Build(Zip(constraints, ids))
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
