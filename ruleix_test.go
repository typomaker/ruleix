package ruleix_test

import (
	"cmp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

type TimeRange struct{ Since, Until time.Time }
type CustomerOrderCount struct {
	Operator *ruleix.Operator
	Total    int
}
type Platform struct {
	Name    string
	Version *Version
}
type Version struct {
	Operator            *ruleix.Operator
	Major, Minor, Patch int
}
type SemanticVersion struct{ Major, Minor, Patch int }
type CustomerUUID string
type StoreUUID string
type DBS bool
type MarketType string
type ABTest struct{ Label, Group string }

type pathA struct{ B *pathB }
type pathB struct{ C *pathC }
type pathC struct{ D *pathD }
type pathD struct{ E *pathE }
type pathE struct{ Value *int }

type Constraint struct {
	Activity           *TimeRange
	CustomerOrderCount *CustomerOrderCount
	CustomerUUID       *CustomerUUID
	StoreUUID          *StoreUUID
	Platform           *Platform
	DBS                *DBS
	MarketType         *MarketType
	ABTest             *ABTest
}

type ModifierUUID string

func ptr[T any](v T) *T { return &v }
func search[C any, ID comparable](ix *ruleix.Index[C, ID], value C) []ID {
	var dst []ID
	ix.Search(value, &dst)
	return dst
}
func buildZip[C any, ID comparable](t testing.TB, schema ruleix.Rule[C], constraints []C, ids []ID) *ruleix.Index[C, ID] {
	t.Helper()
	entries := ruleix.Zip(constraints, ids)
	ix, err := ruleix.New[C, ID](schema).Build(entries)
	require.NoError(t, err)
	return ix
}
func compareTime(a, b time.Time) int { return a.Compare(b) }
func compareVersion(a, b SemanticVersion) int {
	if n := cmp.Compare(a.Major, b.Major); n != 0 {
		return n
	}
	if n := cmp.Compare(a.Minor, b.Minor); n != 0 {
		return n
	}
	return cmp.Compare(a.Patch, b.Patch)
}

func schema() ruleix.Rule[Constraint] {
	return ruleix.All(
		ruleix.Between(
			ruleix.Path(
				func(c Constraint) *TimeRange { return c.Activity },
				func(r TimeRange) *time.Time { return &r.Since },
			),
			ruleix.Path(
				func(c Constraint) *TimeRange { return c.Activity },
				func(r TimeRange) *time.Time { return &r.Until },
			),
			compareTime,
		),
		ruleix.CompareBy(
			ruleix.Path(
				func(c Constraint) *CustomerOrderCount { return c.CustomerOrderCount },
				func(c CustomerOrderCount) *int { return &c.Total },
			),
			ruleix.Path(
				func(c Constraint) *CustomerOrderCount { return c.CustomerOrderCount },
				func(c CustomerOrderCount) *ruleix.Operator { return c.Operator },
			),
			cmp.Compare[int],
		),
		ruleix.Include(func(c Constraint) *CustomerUUID { return c.CustomerUUID }),
		ruleix.Include(func(c Constraint) *StoreUUID { return c.StoreUUID }),
		ruleix.Include(ruleix.Path(
			func(c Constraint) *Platform { return c.Platform },
			func(p Platform) *string { return &p.Name },
		)),
		ruleix.CompareBy(
			ruleix.Path3(
				func(c Constraint) *Platform { return c.Platform },
				func(p Platform) *Version { return p.Version },
				func(v Version) *SemanticVersion {
					return &SemanticVersion{v.Major, v.Minor, v.Patch}
				},
			),
			ruleix.Path3(
				func(c Constraint) *Platform { return c.Platform },
				func(p Platform) *Version { return p.Version },
				func(v Version) *ruleix.Operator { return v.Operator },
			),
			compareVersion,
		),
		ruleix.Include(func(c Constraint) *DBS { return c.DBS }),
		ruleix.Include(func(c Constraint) *MarketType { return c.MarketType }),
		ruleix.Include(ruleix.Path(
			func(c Constraint) *ABTest { return c.ABTest },
			func(a ABTest) *string { return &a.Label },
		)),
		ruleix.Include(ruleix.Path(
			func(c Constraint) *ABTest { return c.ABTest },
			func(a ABTest) *string { return &a.Group },
		)),
	)
}

func TestCompleteConstraint(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t1, t2, t3 := t0.Add(time.Hour), t0.Add(2*time.Hour), t0.Add(3*time.Hour)
	rules := []struct {
		constraint Constraint
		id         ModifierUUID
	}{
		{Constraint{
			Activity:           &TimeRange{t0, t3},
			CustomerOrderCount: &CustomerOrderCount{Operator: ptr(ruleix.OperatorGTE), Total: 10},
			StoreUUID:          ptr(StoreUUID("store-1")),
			Platform:           &Platform{Name: "ios", Version: &Version{Operator: ptr(ruleix.OperatorGTE), Major: 2}},
			ABTest:             &ABTest{Label: "checkout", Group: "b"},
		}, "specific"},
		{Constraint{}, "wildcard"},
		{Constraint{Platform: &Platform{Name: "android"}}, "wrong-platform"},
		{Constraint{Activity: &TimeRange{t2, t3}}, "wrong-range"},
	}
	constraints := make([]Constraint, len(rules))
	ids := make([]ModifierUUID, len(rules))
	for i, row := range rules {
		constraints[i], ids[i] = row.constraint, row.id
	}
	ix := buildZip(t, schema(), constraints, ids)

	got := search(ix, Constraint{
		Activity:           &TimeRange{t1, t2},
		CustomerOrderCount: &CustomerOrderCount{Operator: ptr(ruleix.OperatorGTE), Total: 12},
		StoreUUID:          ptr(StoreUUID("store-1")),
		Platform:           &Platform{Name: "ios", Version: &Version{Operator: ptr(ruleix.OperatorGTE), Major: 2, Minor: 1}},
		ABTest:             &ABTest{Label: "checkout", Group: "b"},
	})
	require.Equal(t, []ModifierUUID{"specific", "wildcard"}, got)
}

func TestCompareByAllOperators(t *testing.T) {
	comparisonSchema := ruleix.CompareBy(
		func(c CustomerOrderCount) *int { return &c.Total },
		func(c CustomerOrderCount) *ruleix.Operator { return c.Operator },
		cmp.Compare[int],
	)
	ix := buildZip(t, comparisonSchema, []CustomerOrderCount{
		{Operator: ptr(ruleix.OperatorGTE), Total: 10},
		{Operator: ptr(ruleix.OperatorLTE), Total: 20},
		{Operator: ptr(ruleix.OperatorEQ), Total: 15},
		{Operator: ptr(ruleix.OperatorLT), Total: 15},
		{Operator: ptr(ruleix.OperatorGT), Total: 15},
	}, []string{"gte", "lte", "eq", "lt", "gt"})

	require.Equal(t, []string{"gte", "lte", "eq"}, search(ix, CustomerOrderCount{Total: 15}))
	require.Equal(t, []string{"gte", "lte", "eq"}, search(ix, CustomerOrderCount{Operator: ptr(ruleix.OperatorGT), Total: 15}), "query operator must be ignored")
}

func TestCompareByRejectsInvalidInsertedOperator(t *testing.T) {
	invalid := ruleix.Operator(255)
	_, err := ruleix.New[CustomerOrderCount, string](ruleix.CompareBy(
		func(c CustomerOrderCount) *int { return &c.Total },
		func(c CustomerOrderCount) *ruleix.Operator { return c.Operator },
		cmp.Compare[int],
	)).Build(ruleix.Zip([]CustomerOrderCount{{Operator: &invalid, Total: 5}}, []string{"invalid"}))
	require.EqualError(t, err, "ruleix: entry 0: ruleix: unsupported operator 255")
}

func TestBetweenAndPathWildcard(t *testing.T) {
	t0 := time.Unix(0, 0)
	t1, t2, t3 := t0.Add(time.Hour), t0.Add(2*time.Hour), t0.Add(3*time.Hour)
	intervalSchema := ruleix.Between(
		ruleix.Path(
			func(c Constraint) *TimeRange { return c.Activity },
			func(r TimeRange) *time.Time { return &r.Since },
		),
		ruleix.Path(
			func(c Constraint) *TimeRange { return c.Activity },
			func(r TimeRange) *time.Time { return &r.Until },
		),
		compareTime,
	)
	ix := buildZip(t, intervalSchema,
		[]Constraint{{Activity: &TimeRange{t0, t3}}, {}, {Activity: &TimeRange{t2, t3}}},
		[]string{"covering", "wildcard", "late"})
	require.Equal(t, []string{"covering", "wildcard"}, search(ix, Constraint{Activity: &TimeRange{t1, t2}}))
}

func TestPath3(t *testing.T) {
	schema := ruleix.Include(ruleix.Path3(
		func(c Constraint) *Platform { return c.Platform },
		func(p Platform) *Version { return p.Version },
		func(v Version) *int { return &v.Major },
	))
	ix := buildZip(t, schema,
		[]Constraint{{}, {Platform: &Platform{}}, {Platform: &Platform{Version: &Version{Major: 2}}}},
		[]string{"missing-platform", "missing-version", "version-2"})

	require.Equal(t,
		[]string{"missing-platform", "missing-version", "version-2"},
		search(ix, Constraint{Platform: &Platform{Version: &Version{Major: 2}}}),
	)
}

func TestPath4AndPath5(t *testing.T) {
	ab := func(a pathA) *pathB { return a.B }
	bc := func(b pathB) *pathC { return b.C }
	cd := func(c pathC) *pathD { return c.D }
	de := func(d pathD) *pathE { return d.E }
	ef := func(e pathE) *int { return e.Value }
	value := 42
	complete := pathA{B: &pathB{C: &pathC{D: &pathD{E: &pathE{Value: &value}}}}}

	require.Same(t, complete.B.C.D.E, ruleix.Path4(ab, bc, cd, de)(complete))
	require.Equal(t, &value, ruleix.Path5(ab, bc, cd, de, ef)(complete))
	require.Nil(t, ruleix.Path4(ab, bc, cd, de)(pathA{}))
	require.Nil(t, ruleix.Path5(ab, bc, cd, de, ef)(pathA{}))
}

func TestConcurrentSearchAndBitmapReuse(t *testing.T) {
	comparisonSchema := ruleix.CompareBy(
		func(c CustomerOrderCount) *int { return &c.Total },
		func(c CustomerOrderCount) *ruleix.Operator { return c.Operator },
		cmp.Compare[int],
	)
	constraints := make([]CustomerOrderCount, 100)
	ids := make([]int, 100)
	for i := 0; i < 100; i++ {
		constraints[i], ids[i] = CustomerOrderCount{Operator: ptr(ruleix.OperatorGTE), Total: i}, i
	}
	ix := buildZip(t, comparisonSchema, constraints, ids)
	var wg sync.WaitGroup
	lengths := make(chan int, 1000)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var dst []int
			for j := 0; j < 50; j++ {
				ix.Search(CustomerOrderCount{Operator: ptr(ruleix.OperatorGTE), Total: 49}, &dst)
				lengths <- len(dst)
			}
		}()
	}
	wg.Wait()
	close(lengths)
	for length := range lengths {
		require.Equal(t, 50, length)
	}
}

