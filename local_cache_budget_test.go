package ruleix

import (
	"cmp"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestLocalAllResultCacheMatchesExactQueryBeforeChildLookup(t *testing.T) {
	type constraint struct{ left, right int }
	var leftCalls, rightCalls int
	schema := All(
		Include(func(value constraint) (int, bool) {
			leftCalls++
			return value.left, true
		}),
		Include(func(value constraint) (int, bool) {
			rightCalls++
			return value.right, true
		}),
	)
	constraints := make([]constraint, 10_000)
	ids := make([]int, len(constraints))
	for id := range constraints {
		constraints[id] = constraint{left: 2, right: 2}
		if id < 5_000 {
			constraints[id].left = 1
		}
		if id >= 4_955 {
			constraints[id].right = 1
		}
		ids[id] = id
	}
	index, err := New[constraint, int](schema).Build(Zip(constraints, ids))
	require.NoError(t, err)
	local := index.Local()
	t.Cleanup(local.Close)
	query := constraint{left: 1, right: 1}
	var matches []int
	for range 3 {
		matches = matches[:0]
		local.Search(query, &matches)
	}
	require.Len(t, matches, 45)

	leftCalls, rightCalls = 0, 0
	matches = matches[:0]
	local.Search(query, &matches)

	require.Len(t, matches, 45)
	require.Equal(t, 1, leftCalls)
	require.Equal(t, 1, rightCalls)

	changed := constraint{left: 1, right: 2}
	var want []int
	matches = matches[:0]
	index.Search(changed, &want)
	local.Search(changed, &matches)
	require.Equal(t, want, matches)
}

func TestLocalQueryKeysUseOperationEquality(t *testing.T) {
	type query struct {
		equality int
		ordered  int
		from     int
		until    int
		value    int
		operator Operator
	}
	base := query{equality: 1, ordered: 2, from: 3, until: 4, value: 5, operator: OperatorGT}
	providers := []localQueryKeyProvider[query]{
		&eqRule[query, int, int]{get: func(value query) (int, bool) { return value.equality, true }},
		&orderedRule[query, int]{
			get: func(value query) (int, bool) { return value.ordered, true }, compare: cmp.Compare[int],
		},
		&betweenRule[query, int]{
			from:    &orderedRule[query, int]{get: func(value query) (int, bool) { return value.from, true }},
			until:   &orderedRule[query, int]{get: func(value query) (int, bool) { return value.until, true }},
			compare: cmp.Compare[int],
		},
		&compareByRule[query, int]{
			value:    func(value query) (int, bool) { return value.value, true },
			operator: func(value query) (Operator, bool) { return value.operator, true },
			compare:  cmp.Compare[int],
		},
	}
	for _, provider := range providers {
		key, retained := provider.localQueryKey(base)
		require.Positive(t, retained)
		require.True(t, provider.localQueryKeyMatches(base, key))
	}

	compareKey, _ := providers[3].localQueryKey(base)
	base.operator = OperatorLTE
	require.True(t, providers[3].localQueryKeyMatches(base, compareKey), "query-side CompareBy operator is ignored")
}

func TestLocalAllResultCacheHonorsSharedByteBudget(t *testing.T) {
	pool := newLocalBitmapPool(1)
	pool.observeRuntime = false
	rule := &allRule[int]{}
	plan := &localAllPlan{order: []int{0}}
	pool.allPlans = map[any]*localAllPlan{rule: plan}
	ranked := []rankedBitmap{{bits: roaring.BitmapOf(1), childIdx: 0}}

	large := roaring.New()
	for id := uint32(0); large.GetSizeInBytes() <= maxLocalAllResultBytes; id += 2 {
		large.Add(id)
	}
	rule.storeLocalResult(pool, ranked, large, 0)
	require.Zero(t, pool.allResultBytes)
	require.Nil(t, plan.results[0].bits)

	small := roaring.BitmapOf(1, 2, 3)
	rule.storeLocalResult(pool, ranked, small, 0)
	require.Positive(t, pool.allResultBytes)
	require.LessOrEqual(t, pool.allResultBytes, uint64(maxLocalAllResultBytes))
	require.True(t, plan.results[0].idsSet)
	require.Equal(t, []uint32{1, 2, 3}, plan.results[0].ids)
	plan.resetResults(pool)
	require.Zero(t, pool.allResultBytes)
}

func TestLocalAllResultCacheKeepsWideResultsOnBitmapPath(t *testing.T) {
	pool := newLocalBitmapPool(1)
	rule := &allRule[int]{}
	plan := &localAllPlan{order: []int{0}}
	pool.allPlans = map[any]*localAllPlan{rule: plan}
	ranked := []rankedBitmap{{bits: roaring.BitmapOf(1), childIdx: 0}}
	wide := roaring.New()
	wide.AddRange(0, maxLocalAllResultIDs+1)

	rule.storeLocalResult(pool, ranked, wide, 0)

	require.NotNil(t, plan.results[0].bits)
	require.False(t, plan.results[0].idsSet)
	require.Nil(t, plan.results[0].ids)
}

func TestLocalAllPlanHonorsSharedByteBudget(t *testing.T) {
	pool := newLocalBitmapPool(0)
	children := make([]Rule[int], maxLocalAllPlanBytes/8)
	ranked := make([]rankedBitmap, len(children))
	for i := range ranked {
		ranked[i] = rankedBitmap{childIdx: i, card: 1}
	}
	rule := &allRule[int]{children: children}
	rule.rememberLocalPlan(pool, ranked)
	require.Nil(t, pool.allPlans)
	require.Zero(t, pool.allPlanBytes)

	small := &allRule[int]{children: children[:2]}
	small.rememberLocalPlan(pool, ranked[:2])
	require.NotNil(t, pool.allPlans[small])
	require.Positive(t, pool.allPlanBytes)
	require.LessOrEqual(t, pool.allPlanBytes, uint64(maxLocalAllPlanBytes))
}

func TestLocalRejectsInvalidCachedPlan(t *testing.T) {
	pool := newLocalBitmapPool(0)
	rule := &allRule[int]{children: []Rule[int]{&countingRule{}, &countingRule{}}}
	rule.rememberLocalPlan(pool, []rankedBitmap{{childIdx: 0, card: 1}, {childIdx: 0, card: 1}})
	require.False(t, pool.allPlans[rule].valid)
	ranked := make([]rankedBitmap, 2)
	_, reused := rule.reuseLocalPlan(0, pool, ranked)
	require.False(t, reused)
}

func TestLocalResetDropsPlansAndTheirAccounting(t *testing.T) {
	pool := newLocalBitmapPool(0)
	rule := &allRule[int]{children: []Rule[int]{&countingRule{}}}
	rule.rememberLocalPlan(pool, []rankedBitmap{{childIdx: 0, card: 1}})
	require.NotEmpty(t, pool.allPlans)
	require.Positive(t, pool.allPlanBytes)

	pool.resetLocal()
	require.Nil(t, pool.allPlans)
	require.Zero(t, pool.allPlanBytes)
	require.Zero(t, pool.allResultBytes)
}

func TestLocalChildCacheHonorsSharedByteBudget(t *testing.T) {
	pool := newLocalBitmapPool(1)
	cache := newValueBitmapCache[int](pool)
	value := optionalValue[int]{value: 1, ok: true}
	bits := cache.replace(value, pool)
	for id := uint32(0); bits.GetSizeInBytes() <= maxLocalChildCacheBytes; id += 2 {
		bits.Add(id)
	}
	cache.commit(bits, pool)

	reused, found := comparableValueCacheLookup(cache, value)
	require.False(t, found)
	require.Nil(t, reused)
	require.Zero(t, pool.childCacheBytes)

	small := cache.replace(value, pool)
	small.AddMany([]uint32{1, 2, 3})
	cache.commit(small, pool)
	require.Positive(t, pool.childCacheBytes)
	cache.reset(pool)
	require.Zero(t, pool.childCacheBytes)
}
