//nolint:lll // Test schemas keep focused pointer getters inline.
package ruleix_test

import (
	"cmp"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

func TestLocalSearchCachesEqualityNodesWithoutChangingResults(t *testing.T) {
	type pair struct{ store, region *int }
	ix := buildZip(t, ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v pair) *int { return v.store })),
		ruleix.Include(ruleix.GetterFromPointer(func(v pair) *int { return v.region })),
	), []pair{
		{},
		{store: ptr(10)},
		{store: ptr(10), region: ptr(20)},
		{store: ptr(10), region: ptr(30)},
		{store: ptr(11), region: ptr(20)},
	}, []string{"global", "store", "region-20", "region-30", "other-store"})
	local := ix.Local()

	var got []string
	local.Search(pair{store: ptr(10), region: ptr(20)}, &got)
	require.Equal(t, []string{"global", "store", "region-20"}, got)
	got = got[:0]
	local.Search(pair{store: ptr(10), region: ptr(30)}, &got)
	require.Equal(t, []string{"global", "store", "region-30"}, got)
	got = got[:0]
	local.Search(pair{}, &got)
	require.Equal(t, []string{"global"}, got)
	got = got[:0]
	local.Search(pair{store: ptr(10), region: ptr(20)}, &got)
	require.Equal(t, []string{"global", "store", "region-20"}, got)
}

func TestLocalSearchPanicsWithNilDestination(t *testing.T) {
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })),
		[]benchmarkEquality{{optional: ptr(1)}}, []int{1})
	require.PanicsWithValue(t, "ruleix: nil search destination", func() {
		ix.Local().Search(benchmarkEquality{optional: ptr(1)}, nil)
	})
}

func TestLocalSearchCachesOrderedNodesWithoutChangingResults(t *testing.T) {
	ix := buildZip(t, ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), cmp.Compare[int]), []benchmarkRange{
		{},
		{value: ptr(5)},
		{value: ptr(10)},
		{value: ptr(15)},
	}, []string{"wildcard", "five", "ten", "fifteen"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkRange{value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "five", "ten"}, got)
	got = got[:0]
	local.Search(benchmarkRange{value: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "five", "ten", "fifteen"}, got)
	got = got[:0]
	local.Search(benchmarkRange{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	got = got[:0]
	local.Search(benchmarkRange{value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "five", "ten"}, got)
}

func TestLocalSearchCompareByMatchesIndexSearch(t *testing.T) {
	ix := buildZip(t, ruleix.CompareBy(ruleix.GetterFromPointer(func(v benchmarkRange) *int { return v.value }), ruleix.GetterFromPointer(func(v benchmarkRange) *ruleix.Operator { return v.operator }), cmp.Compare[int]), []benchmarkRange{
		{},
		{operator: ptr(ruleix.OperatorEQ), value: ptr(10)},
		{operator: ptr(ruleix.OperatorGTE), value: ptr(5)},
		{operator: ptr(ruleix.OperatorLTE), value: ptr(15)},
	}, []string{"wildcard", "ten", "five", "fifteen"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkRange{operator: ptr(ruleix.OperatorGTE), value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "ten", "five", "fifteen"}, got)
	got = got[:0]
	local.Search(benchmarkRange{operator: ptr(ruleix.OperatorLTE), value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "ten", "five", "fifteen"}, got)
	got = got[:0]
	local.Search(benchmarkRange{operator: ptr(ruleix.OperatorGTE)}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	got = got[:0]
	local.Search(benchmarkRange{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
}

func TestLocalSearchCachesBetweenNodesWithoutChangingResults(t *testing.T) {
	ix := buildZip(t, ruleix.Between(ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.from }), ruleix.GetterFromPointer(func(v benchmarkInterval) *int { return v.until }), cmp.Compare[int]), []benchmarkInterval{
		{},
		{from: ptr(0), until: ptr(20)},
		{from: ptr(5), until: ptr(15)},
		{from: ptr(10), until: ptr(30)},
	}, []string{"wildcard", "wide", "middle", "late"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkInterval{from: ptr(10), until: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "wide", "middle", "late"}, got)
	got = got[:0]
	local.Search(benchmarkInterval{from: ptr(15), until: ptr(25)}, &got)
	require.Equal(t, []string{"wildcard", "late"}, got)
	got = got[:0]
	local.Search(benchmarkInterval{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	got = got[:0]
	local.Search(benchmarkInterval{from: ptr(10), until: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "wide", "middle", "late"}, got)
}

func TestLocalSearchCachesExclusionsWithoutChangingResults(t *testing.T) {
	type platformConstraint struct{ platform *string }
	ix := buildZip(t, ruleix.Exclude(ruleix.GetterFromPointer(func(v platformConstraint) *string { return v.platform })),
		[]platformConstraint{
			{},
			{platform: ptr("android")},
			{platform: ptr("ios")},
			{platform: ptr("ios")},
		}, []string{"wildcard", "both", "both", "ios-only"})
	local := ix.Local()

	var got []string
	local.Search(platformConstraint{platform: ptr("android")}, &got)
	require.Equal(t, []string{"wildcard", "ios-only"}, got)
	got = got[:0]
	local.Search(platformConstraint{platform: ptr("ios")}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	got = got[:0]
	local.Search(platformConstraint{}, &got)
	require.Equal(t, []string{"wildcard", "both", "ios-only"}, got)
	got = got[:0]
	local.Search(platformConstraint{platform: ptr("android")}, &got)
	require.Equal(t, []string{"wildcard", "ios-only"}, got)
}

func TestSeparateLocalsSupportConcurrentSearch(t *testing.T) {
	ix := buildZip(t, ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.a })),
		ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v benchmarkAllValue) *int { return v.b }), cmp.Compare[int]),
	), []benchmarkAllValue{
		{a: ptr(1), b: ptr(1)},
		{a: ptr(1), b: ptr(2)},
		{a: ptr(2), b: ptr(1)},
	}, []int{1, 2, 3})

	var wg sync.WaitGroup
	results := make(chan []int, 20*50)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			local := ix.Local()
			var got []int
			for range 50 {
				got = got[:0]
				local.Search(benchmarkAllValue{a: ptr(1), b: ptr(2)}, &got)
				results <- append([]int(nil), got...)
			}
		}()
	}
	wg.Wait()
	close(results)
	for got := range results {
		require.Equal(t, []int{1, 2}, got)
	}
}

