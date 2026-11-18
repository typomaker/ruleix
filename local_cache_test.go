//nolint:lll // Migration coverage keeps legacy pointer getters inline.
package ruleix

import (
	"cmp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValueBitmapCacheRetainsEveryEntry(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	a, b, c := 1, 2, 3
	pool := newBitmapPool()

	cache.replace(optionalValue[int]{value: a, ok: true}, pool).Add(1)
	cache.replace(optionalValue[int]{value: b, ok: true}, pool).Add(2)

	bits, found := comparableValueCacheLookup(cache, optionalValue[int]{value: a, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(1))

	cache.replace(optionalValue[int]{value: c, ok: true}, pool).Add(3)

	bits, found = comparableValueCacheLookup(cache, optionalValue[int]{value: b, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(2))
	bits, found = comparableValueCacheLookup(cache, optionalValue[int]{value: a, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(1))
	bits, found = comparableValueCacheLookup(cache, optionalValue[int]{value: c, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(3))
}

func TestValueBitmapCacheClearsValueForNilEntry(t *testing.T) {
	type value struct{ id int }
	cache := &valueBitmapCache[*value]{}
	first := &value{id: 1}
	second := &value{id: 2}

	pool := newBitmapPool()
	cache.replace(optionalValue[*value]{value: first, ok: true}, pool)
	cache.replace(optionalValue[*value]{value: second, ok: true}, pool)
	cache.replace(optionalValue[*value]{}, pool)

	entry := cache.entry(cache.entryCount() - 1)
	require.True(t, entry.initialized)
	require.False(t, entry.hasValue)
	require.Nil(t, entry.value)
}

func TestLocalCloseResetsNodeCachesAndReturnsInternalContext(t *testing.T) {
	type constraint struct{ value int }
	get := func(value constraint) (int, bool) { return value.value, true }
	index, err := New[constraint, int](Include(get)).Build(
		Zip([]constraint{{value: 1}}, []int{7}),
	)
	require.NoError(t, err)
	local := index.TestLocal()
	var matches []int
	local.SearchOne(constraint{value: 1}, &matches)
	cache := local.pool.local[0].equality
	require.NotNil(t, cache)
	pool := local.pool

	local.Close()
	require.Same(t, cache, pool.local[0].equality)
	require.PanicsWithValue(t, "ruleix: closed context", func() { local.SearchOne(constraint{}, &matches) })
	require.NotPanics(t, local.Close)

	reused := index.TestLocal()
	matches = matches[:0]
	reused.SearchOne(constraint{value: 1}, &matches)
	require.Equal(t, []int{7}, matches)
	if reused.pool == pool {
		require.Same(t, cache, reused.pool.local[0].equality)
	} else {
		// sync.Pool may discard or return a different context at any time,
		// especially under the race detector. A newly selected context must
		// still create a valid cold cache on first use.
		require.NotNil(t, reused.pool.local[0].equality)
	}
	reused.Close()
}

func TestResetLocalReturnsCachedBitmapsToScratchPool(t *testing.T) {
	pool := newLocalBitmapPool(2)
	valueCache := newValueBitmapCache[int](pool)
	valueBits := valueCache.replace(optionalValue[int]{value: 1, ok: true}, pool)
	valueBits.AddRange(0, 100)
	betweenCache := newBetweenCache[int](pool)
	betweenBits := pool.get()
	betweenBits.AddRange(100, 200)
	betweenCache.entries[0] = betweenCacheEntry[int]{initialized: true, bits: betweenBits}
	betweenCache.count = 1
	pool.local[0].ordered = valueCache
	pool.local[1].between = betweenCache
	pool.allPlans = map[any]*localAllPlan{"all": {order: []int{1, 0}}}

	pool.resetLocal()

	require.True(t, valueBits.IsEmpty())
	require.True(t, betweenBits.IsEmpty())
	require.Zero(t, valueCache.entryCount())
	require.Zero(t, betweenCache.entryCount())
	require.Same(t, valueCache, pool.local[0].ordered)
	require.Same(t, betweenCache, pool.local[1].between)
	require.Nil(t, pool.allPlans)
}

func TestEqualityExposesOnlyWarmLocalBitmap(t *testing.T) {
	type constraint struct{ value int }
	index, err := New[constraint, int](Include(func(value constraint) (int, bool) {
		return value.value, true
	})).Build(Zip(
		[]constraint{{value: 1}, {value: 2}, {value: 3}},
		[]int{1, 2, 3},
	))
	require.NoError(t, err)
	local := index.TestLocal()
	rule := index.root.(*eqRule[constraint, int, int])
	query := constraint{value: 2}

	_, found := rule.lookupCachedBitmap(query, local.pool)
	require.False(t, found)
	var matches []int
	for range 2 {
		matches = matches[:0]
		local.SearchOne(query, &matches)
	}
	bits, found := rule.lookupCachedBitmap(query, local.pool)
	require.True(t, found)
	require.Equal(t, uint64(1), bits.GetCardinality())
}

func TestLocalAllPlanRefreshSignals(t *testing.T) {
	require.False(t, localPlanCardinalityChanged(100, 60))
	require.True(t, localPlanCardinalityChanged(100, 49))
	require.True(t, localPlanCardinalityChanged(allCandidateScanLimit, allCandidateScanLimit+1))
	require.True(t, cachedChildMoreSelective(100, []rankedBitmap{{card: 50}, {card: ^uint64(0)}}))
	require.False(t, cachedChildMoreSelective(100, []rankedBitmap{{card: 51}}))
}

func TestLocalContextsCanBeAcquiredAndClosedConcurrently(t *testing.T) {
	type constraint struct{ value int }
	index, err := New[constraint, int](Include(func(v constraint) (int, bool) { return v.value, true })).Build(
		Zip([]constraint{{value: 1}}, []int{1}),
	)
	require.NoError(t, err)
	var workers sync.WaitGroup
	for range 16 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 100 {
				local := index.TestLocal()
				var matches []int
				local.SearchOne(constraint{value: 1}, &matches)
				if len(matches) != 1 || matches[0] != 1 {
					t.Errorf("matches = %v, want [1]", matches)
				}
				local.Close()
			}
		}()
	}
	workers.Wait()
}

func TestAllUsesWarmOrderedLocalBitmapsDuringPlanning(t *testing.T) {
	type constraint struct{ minimum, maximum int }
	schema := All(
		GreaterOrEqual(func(value constraint) (int, bool) { return value.minimum, true }, cmp.Compare[int]),
		LessOrEqual(func(value constraint) (int, bool) { return value.maximum, true }, cmp.Compare[int]),
	)
	constraints := make([]constraint, 16)
	ids := make([]int, len(constraints))
	for id := range constraints {
		constraints[id] = constraint{minimum: id, maximum: id + 16}
		ids[id] = id
	}
	index, err := New[constraint, int](schema).Build(Zip(constraints, ids))
	require.NoError(t, err)
	local := index.TestLocal()
	query := constraint{minimum: 12, maximum: 20}
	var matches []int
	for range 2 {
		matches = matches[:0]
		local.SearchOne(query, &matches)
	}

	root := index.root.(*allRule[constraint])
	plan := local.pool.allPlans[root]
	require.NotNil(t, plan)
	require.Len(t, plan.order, len(root.children))
	ranked := make([]rankedBitmap, len(root.children))
	require.True(t, root.rankChildren(query, local.pool, ranked))
	for _, child := range ranked {
		require.NotNil(t, child.bits)
		require.False(t, child.owned)
	}
	pool := local.pool
	local.Close()
	require.Nil(t, pool.allPlans)
}

var benchmarkLocalLifecycle *bitmapPool

func BenchmarkLocalLifecycleReuse(b *testing.B) {
	type constraint struct{ value int }
	index, err := New[constraint, int](Include(func(v constraint) (int, bool) { return v.value, true })).Build(
		Zip([]constraint{{value: 1}}, []int{1}),
	)
	if err != nil {
		b.Fatal(err)
	}

	b.Run("Fresh", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			pool := newLocalBitmapPool(index.nodes)
			pool.resetLocal()
			benchmarkLocalLifecycle = pool
		}
	})
	b.Run("Reused", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			local := index.TestLocal()
			benchmarkLocalLifecycle = local.pool
			local.Close()
		}
	})
}