func TestSearchDeduplicatesAndReusesDestination(t *testing.T) {
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return &v.required }),
		[]benchmarkEquality{{required: 1}, {required: 1}, {required: 1}},
		[]string{"same", "other", "same"})

	dst := make([]string, 3, 8)
	dst[0] = "stale"
	before := &dst[0]
	ix.Search(benchmarkEquality{required: 1}, &dst)
	require.Equal(t, []string{"same", "other"}, dst)
	require.Same(t, before, &dst[0])
}

func TestEqNilStoredValueMatchesConcreteSearchValue(t *testing.T) {
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return v.optional }),
		[]benchmarkEquality{{}, {optional: ptr(7)}, {optional: ptr(8)}},
		[]string{"wildcard", "exact", "different"})

	require.Equal(t, []string{"wildcard", "exact"}, search(ix, benchmarkEquality{optional: ptr(7)}))
}

func TestSearchDeduplicatesAcrossMatchingBranches(t *testing.T) {
	comparisonSchema := ruleix.CompareBy(
		func(c CustomerOrderCount) *int { return &c.Total },
		func(c CustomerOrderCount) *ruleix.Operator { return c.Operator },
		cmp.Compare[int],
	)
	ix := buildZip(t, comparisonSchema,
		[]CustomerOrderCount{{Operator: ptr(ruleix.OperatorGTE), Total: 10}, {Operator: ptr(ruleix.OperatorGTE), Total: 5}, {Operator: ptr(ruleix.OperatorGTE), Total: 10}},
		[]string{"duplicate", "duplicate", "last"})
	require.Equal(t, []string{"duplicate", "last"}, search(ix, CustomerOrderCount{Operator: ptr(ruleix.OperatorGTE), Total: 10}))
}

