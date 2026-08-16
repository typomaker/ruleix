package ruleix_test

import (
	"sync"
	"testing"

	"github.com/typomaker/ruleix"
	"github.com/stretchr/testify/require"
)

type buildConstraint struct {
	value    int
	operator string
}

func buildSchema() ruleix.Rule[buildConstraint] {
	return ruleix.CompareBy(
		func(v buildConstraint) string { return v.operator },
		func(v buildConstraint) *int { return &v.value },
		func(a, b int) int { return a - b },
	)
}

func TestZipRejectsDifferentLengths(t *testing.T) {
	entries, err := ruleix.Zip([]int{1, 2}, []string{"one"})
	require.Nil(t, entries)
	require.EqualError(t, err, "ruleix: cannot zip 2 constraints with 1 IDs")
}

func TestBuildRejectsInvalidEntry(t *testing.T) {
	entries, err := ruleix.Zip(
		[]buildConstraint{{value: 10, operator: "<="}, {value: 20, operator: "invalid"}},
		[]string{"valid", "invalid"},
	)
	require.NoError(t, err)
	ix, err := ruleix.New[buildConstraint, string](buildSchema()).Build(entries)
	require.Nil(t, ix)
	require.EqualError(t, err, `ruleix: entry 1: ruleix: unsupported operator "invalid"`)
}

func TestBuildReportsEntryPositionWithDuplicateIDs(t *testing.T) {
	entries, err := ruleix.Zip(
		[]buildConstraint{{value: 10, operator: "<="}, {value: 20, operator: "<="}, {value: 30, operator: "invalid"}},
		[]string{"duplicate", "duplicate", "invalid"},
	)
	require.NoError(t, err)
	ix, err := ruleix.New[buildConstraint, string](buildSchema()).Build(entries)
	require.Nil(t, ix)
	require.EqualError(t, err, `ruleix: entry 2: ruleix: unsupported operator "invalid"`)
}

func TestBuilderCreatesIndependentIndexesAndRecoversAfterError(t *testing.T) {
	builder := ruleix.New[buildConstraint, string](buildSchema())

	firstEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 10, operator: ">="}},
		[]string{"first"},
	)
	require.NoError(t, err)
	first, err := builder.Build(firstEntries)
	require.NoError(t, err)

	invalidEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 20, operator: "invalid"}},
		[]string{"invalid"},
	)
	require.NoError(t, err)
	invalid, err := builder.Build(invalidEntries)
	require.Nil(t, invalid)
	require.EqualError(t, err, `ruleix: entry 0: ruleix: unsupported operator "invalid"`)

	secondEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 20, operator: ">="}},
		[]string{"second"},
	)
	require.NoError(t, err)
	second, err := builder.Build(secondEntries)
	require.NoError(t, err)

	var got []string
	first.Search(buildConstraint{value: 20}, &got)
	require.Equal(t, []string{"first"}, got)
	second.Search(buildConstraint{value: 10}, &got)
	require.Empty(t, got)
	second.Search(buildConstraint{value: 20}, &got)
	require.Equal(t, []string{"second"}, got)
}

func TestSchemaBuildsIndependentIndexes(t *testing.T) {
	schema := ruleix.All(
		ruleix.Include(func(v buildConstraint) *int { return &v.value }),
		buildSchema(),
	)

	firstEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 10, operator: ">="}},
		[]string{"first"},
	)
	require.NoError(t, err)
	first, err := ruleix.New[buildConstraint, string](schema).Build(firstEntries)
	require.NoError(t, err)

	secondEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 20, operator: ">="}},
		[]string{"second"},
	)
	require.NoError(t, err)
	second, err := ruleix.New[buildConstraint, string](schema).Build(secondEntries)
	require.NoError(t, err)

	var got []string
	first.Search(buildConstraint{value: 10}, &got)
	require.Equal(t, []string{"first"}, got)
	first.Search(buildConstraint{value: 20}, &got)
	require.Empty(t, got, "the later build must not mutate the first index")

	second.Search(buildConstraint{value: 20}, &got)
	require.Equal(t, []string{"second"}, got)
	second.Search(buildConstraint{value: 10}, &got)
	require.Empty(t, got, "indexes must not share mutable posting lists")
}

func TestBuiltIndexSupportsConcurrentSearch(t *testing.T) {
	entries, err := ruleix.Zip([]buildConstraint{{value: 10, operator: ">="}}, []int{1})
	require.NoError(t, err)
	ix, err := ruleix.New[buildConstraint, int](buildSchema()).Build(entries)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got []int
			for i := 0; i < 100; i++ {
				ix.Search(buildConstraint{value: 20}, &got)
				require.Equal(t, []int{1}, got)
			}
		}()
	}
	wg.Wait()
}
