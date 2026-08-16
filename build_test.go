package ruleix_test

import (
	"sync"
	"testing"

	"github.com/albertsultanov/ruleix"
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

func TestBuilderIsSingleUse(t *testing.T) {
	builder := ruleix.New[buildConstraint, string](buildSchema())
	entries, err := ruleix.Zip([]buildConstraint{{value: 10, operator: ">="}}, []string{"first"})
	require.NoError(t, err)
	_, err = builder.Build(entries)
	require.NoError(t, err)
	_, err = builder.Build(entries)
	require.EqualError(t, err, "ruleix: builder has already been used")
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