func TestSearchDeduplicatesNonConsecutiveIDsInPostingList(t *testing.T) {
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return &v.required }),
		[]benchmarkEquality{{required: 1}, {required: 1}, {required: 1}, {required: 1}},
		[]string{"first", "second", "third", "first"})

	require.Equal(t, []string{"first", "second", "third"}, search(ix, benchmarkEquality{required: 1}))
}

func TestNotExcludesIDWhenAnyConstraintMatches(t *testing.T) {
	type PlatformConstraint struct{ PlatformName string }
	ix := buildZip(t, ruleix.Exclude(func(c PlatformConstraint) *string { return &c.PlatformName }),
		[]PlatformConstraint{{PlatformName: "android"}, {PlatformName: "ios"}, {PlatformName: "ios"}},
		[]int{1, 1, 2})

	require.Equal(t, []int{2}, search(ix, PlatformConstraint{PlatformName: "android"}))
	require.Empty(t, search(ix, PlatformConstraint{PlatformName: "ios"}))
	require.Equal(t, []int{1, 2}, search(ix, PlatformConstraint{PlatformName: "windows"}))
}

func TestVisitHonorsNotExclusionAcrossConstraints(t *testing.T) {
	type PlatformConstraint struct{ PlatformName string }
	ix := buildZip(t, ruleix.Exclude(func(c PlatformConstraint) *string { return &c.PlatformName }),
		[]PlatformConstraint{{PlatformName: "android"}, {PlatformName: "ios"}}, []int{1, 1})

	var got []int
	ix.Visit(PlatformConstraint{PlatformName: "android"}, func(id int) bool {
		got = append(got, id)
		return true
	})
	require.Empty(t, got)
}

