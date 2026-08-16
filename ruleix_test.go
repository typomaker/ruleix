package ruleix_test

import (
	"cmp"
	"sync"
	"testing"
	"time"

	"github.com/albertsultanov/ruleix"
	"github.com/stretchr/testify/require"
)

type TimeRange struct{ Since, Until time.Time }
type CustomerOrderCount struct {
	Operator string
	Total    int
}
type Platform struct {
	Name    string
	Version *Version
}
type Version struct {
	Operator            string
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
	entries, err := ruleix.Zip(constraints, ids)
	require.NoError(t, err)
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
		ruleix.BetweenConstraint(
			func(c Constraint) *TimeRange { return c.Activity },
			func(r TimeRange) *time.Time { return &r.Since },
			func(r TimeRange) *time.Time { return &r.Until },
			compareTime,
		),
		ruleix.CompareBy(
			func(c Constraint) string {
				if c.CustomerOrderCount == nil {
					return ""
				}
				return c.CustomerOrderCount.Operator
			},
			ruleix.Path(
				func(c Constraint) *CustomerOrderCount { return c.CustomerOrderCount },
				func(c CustomerOrderCount) *int { return &c.Total },
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
			func(c Constraint) string {
				if c.Platform == nil || c.Platform.Version == nil {
					return ""
				}
				return c.Platform.Version.Operator
			},
			ruleix.Path3(
				func(c Constraint) *Platform { return c.Platform },
				func(p Platform) *Version { return p.Version },
				func(v Version) *SemanticVersion {
					return &SemanticVersion{v.Major, v.Minor, v.Patch}
				},
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
			CustomerOrderCount: &CustomerOrderCount{Operator: ">=", Total: 10},
			StoreUUID:          ptr(StoreUUID("store-1")),
			Platform:           &Platform{Name: "ios", Version: &Version{Operator: ">=", Major: 2}},
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
		CustomerOrderCount: &CustomerOrderCount{Total: 12},
		StoreUUID:          ptr(StoreUUID("store-1")),
		Platform:           &Platform{Name: "ios", Version: &Version{Major: 2, Minor: 1}},
		ABTest:             &ABTest{Label: "checkout", Group: "b"},
	})
	require.Equal(t, []ModifierUUID{"specific", "wildcard"}, got)
}

func TestCompareByAllOperators(t *testing.T) {
	comparisonSchema := ruleix.CompareBy(
		func(c CustomerOrderCount) string { return c.Operator },
		func(c CustomerOrderCount) *int { return &c.Total },
		cmp.Compare[int],
	)
	rules := []struct {
		operator string
		value    int
		id       string
	}{
		{"=", 10, "eq"},
		{"<", 11, "lt"},
		{"<", 10, "lt-strict"},
		{"<=", 10, "lte"},
		{">", 9, "gt"},
		{">", 10, "gt-strict"},
		{">=", 10, "gte"},
	}
	constraints := make([]CustomerOrderCount, len(rules))
	ids := make([]string, len(rules))
	for i, row := range rules {
		constraints[i], ids[i] = CustomerOrderCount{Operator: row.operator, Total: row.value}, row.id
	}
	ix := buildZip(t, comparisonSchema, constraints, ids)
	require.Equal(t, []string{"eq", "lt", "lte", "gt", "gte"}, search(ix, CustomerOrderCount{Total: 10}))
}

func TestCompareByRejectsInvalidOperatorAtomically(t *testing.T) {
	builder := ruleix.New[CustomerOrderCount, string](ruleix.CompareBy(
		func(c CustomerOrderCount) string { return c.Operator },
		func(c CustomerOrderCount) *int { return &c.Total },
		cmp.Compare[int],
	))
	entries, err := ruleix.Zip([]CustomerOrderCount{{Operator: "!="}}, []string{"invalid"})
	require.NoError(t, err)
	ix, err := builder.Build(entries)
	require.Nil(t, ix)
	require.EqualError(t, err, `ruleix: entry 0: ruleix: unsupported operator "!="`)
}

func TestBetweenAndPathWildcard(t *testing.T) {
	t0 := time.Unix(0, 0)
	t1, t2, t3 := t0.Add(time.Hour), t0.Add(2*time.Hour), t0.Add(3*time.Hour)
	intervalSchema := ruleix.BetweenConstraint(
		func(c Constraint) *TimeRange { return c.Activity },
		func(r TimeRange) *time.Time { return &r.Since },
		func(r TimeRange) *time.Time { return &r.Until },
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
		func(c CustomerOrderCount) string { return c.Operator },
		func(c CustomerOrderCount) *int { return &c.Total },
		cmp.Compare[int],
	)
	constraints := make([]CustomerOrderCount, 100)
	ids := make([]int, 100)
	for i := 0; i < 100; i++ {
		constraints[i], ids[i] = CustomerOrderCount{Operator: ">=", Total: i}, i
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
				ix.Search(CustomerOrderCount{Total: 49}, &dst)
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
		func(c CustomerOrderCount) string { return c.Operator },
		func(c CustomerOrderCount) *int { return &c.Total },
		cmp.Compare[int],
	)
	ix := buildZip(t, comparisonSchema,
		[]CustomerOrderCount{{Operator: "=", Total: 10}, {Operator: ">=", Total: 5}, {Operator: "<=", Total: 10}},
		[]string{"duplicate", "duplicate", "last"})
	require.Equal(t, []string{"duplicate", "last"}, search(ix, CustomerOrderCount{Total: 10}))
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

func TestLocalSearchCachesCompareByNodesWithoutChangingResults(t *testing.T) {
	ix := buildZip(t, ruleix.CompareBy(
		func(v benchmarkRange) string { return v.operator },
		func(v benchmarkRange) *int { return v.value },
		cmp.Compare[int],
	), []benchmarkRange{
		{},
		{operator: "=", value: ptr(10)},
		{operator: ">=", value: ptr(5)},
		{operator: "<", value: ptr(15)},
	}, []string{"wildcard", "equal", "minimum", "upper"})
	local := ix.Local()

	var got []string
	local.Search(benchmarkRange{value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "equal", "minimum", "upper"}, got)
	local.Search(benchmarkRange{value: ptr(15)}, &got)
	require.Equal(t, []string{"wildcard", "minimum"}, got)
	local.Search(benchmarkRange{}, &got)
	require.Equal(t, []string{"wildcard"}, got)
	local.Search(benchmarkRange{value: ptr(10)}, &got)
	require.Equal(t, []string{"wildcard", "equal", "minimum", "upper"}, got)
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

func TestParseOperator(t *testing.T) {
	for text, want := range map[string]ruleix.Operator{
		"=": ruleix.OperatorEQ, "<": ruleix.OperatorLT, "<=": ruleix.OperatorLTE,
		">": ruleix.OperatorGT, ">=": ruleix.OperatorGTE,
	} {
		got, err := ruleix.ParseOperator(text)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	_, err := ruleix.ParseOperator("<>")
	require.Error(t, err)
}
