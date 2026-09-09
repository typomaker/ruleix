//nolint:lll // Migration coverage keeps legacy pointer getters inline.
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
func buildZip[C any, ID comparable](
	t testing.TB,
	schema ruleix.Rule[C],
	constraints []C,
	ids []ID,
) *ruleix.Index[C, ID] {
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
		ruleix.Between(func(c Constraint) (time.Time, bool) {
			if c.Activity == nil {
				return time.Time{}, false
			}
			return c.Activity.Since, true
		}, func(c Constraint) (time.Time, bool) {
			if c.Activity == nil {
				return time.Time{}, false
			}
			return c.Activity.Until, true
		}, compareTime,
		),
		ruleix.CompareBy(func(c Constraint) (int, bool) {
			if c.CustomerOrderCount == nil {
				return 0, false
			}
			return c.CustomerOrderCount.Total, true
		}, func(c Constraint) (ruleix.Operator, bool) {
			if c.CustomerOrderCount == nil || c.CustomerOrderCount.Operator == nil {
				return 0, false
			}
			return *c.CustomerOrderCount.Operator, true
		}, cmp.Compare[int],
		),
		ruleix.Include(ruleix.GetterFromPointer(func(c Constraint) *CustomerUUID { return c.CustomerUUID })),
		ruleix.Include(ruleix.GetterFromPointer(func(c Constraint) *StoreUUID { return c.StoreUUID })),
		ruleix.Include(func(c Constraint) (string, bool) {
			if c.Platform == nil {
				return "", false
			}
			return c.Platform.Name, true
		}),
		ruleix.CompareBy(func(c Constraint) (SemanticVersion, bool) {
			if c.Platform == nil || c.Platform.Version == nil {
				return SemanticVersion{}, false
			}
			v := c.Platform.Version
			return SemanticVersion{v.Major, v.Minor, v.Patch}, true
		}, func(c Constraint) (ruleix.Operator, bool) {
			if c.Platform == nil || c.Platform.Version == nil || c.Platform.Version.Operator == nil {
				return 0, false
			}
			return *c.Platform.Version.Operator, true
		}, compareVersion,
		),
		ruleix.Include(ruleix.GetterFromPointer(func(c Constraint) *DBS { return c.DBS })),
		ruleix.Include(ruleix.GetterFromPointer(func(c Constraint) *MarketType { return c.MarketType })),
		ruleix.Include(func(c Constraint) (string, bool) {
			if c.ABTest == nil {
				return "", false
			}
			return c.ABTest.Label, true
		}),
		ruleix.Include(func(c Constraint) (string, bool) {
			if c.ABTest == nil {
				return "", false
			}
			return c.ABTest.Group, true
		}),
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
	comparisonSchema := ruleix.CompareBy(ruleix.GetterFromPointer(func(c CustomerOrderCount) *int { return &c.Total }), ruleix.GetterFromPointer(func(c CustomerOrderCount) *ruleix.Operator { return c.Operator }), cmp.Compare[int])
	ix := buildZip(t, comparisonSchema, []CustomerOrderCount{
		{Operator: ptr(ruleix.OperatorGTE), Total: 10},
		{Operator: ptr(ruleix.OperatorLTE), Total: 20},
		{Operator: ptr(ruleix.OperatorEQ), Total: 15},
		{Operator: ptr(ruleix.OperatorLT), Total: 15},
		{Operator: ptr(ruleix.OperatorGT), Total: 15},
	}, []string{"gte", "lte", "eq", "lt", "gt"})

	require.Equal(t, []string{"gte", "lte", "eq"}, search(ix, CustomerOrderCount{Total: 15}))
	require.Equal(
		t,
		[]string{"gte", "lte", "eq"},
		search(ix, CustomerOrderCount{Operator: ptr(ruleix.OperatorGT), Total: 15}),
		"query operator must be ignored",
	)
}