func TestSearchUpdatesDestinationWhenItGrows(t *testing.T) {
	constraints := make([]benchmarkEquality, 10)
	ids := make([]int, 10)
	for id := 0; id < 10; id++ {
		constraints[id], ids[id] = benchmarkEquality{required: 1}, id
	}
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return &v.required }), constraints, ids)
	dst := make([]int, 0, 1)
	ix.Search(benchmarkEquality{required: 1}, &dst)
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, dst)
}

func TestLocalSearchCachesEqualityNodesWithoutChangingResults(t *testing.T) {
	type pair struct{ store, region *int }
	ix := buildZip(t, ruleix.All(
		ruleix.Include(func(v pair) *int { return v.store }),
		ruleix.Include(func(v pair) *int { return v.region }),
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
	local.Search(pair{store: ptr(10), region: ptr(30)}, &got)
	require.Equal(t, []string{"global", "store", "region-30"}, got)
	local.Search(pair{}, &got)
	require.Equal(t, []string{"global"}, got)
	local.Search(pair{store: ptr(10), region: ptr(20)}, &got)
	require.Equal(t, []string{"global", "store", "region-20"}, got)
}

func TestLocalSearchPanicsWithNilDestination(t *testing.T) {
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return v.optional }),
		[]benchmarkEquality{{optional: ptr(1)}}, []int{1})
	require.PanicsWithValue(t, "ruleix: nil search destination", func() {
		ix.Local().Search(benchmarkEquality{optional: ptr(1)}, nil)
	})
}

