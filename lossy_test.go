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
		require.Equal(
			t,
			RuleModeLossy,
			inspector.Snapshot().Mode(),
			"property case must exercise a lossy representation",
		)
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
	testLossyEqualityScalar(
		t,
		"float64",
		[]float64{
			math.NaN(),
			math.Inf(-1),
			-math.MaxFloat64,
			math.Copysign(0, -1),
			0,
			math.SmallestNonzeroFloat64,
			math.MaxFloat64,
			math.Inf(1),
		},
	)
	testLossyEqualityScalar(t, "string", []string{"", "a", "customer-17", "\x00", "世界"})
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
	testLossyOrderedScalar(
		t,
		"int64",
		[]int64{math.MinInt64, math.MinInt64 + 1, -1, 0, 1, math.MaxInt64 - 1, math.MaxInt64},
	)
	testLossyOrderedScalar(t, "uint64", []uint64{0, 1, 1 << 63, math.MaxUint64 - 1, math.MaxUint64})
	testLossyOrderedScalar(
		t,
		"float64",
		[]float64{
			math.NaN(),
			math.Inf(-1),
			-math.MaxFloat64,
			-math.SmallestNonzeroFloat64,
			math.Copysign(0, -1),
			0,
			math.SmallestNonzeroFloat64,
			math.MaxFloat64,
			math.Inf(1),
		},
	)
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
				verifyLossySuperset(
					t,
					operator.build(),
					Lossy(operator.build(), MemoryLimit(1536)),
					constraints,
					ids,
					queries,
					true,
				)
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
	approximate, err := New[lossyConstraint, int](
		Inspect(&inspector, Lossy(Include(get), MemoryLimit(5000))),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for i := 0; i < len(constraints); i += 37 {
		var want, got []int
		exact.Search(constraints[i], &want)
		approximate.Search(constraints[i], &got)
		requireSuperset(t, want, got)
	}
	require.Equal(t, RuleModeLossy, inspector.Snapshot().Mode())
	require.Equal(t, "equality", inspector.Snapshot().Strategy())
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
	approximate, err := New[lossyConstraint, int](
		Lossy(GreaterOrEqual(get, cmp.Compare[int64]), MemoryLimit(5000)),
	).Build(Zip(constraints, ids))
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
	codec, err := compileEqualityCodec[string]()
	require.NoError(t, err)
	hash := codec.hash(query.name)
	getterCalls := [2]int{}
	children := make([]Rule[lossyConstraint], 2)
	for i := range children {
		i := i
		children[i] = &quantizedEqualityRule[lossyConstraint, string]{
			get: func(v lossyConstraint) (string, bool) {
				getterCalls[i]++
				return v.name, v.present
			},
			wildcard:  roaring.New(),
			codec:     codec,
			quantizer: newEqualityQuantizer(65536),
			values:    testEqualityValues(map[uint64]*roaring.Bitmap{reduceEqualityHash(hash, 65536): roaring.BitmapOf(7)}),
		}
	}

	result := roaring.New()
	(&allRule[lossyConstraint]{children: children}).search(query, result, newBitmapPool())

	require.Equal(t, []uint32{7}, result.ToArray())
	require.Equal(t, [2]int{1, 1}, getterCalls)
}

func TestLossyAllLocalPlanReusesPlanningBucket(t *testing.T) {
	query := lossyConstraint{name: "customer-7", present: true}
	codec, err := compileEqualityCodec[string]()
	require.NoError(t, err)
	hash := codec.hash(query.name)
	getterCalls := [2]int{}
	children := make([]Rule[lossyConstraint], 2)
	for i := range children {
		i := i
		children[i] = &quantizedEqualityRule[lossyConstraint, string]{
			get: func(v lossyConstraint) (string, bool) {
				getterCalls[i]++
				return v.name, v.present
			},
			wildcard:  roaring.New(),
			codec:     codec,
			quantizer: newEqualityQuantizer(65536),
			values:    testEqualityValues(map[uint64]*roaring.Bitmap{reduceEqualityHash(hash, 65536): roaring.BitmapOf(7)}),
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
	codec, err := compileEqualityCodec[string]()
	require.NoError(t, err)
	hash := codec.hash(value)
	rule := &quantizedEqualityRule[lossyConstraint, string]{
		nodeID:    0,
		get:       func(v lossyConstraint) (string, bool) { return v.name, v.present },
		wildcard:  roaring.New(),
		codec:     codec,
		quantizer: newEqualityQuantizer(256),
		values:    testEqualityValues(map[uint64]*roaring.Bitmap{reduceEqualityHash(hash, 256): roaring.BitmapOf(7)}),
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

func TestLossyEqualityLocalQueryKeyIsCollisionSafe(t *testing.T) {
	rule := &quantizedEqualityRule[lossyConstraint, string]{
		get: func(v lossyConstraint) (string, bool) { return v.name, v.present },
	}
	var provider localQueryKeyProvider[lossyConstraint] = rule
	query := lossyConstraint{name: "customer-7", present: true}
	key, retained := provider.localQueryKey(query)

	require.Positive(t, retained)
	require.True(t, provider.localQueryKeyMatches(query, key))
	require.False(t, provider.localQueryKeyMatches(lossyConstraint{name: "customer-8", present: true}, key))
	require.False(t, provider.localQueryKeyMatches(lossyConstraint{}, key))
	require.False(t, provider.localQueryKeyMatches(query, "customer-7"))
}

func TestLossyCompareByLocalQueryKeyUsesPreparedComparator(t *testing.T) {
	rule := &quantizedCompareByRule[lossyScalarConstraint[int], int]{&compareByRule[lossyScalarConstraint[int], int]{
		value:   func(v lossyScalarConstraint[int]) (int, bool) { return v.value, v.present },
		compare: cmp.Compare[int],
	}}
	var provider localQueryKeyProvider[lossyScalarConstraint[int]] = rule
	query := lossyScalarConstraint[int]{value: 7, present: true}
	key, retained := provider.localQueryKey(query)

	require.Positive(t, retained)
	require.True(t, provider.localQueryKeyMatches(query, key))
	require.False(t, provider.localQueryKeyMatches(lossyScalarConstraint[int]{value: 8, present: true}, key))
	require.False(t, provider.localQueryKeyMatches(lossyScalarConstraint[int]{}, key))
	require.False(t, provider.localQueryKeyMatches(query, "7"))
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
	codec, err := compileEqualityCodec[string]()
	if err != nil {
		b.Fatal(err)
	}
	hash := codec.hash(query.name)
	broad := roaring.New()
	broad.AddRange(0, entries)
	selective := &quantizedEqualityRule[lossyConstraint, string]{
		get:       func(v lossyConstraint) (string, bool) { return v.name, v.present },
		wildcard:  roaring.New(),
		codec:     codec,
		quantizer: newEqualityQuantizer(65536),
		values:    testEqualityValues(map[uint64]*roaring.Bitmap{reduceEqualityHash(hash, 65536): roaring.BitmapOf(7)}),
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
