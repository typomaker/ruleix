//nolint:lll // Migration coverage keeps legacy pointer getters inline.
package ruleix

import (
	"cmp"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValueBitmapCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	a, b, c := 1, 2, 3

	pool := newBitmapPool()
	cache.replace(optionalValue[int]{value: a, ok: true}, pool).Add(1)
	cache.replace(optionalValue[int]{value: b, ok: true}, pool).Add(2)

	bits, found := comparableValueCacheLookup(cache, optionalValue[int]{value: a, ok: true})
	require.True(t, found)
	require.True(t, bits.Contains(1))

	cache.replace(optionalValue[int]{value: c, ok: true}, pool).Add(3)

	_, found = comparableValueCacheLookup(cache, optionalValue[int]{value: b, ok: true})
	require.False(t, found)
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

	require.True(t, cache.entries[0].initialized)
	require.False(t, cache.entries[0].hasValue)
	require.Nil(t, cache.entries[0].value)
}

func TestLocalCloseReleasesNodeCachesAndReturnsInternalContext(t *testing.T) {
	type constraint struct{ value int }
	get := func(value constraint) (int, bool) { return value.value, true }
	index, err := New[constraint, int](Include(get)).Build(
		Zip([]constraint{{value: 1}}, []int{7}),
	)
	require.NoError(t, err)
	local := index.Local()
	var matches []int
	local.Search(constraint{value: 1}, &matches)
	require.NotNil(t, local.pool.local[0].equality)
	pool := local.pool

	local.Close()
	require.Nil(t, pool.local[0].equality)
	require.PanicsWithValue(t, "ruleix: closed Local", func() { local.Search(constraint{}, &matches) })
	require.NotPanics(t, local.Close)

	reused := index.Local()
	require.Nil(t, reused.pool.local[0].equality)
	matches = matches[:0]
	reused.Search(constraint{value: 1}, &matches)
	require.Equal(t, []int{7}, matches)
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
	pool.local[0].ordered = valueCache
	pool.local[1].between = betweenCache
	pool.allPlans = map[any]*localAllPlan{"all": {order: []int{1, 0}}}

	pool.resetLocal()

	require.True(t, valueBits.IsEmpty())
	require.True(t, betweenBits.IsEmpty())
	require.Nil(t, valueCache.entries[0].bits)
	require.Nil(t, betweenCache.entries[0].bits)
	require.Nil(t, pool.local[0].ordered)
	require.Nil(t, pool.local[1].between)
	require.Equal(t, []int{1, 0}, pool.allPlans["all"].order)
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
	local := index.Local()
	rule := index.root.(*eqRule[constraint, int])
	query := constraint{value: 2}

	_, found := rule.lookupCachedBitmap(query, local.pool)
	require.False(t, found)
	var matches []int
	for range 2 {
		matches = matches[:0]
		local.Search(query, &matches)
	}
	bits, found := rule.lookupCachedBitmap(query, local.pool)
	require.True(t, found)
	require.Equal(t, uint64(1), bits.GetCardinality())
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
				local := index.Local()
				var matches []int
				local.Search(constraint{value: 1}, &matches)
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
	local := index.Local()
	query := constraint{minimum: 12, maximum: 20}
	var matches []int
	for range 2 {
		matches = matches[:0]
		local.Search(query, &matches)
	}

	root := index.root.(*allRule[constraint])
	plan := local.pool.allPlans[root]
	require.NotNil(t, plan)
	require.Len(t, plan.order, len(root.children))
	plan.uses = allLocalPlanRefreshInterval - 1
	ranked := make([]rankedBitmap, len(root.children))
	require.True(t, root.rankChildren(query, local.pool, ranked))
	require.Zero(t, plan.uses)
	for _, child := range ranked {
		require.NotNil(t, child.bits)
		require.False(t, child.owned)
	}
	pool := local.pool
	local.Close()
	require.Same(t, plan, pool.allPlans[root])
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
			local := index.Local()
			benchmarkLocalLifecycle = local.pool
			local.Close()
		}
	})
}