func TestLocalNestedAllMatchesIndexSearch(t *testing.T) {
	type mixed struct {
		country   *string
		minimum   *int
		excluded  *string
		operator  *ruleix.Operator
		threshold *int
	}
	schema := ruleix.All(
		ruleix.Include(ruleix.GetterFromPointer(func(v mixed) *string { return v.country })),
		ruleix.All(
			ruleix.GreaterOrEqual(ruleix.GetterFromPointer(func(v mixed) *int { return v.minimum }), cmp.Compare[int]),
			ruleix.Exclude(ruleix.GetterFromPointer(func(v mixed) *string { return v.excluded })),
		),
		ruleix.CompareBy(ruleix.GetterFromPointer(func(v mixed) *int { return v.threshold }), ruleix.GetterFromPointer(func(v mixed) *ruleix.Operator { return v.operator }), cmp.Compare[int]),
	)
	ix := buildZip(t, schema, []mixed{
		{},
		{
			country: ptr("DE"), minimum: ptr(10), excluded: ptr("marketplace"),
			operator: ptr(ruleix.OperatorGTE), threshold: ptr(5),
		},
		{
			country: ptr("DE"), minimum: ptr(20), excluded: ptr("retail"),
			operator: ptr(ruleix.OperatorLTE), threshold: ptr(30),
		},
		{country: ptr("FR"), minimum: ptr(10), operator: ptr(ruleix.OperatorEQ), threshold: ptr(15)},
	}, []string{"global", "de-minimum", "de-upper", "fr-exact"})
	queries := []mixed{
		{
			country: ptr("DE"), minimum: ptr(15), excluded: ptr("web"),
			operator: ptr(ruleix.OperatorGTE), threshold: ptr(10),
		},
		{
			country: ptr("DE"), minimum: ptr(25), excluded: ptr("retail"),
			operator: ptr(ruleix.OperatorLTE), threshold: ptr(20),
		},
		{
			country: ptr("FR"), minimum: ptr(15), excluded: ptr("web"),
			operator: ptr(ruleix.OperatorEQ), threshold: ptr(15),
		},
	}
	local := ix.Local()
	for range 3 {
		for _, query := range queries {
			var want, got []string
			ix.Search(query, &want)
			local.Search(query, &got)
			require.Equal(t, want, got)
		}
	}
}
