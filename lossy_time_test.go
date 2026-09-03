package ruleix

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLossyTimeTerminalPressureNeverDropsExactMatches(t *testing.T) {
	type constraint struct {
		value time.Time
	}
	get := func(value constraint) (time.Time, bool) { return value.value, true }
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	constraints := make([]constraint, 512)
	ids := make([]int, len(constraints))
	for index := range constraints {
		constraints[index].value = base.Add(time.Duration(index) * 24 * time.Hour)
		ids[index] = index
	}

	exact, err := New[constraint, int](GreaterOrEqual(get, time.Time.Compare)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	approximate, err := New[constraint, int](
		Lossy(GreaterOrEqual(get, time.Time.Compare), MemoryLimit(2048)),
	).Build(Zip(constraints, ids))
	require.NoError(t, err)

	for _, day := range []int{-1, 0, 127, 255, 511, 512} {
		query := constraint{value: base.Add(time.Duration(day) * 24 * time.Hour)}
		var want, got []int
		exact.Search(query, &want)
		approximate.Search(query, &got)
		requireSuperset(t, want, got)
	}
}
