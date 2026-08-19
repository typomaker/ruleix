package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildDetectsWhetherSchemaHasExclusions(t *testing.T) {
	type constraint struct{ value string }
	get := func(value constraint) (string, bool) { return value.value, true }
	entries := Zip([]constraint{{value: "one"}}, []int{1})

	without, err := New[constraint, int](All(Include(get), Include(get))).Build(entries)
	require.NoError(t, err)
	require.Empty(t, without.exclusions)

	with, err := New[constraint, int](All(Include(get), All(Exclude(get)))).Build(entries)
	require.NoError(t, err)
	require.Len(t, with.exclusions, 1)
}

func TestBuildDropsIneffectiveExcludeFromDetection(t *testing.T) {
	type constraint struct{ value string }
	missing := func(constraint) (string, bool) { return "", false }

	index, err := New[constraint, int](All(Include(missing), Exclude(missing))).Build(
		Zip([]constraint{{}}, []int{1}),
	)
	require.NoError(t, err)
	require.Empty(t, index.exclusions)
}

func TestAllExclusionsMatchAcrossCandidateScanThresholds(t *testing.T) {
	type constraint struct{ included, excluded int }
	schema := All(
		Include(func(value constraint) (int, bool) { return value.included, true }),
		Exclude(func(value constraint) (int, bool) { return value.excluded, true }),
	)
	for _, count := range []int{10, 100} {
		constraints := make([]constraint, count)
		ids := make([]int, count)
		want := make([]int, 0, count/2)
		for id := range count {
			constraints[id] = constraint{included: 1, excluded: id % 2}
			ids[id] = id
			if id%2 == 0 {
				want = append(want, id)
			}
		}
		index, err := New[constraint, int](schema).Build(Zip(constraints, ids))
		require.NoError(t, err)

		query := constraint{included: 1, excluded: 1}
		var got []int
		index.Search(query, &got)
		require.Equal(t, want, got)
		got = got[:0]
		index.Local().Search(query, &got)
		require.Equal(t, want, got)
	}
}
