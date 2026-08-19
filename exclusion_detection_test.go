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
	require.False(t, without.hasExclusions)

	with, err := New[constraint, int](All(Include(get), All(Exclude(get)))).Build(entries)
	require.NoError(t, err)
	require.True(t, with.hasExclusions)
}

func TestBuildDropsIneffectiveExcludeFromDetection(t *testing.T) {
	type constraint struct{ value string }
	missing := func(constraint) (string, bool) { return "", false }

	index, err := New[constraint, int](All(Include(missing), Exclude(missing))).Build(
		Zip([]constraint{{}}, []int{1}),
	)
	require.NoError(t, err)
	require.False(t, index.hasExclusions)
}
