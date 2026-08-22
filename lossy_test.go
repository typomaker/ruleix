package ruleix

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type lossyConstraint struct {
	name    string
	minimum int64
	present bool
}

func requireSuperset(t *testing.T, exact, approximate []int) {
	t.Helper()
	got := make(map[int]bool, len(approximate))
	for _, id := range approximate {
		got[id] = true
	}
	for _, id := range exact {
		require.True(t, got[id], "lossy result dropped id %d", id)
	}
}

func TestLossyEqualityNeverDropsExactMatches(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{name: fmt.Sprintf("customer-%d", i), present: true}
		ids[i] = i
	}
	constraints[0].present = false
	exact, err := New[lossyConstraint, int](Include(get)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	approximate, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(Include(get), MemoryLimit(5000)))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for i := 0; i < len(constraints); i += 37 {
		var want, got []int
		exact.Search(constraints[i], &want)
		approximate.Search(constraints[i], &got)
		requireSuperset(t, want, got)
	}
	require.Equal(t, RuleModeLossy, inspector.Mode())
	require.Equal(t, "lossy-grouped-hash", inspector.Strategy())
}

func TestLossyOrderedNeverDropsExactMatches(t *testing.T) {
	get := func(v lossyConstraint) (int64, bool) { return v.minimum, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{minimum: int64(i - 1000), present: true}
		ids[i] = i
	}
	constraints[0].present = false
	exact, err := New[lossyConstraint, int](GreaterOrEqual(get, cmp.Compare[int64])).Build(Zip(constraints, ids))
	require.NoError(t, err)
	approximate, err := New[lossyConstraint, int](Lossy(GreaterOrEqual(get, cmp.Compare[int64]), MemoryLimit(5000))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for q := int64(-1100); q <= 1100; q += 53 {
		query := lossyConstraint{minimum: q, present: true}
		var want, got []int
		exact.Search(query, &want)
		approximate.Search(query, &got)
		requireSuperset(t, want, got)
	}
}

func TestLossyPolicyValidation(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, true }
	empty := Zip([]lossyConstraint{}, []int{})
	_, err := New[lossyConstraint, int](Lossy(Include(get))).Build(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "MemoryLimit")
	_, err = New[lossyConstraint, int](Lossy(Include(get), MemoryLimit(1), MemoryLimit(2))).Build(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one")
	_, err = New[lossyConstraint, int](Lossy(All(Include(get)), MemoryLimit(100))).Build(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "directly decorate All")
}