func TestCompareByRejectsInvalidInsertedOperator(t *testing.T) {
	invalid := ruleix.Operator(255)
	_, err := ruleix.New[CustomerOrderCount, string](ruleix.CompareBy(ruleix.GetterFromPointer(func(c CustomerOrderCount) *int { return &c.Total }), ruleix.GetterFromPointer(func(c CustomerOrderCount) *ruleix.Operator { return c.Operator }), cmp.Compare[int])).Build(ruleix.Zip([]CustomerOrderCount{{Operator: &invalid, Total: 5}}, []string{"invalid"}))
	require.EqualError(t, err, "ruleix: entry 0: ruleix: unsupported operator 255")
}

func TestCompareByRejectsMissingInsertedOperator(t *testing.T) {
	_, err := ruleix.New[CustomerOrderCount, string](ruleix.CompareBy(
		ruleix.GetterFromPointer(func(c CustomerOrderCount) *int { return &c.Total }),
		ruleix.GetterFromPointer(func(c CustomerOrderCount) *ruleix.Operator { return c.Operator }),
		cmp.Compare[int],
	)).Build(ruleix.Zip([]CustomerOrderCount{{Total: 5}}, []string{"missing-operator"}))
	require.EqualError(t, err, "ruleix: entry 0: ruleix: CompareBy operator is nil")
}

func TestBetweenNestedWildcard(t *testing.T) {
	t0 := time.Unix(0, 0)
	t1, t2, t3 := t0.Add(time.Hour), t0.Add(2*time.Hour), t0.Add(3*time.Hour)
	intervalSchema := ruleix.Between(func(c Constraint) (time.Time, bool) {
		if c.Activity == nil {
			return time.Time{}, false
		}
		return c.Activity.Since, true
	}, func(c Constraint) (time.Time, bool) {
		if c.Activity == nil {
			return time.Time{}, false
		}
		return c.Activity.Until, true
	}, compareTime,
	)
	ix := buildZip(t, intervalSchema,
		[]Constraint{{Activity: &TimeRange{t0, t3}}, {}, {Activity: &TimeRange{t2, t3}}},
		[]string{"covering", "wildcard", "late"})
	require.Equal(t, []string{"covering", "wildcard"}, search(ix, Constraint{Activity: &TimeRange{t1, t2}}))
}

func TestNestedGetter(t *testing.T) {
	schema := ruleix.Include(func(c Constraint) (int, bool) {
		if c.Platform == nil || c.Platform.Version == nil {
			return 0, false
		}
		return c.Platform.Version.Major, true
	})
	ix := buildZip(t, schema,
		[]Constraint{{}, {Platform: &Platform{}}, {Platform: &Platform{Version: &Version{Major: 2}}}},
		[]string{"missing-platform", "missing-version", "version-2"})

	require.Equal(t,
		[]string{"missing-platform", "missing-version", "version-2"},
		search(ix, Constraint{Platform: &Platform{Version: &Version{Major: 2}}}),
	)
}

func TestConcurrentSearchAndBitmapReuse(t *testing.T) {
	comparisonSchema := ruleix.CompareBy(ruleix.GetterFromPointer(func(c CustomerOrderCount) *int { return &c.Total }), ruleix.GetterFromPointer(func(c CustomerOrderCount) *ruleix.Operator { return c.Operator }), cmp.Compare[int])
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
				dst = dst[:0]
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

func TestSearchDeduplicatesAndAppendsToDestination(t *testing.T) {
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return &v.required })),
		[]benchmarkEquality{{required: 1}, {required: 1}, {required: 1}},
		[]string{"same", "other", "same"})

	dst := make([]string, 1, 8)
	dst[0] = "existing"
	before := &dst[0]
	require.True(t, ix.Search(benchmarkEquality{required: 1}, &dst))
	require.Equal(t, []string{"existing", "same", "other"}, dst)
	require.Same(t, before, &dst[0])
}