func TestBetweenCacheRetainsEveryEntry(t *testing.T) {
	type interval struct{ from, until *int }
	pointer := func(value int) *int { return &value }
	schema := Between(GetterFromPointer(func(value interval) *int { return value.from }), GetterFromPointer(func(value interval) *int { return value.until }), cmp.Compare[int])
	entries := []interval{{from: pointer(0), until: pointer(100)}}
	sequence := Zip(entries, []int{1})
	index, err := New[interval, int](schema).Build(sequence)
	require.NoError(t, err)
	local := index.TestLocal()

	queries := []interval{
		{from: pointer(10), until: pointer(20)},
		{from: pointer(11), until: pointer(21)},
		{from: pointer(10), until: pointer(20)},
		{from: pointer(12), until: pointer(22)},
		{from: pointer(12), until: pointer(22)},
	}
	var matches []int
	for _, query := range queries {
		matches = matches[:0]
		local.SearchOne(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, int])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[int])
	cachedBounds := make(map[[2]int]bool, cache.entryCount())
	for i := 0; i < cache.entryCount(); i++ {
		entry := cache.entry(i)
		cachedBounds[[2]int{entry.from, entry.until}] = true
	}
	require.Equal(t, map[[2]int]bool{{10, 20}: true, {11, 21}: true, {12, 22}: true}, cachedBounds)
}