func TestBetweenCacheEvictsLeastRecentlyUsedEntry(t *testing.T) {
	type interval struct{ from, until *int }
	pointer := func(value int) *int { return &value }
	schema := Between(GetterFromPointer(func(value interval) *int { return value.from }), GetterFromPointer(func(value interval) *int { return value.until }), cmp.Compare[int])
	entries := []interval{{from: pointer(0), until: pointer(100)}}
	sequence := Zip(entries, []int{1})
	index, err := New[interval, int](schema).Build(sequence)
	require.NoError(t, err)
	local := index.Local()

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
		local.Search(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, int])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[int])
	cachedBounds := make(map[[2]int]bool, len(cache.entries))
	for _, entry := range cache.entries {
		cachedBounds[[2]int{entry.from, entry.until}] = true
	}
	require.Equal(t, map[[2]int]bool{{10, 20}: true, {12, 22}: true}, cachedBounds)
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
	local := index.Local()

	for _, query := range []interval{
		{from: pointer(&bound{id: 10}), until: pointer(&bound{id: 20})},
		{from: pointer(&bound{id: 11}), until: pointer(&bound{id: 21})},
		{},
		{},
	} {
		var matches []int
		local.Search(query, &matches)
	}

	rule := index.root.(*betweenRule[interval, *bound])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[*bound])
	require.True(t, cache.entries[0].initialized)
	require.False(t, cache.entries[0].hasFrom)
	require.False(t, cache.entries[0].hasUntil)
	require.Nil(t, cache.entries[0].from)
	require.Nil(t, cache.entries[0].until)
}

func TestBetweenCacheGrowsForRepeatedWorkingSet(t *testing.T) {
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
	local := index.Local()
	queries := [...]interval{
		{from: pointer(10), until: pointer(20)},
		{from: pointer(11), until: pointer(21)},
		{from: pointer(12), until: pointer(22)},
		{from: pointer(13), until: pointer(23)},
	}

	var matches []int
	for range 6 {
		for _, query := range queries {
			matches = matches[:0]
			local.Search(query, &matches)
			require.Equal(t, []int{1}, matches)
		}
	}

	rule := index.root.(*betweenRule[interval, int])
	cache := local.pool.local[rule.nodeID].between.(*betweenCache[int])
	require.Equal(t, 4, cache.capacity())
	for _, query := range queries {
		found := false
		for i := 0; i < cache.capacity(); i++ {
			entry := cache.entry(i)
			found = found || entry.initialized && entry.from == *query.from && entry.until == *query.until
		}
		require.True(t, found)
	}
}

func TestBetweenCacheDoesNotRetainChurnBitmaps(t *testing.T) {
	cache := &betweenCache[int]{}
	for value := 0; value < 100; value++ {
		from := optionalValue[int]{value: value, ok: true}
		until := optionalValue[int]{value: value + 10, ok: true}
		require.False(t, cache.admit(from, until, cmp.Compare[int]))
	}

	require.Nil(t, cache.overflow)
	for _, entry := range cache.entries {
		require.False(t, entry.initialized)
		require.Nil(t, entry.bits)
	}
}

func TestValueBitmapCacheAdmitsOnlyRepeatedValues(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	one := optionalValue[int]{value: 1, ok: true}
	two := optionalValue[int]{value: 2, ok: true}

	require.False(t, comparableValueCacheAdmit(cache, one))
	require.False(t, comparableValueCacheAdmit(cache, two))
	require.True(t, comparableValueCacheAdmit(cache, one))
	require.False(t, comparableValueCacheAdmit(cache, one))
}

func TestValueBitmapCacheGrowsForRepeatedWorkingSet(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	pool := newBitmapPool()
	values := [...]optionalValue[int]{
		{value: 1, ok: true}, {value: 2, ok: true},
		{value: 3, ok: true}, {value: 4, ok: true},
	}

	for range 6 {
		for _, value := range values {
			if _, found := comparableValueCacheLookup(cache, value); found {
				continue
			}
			if comparableValueCacheAdmit(cache, value) {
				cache.replace(value, pool)
			}
		}
	}

	require.NotNil(t, cache.overflow)
	for _, value := range values {
		_, found := comparableValueCacheLookup(cache, value)
		require.True(t, found)
	}
}

func TestValueBitmapCacheDoesNotRetainChurnBitmaps(t *testing.T) {
	cache := &valueBitmapCache[int]{}
	for value := 0; value < 100; value++ {
		admitted := comparableValueCacheAdmit(cache, optionalValue[int]{value: value, ok: true})
		require.False(t, admitted)
	}

	require.Nil(t, cache.overflow)
	for _, entry := range cache.entries {
		require.False(t, entry.initialized)
		require.Nil(t, entry.bits)
	}
}

func TestLocalAdmitsEqualityBitmapAfterSecondUse(t *testing.T) {
	type constraint struct{ value int }
	index, err := New[constraint, int](Include(func(value constraint) (int, bool) {
		return value.value, true
	})).Build(Zip([]constraint{{value: 1}, {value: 2}}, []int{1, 2}))
	require.NoError(t, err)
	local := index.Local()
	query := constraint{value: 1}

	var matches []int
	local.Search(query, &matches)
	cache := local.pool.local[0].equality.(*valueBitmapCache[int])
	require.NotNil(t, cache.seen)
	require.False(t, cache.entries[0].initialized)

	matches = matches[:0]
	local.Search(query, &matches)
	require.Nil(t, cache.seen)
	require.True(t, cache.entries[0].initialized)
	require.Equal(t, []int{1}, matches)
}