func TestSearchReportsWhetherCurrentCallMatched(t *testing.T) {
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return &v.required })),
		[]benchmarkEquality{{required: 1}},
		[]string{"match"})

	dst := []string{"existing"}
	require.False(t, ix.Search(benchmarkEquality{required: 2}, &dst))
	require.Equal(t, []string{"existing"}, dst)
	require.True(t, ix.Search(benchmarkEquality{required: 1}, &dst))
	require.Equal(t, []string{"existing", "match"}, dst)
}

func TestLocalSearchReportsWhetherCurrentCallMatched(t *testing.T) {
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return &v.required })),
		[]benchmarkEquality{{required: 1}},
		[]string{"match"})
	local := ix.Local()

	var dst []string
	require.False(t, local.Search(benchmarkEquality{required: 2}, &dst))
	require.True(t, local.Search(benchmarkEquality{required: 1}, &dst))
	require.Equal(t, []string{"match"}, dst)
}

func TestEqNilStoredValueMatchesConcreteSearchValue(t *testing.T) {
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return v.optional })),
		[]benchmarkEquality{{}, {optional: ptr(7)}, {optional: ptr(8)}},
		[]string{"wildcard", "exact", "different"})

	require.Equal(t, []string{"wildcard", "exact"}, search(ix, benchmarkEquality{optional: ptr(7)}))
}

func TestSearchDeduplicatesAcrossMatchingBranches(t *testing.T) {
	comparisonSchema := ruleix.CompareBy(ruleix.GetterFromPointer(func(c CustomerOrderCount) *int { return &c.Total }), ruleix.GetterFromPointer(func(c CustomerOrderCount) *ruleix.Operator { return c.Operator }), cmp.Compare[int])
	ix := buildZip(t, comparisonSchema,
		[]CustomerOrderCount{
			{Operator: ptr(ruleix.OperatorGTE), Total: 10},
			{Operator: ptr(ruleix.OperatorGTE), Total: 5},
			{Operator: ptr(ruleix.OperatorGTE), Total: 10},
		},
		[]string{"duplicate", "duplicate", "last"})
	require.Equal(
		t,
		[]string{"duplicate", "last"},
		search(ix, CustomerOrderCount{Operator: ptr(ruleix.OperatorGTE), Total: 10}),
	)
}

func TestSearchDeduplicatesNonConsecutiveIDsInPostingList(t *testing.T) {
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return &v.required })),
		[]benchmarkEquality{{required: 1}, {required: 1}, {required: 1}, {required: 1}},
		[]string{"first", "second", "third", "first"})

	require.Equal(t, []string{"first", "second", "third"}, search(ix, benchmarkEquality{required: 1}))
}

func TestNotExcludesIDWhenAnyConstraintMatches(t *testing.T) {
	type PlatformConstraint struct{ PlatformName string }
	ix := buildZip(t, ruleix.Exclude(ruleix.GetterFromPointer(func(c PlatformConstraint) *string { return &c.PlatformName })),
		[]PlatformConstraint{{PlatformName: "android"}, {PlatformName: "ios"}, {PlatformName: "ios"}},
		[]int{1, 1, 2})

	require.Equal(t, []int{2}, search(ix, PlatformConstraint{PlatformName: "android"}))
	require.Empty(t, search(ix, PlatformConstraint{PlatformName: "ios"}))
	require.Equal(t, []int{1, 2}, search(ix, PlatformConstraint{PlatformName: "windows"}))
}

func TestSearchUpdatesDestinationWhenItGrows(t *testing.T) {
	constraints := make([]benchmarkEquality, 10)
	ids := make([]int, 10)
	for id := 0; id < 10; id++ {
		constraints[id], ids[id] = benchmarkEquality{required: 1}, id
	}
	ix := buildZip(t, ruleix.Include(ruleix.GetterFromPointer(func(v benchmarkEquality) *int { return &v.required })), constraints, ids)
	dst := make([]int, 0, 1)
	ix.Search(benchmarkEquality{required: 1}, &dst)
	require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, dst)
}