func TestBetweenCacheClearsMissingBounds(t *testing.T) {
	type bound struct{ id int }
	type interval struct{ from, until **bound }
	pointer := func(value *bound) **bound { return &value }
	compare := func(a, b *bound) int { return cmp.Compare(a.id, b.id) }
	schema := Between(GetterFromPointer(func(value interval) **bound { return value.from }), GetterFromPointer(func(value interval) **bound { return value.until }), compare)
	low, high := &bound{id: 0}, &bound{id: 100}
	index, err := New[interval, int](schema).Build(Zip(
		[]interval{{from: pointer(low), until: pointer(high)}},
		[]int{1},
	))
	require.NoError(t, err)
	local := index.TestLocal()

	for _, query := range []interval{
		{from: pointer(&bound{id: 10}), until: pointer(&bound{id: 20})},
		{from: pointer(&bound{id: 11}), until: pointer(&bound{id: 21})},
		{},
		{},
	} {
		var matches []int
		local.SearchOne(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, *bound])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[*bound])
	entry := cache.entry(cache.entryCount() - 1)
	require.True(t, entry.initialized)
	require.False(t, entry.hasFrom)
	require.False(t, entry.hasUntil)
	require.Nil(t, entry.from)
	require.Nil(t, entry.until)
}

func TestBetweenCacheRetainsUnboundedWorkingSet(t *testing.T) {
	type interval struct{ from, until *int }
	pointer := func(value int) *int { return &value }
	schema := Between(
		GetterFromPointer(func(value interval) *int { return value.from }),
		GetterFromPointer(func(value interval) *int { return value.until }),
		cmp.Compare[int],
	)
	index, err := New[interval, int](schema).Build(Zip(
		[]interval{{from: pointer(0), until: pointer(100)}},
		[]int{1},
	))
	require.NoError(t, err)
	local := index.TestLocal()
	queries := make([]interval, 128)
	for value := range queries {
		queries[value] = interval{from: pointer(value), until: pointer(value + 20)}
	}

	var matches []int
	for _, query := range queries {
		matches = matches[:0]
		local.SearchOne(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, int])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[int])
	require.Equal(t, len(queries), cache.entryCount())
	for _, query := range queries {
		found := false
		for i := 0; i < cache.entryCount(); i++ {
			entry := cache.entry(i)
			found = found || entry.initialized && entry.from == *query.from && entry.until == *query.until
		}
		require.True(t, found)
	}
}

