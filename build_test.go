package ruleix_test

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

type buildConstraint struct {
	value    int
	operator *ruleix.Operator
}

func buildSchema() ruleix.Rule[buildConstraint] {
	return ruleix.CompareBy(
		func(v buildConstraint) *int { return &v.value },
		func(v buildConstraint) *ruleix.Operator { return v.operator },
		func(a, b int) int { return a - b },
	)
}

func TestZipPanicsForDifferentLengths(t *testing.T) {
	require.PanicsWithValue(t, "ruleix: cannot zip 2 constraints with 1 IDs", func() {
		ruleix.Zip([]int{1, 2}, []string{"one"})
	})
}

func TestBuilderCreatesIndependentIndexes(t *testing.T) {
	builder := ruleix.New[buildConstraint, string](buildSchema())

	firstEntries := ruleix.Zip(
		[]buildConstraint{{value: 10}},
		[]string{"first"},
	)
	first, err := builder.Build(firstEntries)
	require.NoError(t, err)

	secondEntries := ruleix.Zip(
		[]buildConstraint{{value: 20}},
		[]string{"second"},
	)
	second, err := builder.Build(secondEntries)
	require.NoError(t, err)

	var got []string
	first.Search(buildConstraint{value: 20, operator: ptr(ruleix.OperatorGTE)}, &got)
	require.Equal(t, []string{"first"}, got)
	second.Search(buildConstraint{value: 10, operator: ptr(ruleix.OperatorGTE)}, &got)
	require.Empty(t, got)
	second.Search(buildConstraint{value: 20, operator: ptr(ruleix.OperatorGTE)}, &got)
	require.Equal(t, []string{"second"}, got)
}

func TestSchemaBuildsIndependentIndexes(t *testing.T) {
	schema := ruleix.All(
		ruleix.Include(func(v buildConstraint) *int { return &v.value }),
		buildSchema(),
	)

	firstEntries := ruleix.Zip(
		[]buildConstraint{{value: 10}},
		[]string{"first"},
	)
	first, err := ruleix.New[buildConstraint, string](schema).Build(firstEntries)
	require.NoError(t, err)

	secondEntries := ruleix.Zip(
		[]buildConstraint{{value: 20}},
		[]string{"second"},
	)
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
	entries := ruleix.Zip([]buildConstraint{{value: 10}}, []int{1})
	ix, err := ruleix.New[buildConstraint, int](buildSchema()).Build(entries)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for n := 0; n < 16; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got []int
			for i := 0; i < 100; i++ {
				ix.Search(buildConstraint{value: 20, operator: ptr(ruleix.OperatorGTE)}, &got)
				require.Equal(t, []int{1}, got)
			}
		}()
	}
	wg.Wait()
}
