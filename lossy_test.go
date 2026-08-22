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
	_, err = New[lossyConstraint, int](Lossy(All(Lossy(Include(get), MemoryLimit(100))), MemoryLimit(100))).Build(empty)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nested Lossy")
}

func TestLossyAllNeverDropsExactMatches(t *testing.T) {
	name := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	minimum := func(v lossyConstraint) (int64, bool) { return v.minimum, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{name: fmt.Sprintf("customer-%d", i), minimum: int64(i - 1000), present: true}
		ids[i] = i
	}
	exact, err := New[lossyConstraint, int](All(Include(name), GreaterOrEqual(minimum, cmp.Compare[int64]))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	approximate, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(
		All(Include(name), GreaterOrEqual(minimum, cmp.Compare[int64])),
		MemoryLimit(16000),
	))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	for i := 0; i < 100; i++ {
		query := lossyConstraint{name: fmt.Sprintf("customer-%d", i*19), minimum: int64(i*23 - 1000), present: true}
		var want, got []int
		exact.Search(query, &want)
		approximate.Search(query, &got)
		requireSuperset(t, want, got)
	}
	usage, ok := inspector.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(16000))
	actualLimit, ok := inspector.MemoryLimit()
	require.True(t, ok)
	require.Equal(t, uint64(16000), actualLimit)
	require.Equal(t, RuleModeLossy, inspector.Mode())
	require.Equal(t, "all", inspector.Strategy())
}

func TestLossyAllRetainsExactChildrenWhenCompositeFits(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, true }
	var inspector Inspector
	_, err := New[lossyConstraint, int](Inspect(&inspector, Lossy(
		All(Include(get), Include(get)),
		MemoryLimit(1<<20),
	))).Build(Zip([]lossyConstraint{{name: "a"}, {name: "b"}}, []int{1, 2}))
	require.NoError(t, err)
	require.Equal(t, RuleModeExact, inspector.Mode())
	usage, ok := inspector.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(1<<20))
}