func TestBetweenCacheOrdersOverflowByComparatorAndPresence(t *testing.T) {
	type interval struct{ from, until *time.Time }
	at := func(hour int) *time.Time {
		value := time.Date(2026, time.November, 17, hour, 0, 0, 0, time.UTC)
		return &value
	}
	schema := Between(
		GetterFromPointer(func(value interval) *time.Time { return value.from }),
		GetterFromPointer(func(value interval) *time.Time { return value.until }),
		time.Time.Compare,
	)
	index, err := New[interval, int](schema).Build(Zip(
		[]interval{{at(1), at(23)}, {nil, at(23)}, {at(1), nil}, {}},
		[]int{1, 2, 3, 4},
	))
	require.NoError(t, err)
	local := index.TestLocal()
	defer local.Close()

	queries := []interval{
		{at(7), at(8)}, {at(8), at(9)}, {at(5), at(9)}, {at(4), at(6)},
		{nil, at(6)}, {at(5), nil}, {at(5), at(7)}, {}, {at(5), at(8)},
	}
	assertMatches := func(query interval) {
		t.Helper()
		var expected, actual []int
		index.SearchOne(query, &expected)
		local.SearchOne(query, &actual)
		require.Equal(t, expected, actual)
	}
	for _, query := range queries {
		assertMatches(query)
	}
	rule := index.root.(*betweenRule[interval, time.Time])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[time.Time])
	require.Equal(t, len(queries), cache.entryCount())

	zone := time.FixedZone("UTC+03", 3*60*60)
	for i := len(queries) - 1; i >= 0; i-- {
		query := queries[i]
		if query.from != nil {
			from := query.from.In(zone)
			query.from = &from
		}
		if query.until != nil {
			until := query.until.In(zone)
			query.until = &until
		}
		assertMatches(query)
	}
	require.Equal(t, len(queries), cache.entryCount())
}

func TestValueBitmapCacheRetainsUnboundedWorkingSet(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	pool := newBitmapPool()
	values := make([]optionalValue[int], 128)
	for value := range values {
		values[value] = optionalValue[int]{value: value, ok: true}
	}

	for _, value := range values {
		cache.replace(value, pool)
	}

	require.Equal(t, len(values), cache.entryCount())
	for _, value := range values {
		_, found := comparableValueCacheLookup(cache, value)
		require.True(t, found)
	}
}

func TestLocalCachesEqualityBitmapAfterFirstUse(t *testing.T) {
	type constraint struct{ value int }
	index, err := New[constraint, int](Include(func(value constraint) (int, bool) {
		return value.value, true
	})).Build(Zip([]constraint{{value: 1}, {value: 2}}, []int{1, 2}))
	require.NoError(t, err)
	local := index.TestLocal()
	query := constraint{value: 1}

	var matches []int
	local.SearchOne(query, &matches)
	cache := local.pool.local[0].equality.(*valueBitmapCache[int])
	require.True(t, cache.entries[0].initialized)
	require.Equal(t, []int{1}, matches)
}

func TestLocalAllResultCacheRetainsUnboundedQueries(t *testing.T) {
	type constraint struct{ group, score int }
	constraints := make([]constraint, 60)
	ids := make([]int, len(constraints))
	for id := range constraints {
		constraints[id] = constraint{group: id / 10, score: id}
		ids[id] = id
	}
	index, err := New[constraint, int](All(
		Include(func(value constraint) (int, bool) { return value.group, true }),
		GreaterOrEqual(func(value constraint) (int, bool) { return value.score, true }, cmp.Compare[int]),
	)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	local := index.TestLocal()
	t.Cleanup(local.Close)

	for round := range 4 {
		for value := range 6 {
			for range 3 {
				query := constraint{group: value, score: value*10 + 5}
				var want, got []int
				index.SearchOne(query, &want)
				local.SearchOne(query, &got)
				require.Equal(t, want, got, "round %d, value %d", round, value)
			}
		}
	}
	plan := local.pool.allPlans[index.root]
	require.NotNil(t, plan)
	require.Len(t, plan.results, 5)
}