func TestLocalSearchCachesOrderedNodesWithoutChangingResults(t *testing.T) {
	ix := buildZip(t, ruleix.GreaterOrEqual(
		func(v benchmarkRange) *int { return v.value }, cmp.Compare[int],
	), []benchmarkRange{
		{},
		{value: ptr(5)},
		{value: ptr(10)},
		{value: ptr(15)},
	}, []string{"wildcard", "five", "ten", "fifteen"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkRange{value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "five", "ten"}, got)
	local.Search(benchmarkRange{value: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "five", "ten", "fifteen"}, got)
	local.Search(benchmarkRange{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	local.Search(benchmarkRange{value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "five", "ten"}, got)
}

func TestLocalSearchCompareByMatchesIndexSearch(t *testing.T) {
	ix := buildZip(t, ruleix.CompareBy(
		func(v benchmarkRange) *int { return v.value },
		func(v benchmarkRange) *ruleix.Operator { return v.operator },
		cmp.Compare[int],
	), []benchmarkRange{
		{},
		{operator: ptr(ruleix.OperatorEQ), value: ptr(10)},
		{operator: ptr(ruleix.OperatorGTE), value: ptr(5)},
		{operator: ptr(ruleix.OperatorLTE), value: ptr(15)},
	}, []string{"wildcard", "ten", "five", "fifteen"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkRange{operator: ptr(ruleix.OperatorGTE), value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "ten", "five", "fifteen"}, got)
	local.Search(benchmarkRange{operator: ptr(ruleix.OperatorLTE), value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "ten", "five", "fifteen"}, got)
	local.Search(benchmarkRange{operator: ptr(ruleix.OperatorGTE)}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	local.Search(benchmarkRange{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
}

func TestLocalSearchCachesBetweenNodesWithoutChangingResults(t *testing.T) {
	ix := buildZip(t, ruleix.Between(
		func(v benchmarkInterval) *int { return v.from },
		func(v benchmarkInterval) *int { return v.until },
		cmp.Compare[int],
	), []benchmarkInterval{
		{},
		{from: ptr(0), until: ptr(20)},
		{from: ptr(5), until: ptr(15)},
		{from: ptr(10), until: ptr(30)},
	}, []string{"wildcard", "wide", "middle", "late"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkInterval{from: ptr(10), until: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "wide", "middle", "late"}, got)
	local.Search(benchmarkInterval{from: ptr(15), until: ptr(25)}, &got)
	require.Equal(t, []string{"wildcard", "late"}, got)
	local.Search(benchmarkInterval{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	local.Search(benchmarkInterval{from: ptr(10), until: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "wide", "middle", "late"}, got)
}

func TestLocalSearchCachesExclusionsWithoutChangingResults(t *testing.T) {
	type platformConstraint struct{ platform *string }
	ix := buildZip(t, ruleix.Exclude(func(v platformConstraint) *string { return v.platform }),
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
	local.Search(platformConstraint{platform: ptr("ios")}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	local.Search(platformConstraint{}, &got)
	require.Equal(t, []string{"wildcard", "both", "ios-only"}, got)
	local.Search(platformConstraint{platform: ptr("android")}, &got)
	require.Equal(t, []string{"wildcard", "ios-only"}, got)
}

func TestLocalVisitUsesCacheAndStopsEarly(t *testing.T) {
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return &v.required }),
		[]benchmarkEquality{{required: 1}, {required: 1}, {required: 1}},
		[]string{"first", "second", "third"})
	local := ix.Local()

	for range 2 {
		var got []string
		local.Visit(benchmarkEquality{required: 1}, func(id string) bool {
			got = append(got, id)
			return len(got) < 2
		})
		require.Equal(t, []string{"first", "second"}, got)
	}
	local.Visit(benchmarkEquality{required: 1}, nil)
}

func TestSeparateLocalsSupportConcurrentSearch(t *testing.T) {
	ix := buildZip(t, ruleix.All(
		ruleix.Include(func(v benchmarkAllValue) *int { return v.a }),
		ruleix.GreaterOrEqual(func(v benchmarkAllValue) *int { return v.b }, cmp.Compare[int]),
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
		ruleix.Include(func(v mixed) *string { return v.country }),
		ruleix.All(
			ruleix.GreaterOrEqual(func(v mixed) *int { return v.minimum }, cmp.Compare[int]),
			ruleix.Exclude(func(v mixed) *string { return v.excluded }),
		),
		ruleix.CompareBy(
			func(v mixed) *int { return v.threshold },
			func(v mixed) *ruleix.Operator { return v.operator },
			cmp.Compare[int],
		),
	)
	ix := buildZip(t, schema, []mixed{
		{},
		{country: ptr("DE"), minimum: ptr(10), excluded: ptr("marketplace"), operator: ptr(ruleix.OperatorGTE), threshold: ptr(5)},
		{country: ptr("DE"), minimum: ptr(20), excluded: ptr("retail"), operator: ptr(ruleix.OperatorLTE), threshold: ptr(30)},
		{country: ptr("FR"), minimum: ptr(10), operator: ptr(ruleix.OperatorEQ), threshold: ptr(15)},
	}, []string{"global", "de-minimum", "de-upper", "fr-exact"})
	queries := []mixed{
		{country: ptr("DE"), minimum: ptr(15), excluded: ptr("web"), operator: ptr(ruleix.OperatorGTE), threshold: ptr(10)},
		{country: ptr("DE"), minimum: ptr(25), excluded: ptr("retail"), operator: ptr(ruleix.OperatorLTE), threshold: ptr(20)},
		{country: ptr("FR"), minimum: ptr(15), excluded: ptr("web"), operator: ptr(ruleix.OperatorEQ), threshold: ptr(15)},
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

func TestVisitDeduplicatesAndStopsEarly(t *testing.T) {
	ix := buildZip(t, ruleix.Include(func(v benchmarkEquality) *int { return &v.required }),
		[]benchmarkEquality{{required: 1}, {required: 1}, {required: 1}, {required: 1}},
		[]string{"first", "first", "second", "third"})
	var got []string
	ix.Visit(benchmarkEquality{required: 1}, func(id string) bool {
		got = append(got, id)
		return len(got) < 2
	})
	require.Equal(t, []string{"first", "second"}, got)
}
