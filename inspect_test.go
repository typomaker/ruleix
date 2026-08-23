package ruleix

import (
	"cmp"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

type inspectConstraint struct {
	country string
	tier    string
}

func TestInspectIsTransparentAndReportsCompiledStrategy(t *testing.T) {
	var country Inspector
	inspected := New[inspectConstraint, string](All(
		Inspect(&country, Include(func(v inspectConstraint) (string, bool) { return v.country, v.country != "" })),
		Include(func(v inspectConstraint) (string, bool) { return v.tier, v.tier != "" }),
	))
	plain := New[inspectConstraint, string](All(
		Include(func(v inspectConstraint) (string, bool) { return v.country, v.country != "" }),
		Include(func(v inspectConstraint) (string, bool) { return v.tier, v.tier != "" }),
	))
	constraints := []inspectConstraint{{country: "DE", tier: "gold"}, {country: "DE"}, {tier: "silver"}}
	ids := []string{"first", "second", "third"}

	inspectedIndex, err := inspected.Build(Zip(constraints, ids))
	require.NoError(t, err)
	plainIndex, err := plain.Build(Zip(constraints, ids))
	require.NoError(t, err)

	for _, query := range []inspectConstraint{{country: "DE", tier: "gold"}, {country: "US", tier: "silver"}, {}} {
		var got, want []string
		inspectedIndex.Search(query, &got)
		plainIndex.Search(query, &want)
		require.Equal(t, want, got)
	}
	require.True(t, country.Bound())
	require.Equal(t, RuleModeExact, country.Mode())
	require.Equal(t, "equality-unary", country.Strategy())
	require.Equal(t, uint64(3), country.EntryCount())
	require.Equal(t, uint64(3), country.RuleCount())
}

func TestInspectLifecyclePinsUntilReset(t *testing.T) {
	var inspector Inspector
	builder := New[inspectConstraint, string](Inspect(
		&inspector,
		Include(func(v inspectConstraint) (string, bool) { return v.country, true }),
	))
	require.False(t, inspector.Bound())

	_, err := builder.Build(Zip([]inspectConstraint{{country: "DE"}}, []string{"one"}))
	require.NoError(t, err)
	require.False(t, inspector.Bound(), "the pre-build observation remains pinned")
	inspector.Reset()
	require.True(t, inspector.Bound())
	require.Equal(t, uint64(1), inspector.EntryCount())

	_, err = builder.Build(nil)
	require.EqualError(t, err, "ruleix: nil entry sequence")
	require.Equal(t, uint64(1), inspector.EntryCount())

	constraints := []inspectConstraint{{country: "DE"}, {country: "US"}}
	_, err = builder.Build(Zip(constraints, []string{"one", "two"}))
	require.NoError(t, err)
	require.Equal(t, uint64(1), inspector.EntryCount(), "a successful build does not replace the pinned snapshot")
	inspector.Reset()
	require.Equal(t, uint64(2), inspector.EntryCount())
}

func TestInspectRejectsOneInspectorOnMultipleRules(t *testing.T) {
	var inspector Inspector
	inspected := Inspect(&inspector, Include(func(v inspectConstraint) (string, bool) { return v.country, true }))
	schema := All(
		inspected,
		inspected,
	)
	_, err := New[inspectConstraint, string](schema).Build(Zip([]inspectConstraint{{}}, []string{"one"}))
	require.EqualError(t, err, "ruleix: one Inspector cannot inspect multiple rules")
	require.False(t, inspector.Bound())
}

func TestInspectMethodsAreSafeDuringRepeatedBuildsAndResets(t *testing.T) {
	var inspector Inspector
	builder := New[inspectConstraint, string](Inspect(
		&inspector,
		Include(func(v inspectConstraint) (string, bool) { return v.country, true }),
	))

	var readers sync.WaitGroup
	readers.Add(1)
	done := make(chan struct{})
	go func() {
		defer readers.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = inspector.Bound()
				_ = inspector.Mode()
				_ = inspector.Strategy()
				_ = inspector.EntryCount()
				_ = inspector.RuleCount()
				_, _ = inspector.MemoryUsage()
				_, _ = inspector.MemoryLimit()
				_, _ = inspector.ItemCount()
				_, _ = inspector.DistinctValueCount()
				_, _ = inspector.Granularity()
				_, _ = inspector.EstimatedFalsePositiveRate()
			}
		}
	}()
	for size := 1; size <= 10; size++ {
		_, err := builder.Build(func(yield func(inspectConstraint, string) bool) {
			for i := range size {
				yield(inspectConstraint{country: fmt.Sprint(i)}, fmt.Sprint(i))
			}
		})
		require.NoError(t, err)
		inspector.Reset()
	}
	close(done)
	readers.Wait()
	inspector.Reset()
	require.Equal(t, uint64(10), inspector.RuleCount())
}

func TestInspectReportsLossyRepresentationStatistics(t *testing.T) {
	get := func(v lossyConstraint) (string, bool) { return v.name, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{name: fmt.Sprintf("customer-%d", i), present: true}
		ids[i] = i
	}
	constraints[0].present = false

	var inspector Inspector
	_, err := New[lossyConstraint, int](Inspect(
		&inspector,
		Lossy(Include(get), MemoryLimit(5000)),
	)).Build(Zip(constraints, ids))
	require.NoError(t, err)

	usage, ok := inspector.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(5000))
	limit, ok := inspector.MemoryLimit()
	require.True(t, ok)
	require.Equal(t, uint64(5000), limit)
	items, ok := inspector.ItemCount()
	require.True(t, ok)
	require.Equal(t, uint64(2000), items)
	distinct, ok := inspector.DistinctValueCount()
	require.True(t, ok)
	require.Equal(t, uint64(1999), distinct)
	granularity, ok := inspector.Granularity()
	require.True(t, ok)
	require.NotZero(t, granularity)
	_, ok = inspector.EstimatedFalsePositiveRate()
	require.False(t, ok)
}

func TestInspectReportsExactSelectionWithinLossyBudget(t *testing.T) {
	get := func(v inspectConstraint) (string, bool) { return v.country, v.country != "" }
	var inspector Inspector
	_, err := New[inspectConstraint, string](Lossy(
		Inspect(&inspector, Include(get)),
		MemoryLimit(1000),
	)).Build(Zip(
		[]inspectConstraint{{country: "DE"}, {country: "US"}, {}},
		[]string{"one", "two", "three"},
	))
	require.NoError(t, err)
	require.Equal(t, RuleModeExact, inspector.Mode())
	require.Equal(t, "equality-binary", inspector.Strategy())
	usage, ok := inspector.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(1000))
	limit, ok := inspector.MemoryLimit()
	require.True(t, ok)
	require.Equal(t, uint64(1000), limit)
	items, ok := inspector.ItemCount()
	require.True(t, ok)
	require.Equal(t, uint64(3), items)
	distinct, ok := inspector.DistinctValueCount()
	require.True(t, ok)
	require.Equal(t, uint64(2), distinct)
	_, ok = inspector.Granularity()
	require.False(t, ok)
}

func TestInspectReportsLossyOrderedStatistics(t *testing.T) {
	get := func(v lossyConstraint) (int64, bool) { return v.minimum, v.present }
	constraints := make([]lossyConstraint, 2000)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = lossyConstraint{minimum: int64(i), present: true}
		ids[i] = i
	}
	constraints[0].present = false

	var inspector Inspector
	_, err := New[lossyConstraint, int](Inspect(
		&inspector,
		Lossy(GreaterOrEqual(get, cmp.Compare[int64]), MemoryLimit(5000)),
	)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	require.Equal(t, RuleModeLossy, inspector.Mode())
	require.Equal(t, "lossy-ordered-buckets", inspector.Strategy())
	usage, ok := inspector.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(5000))
	items, ok := inspector.ItemCount()
	require.True(t, ok)
	require.Equal(t, uint64(2000), items)
	distinct, ok := inspector.DistinctValueCount()
	require.True(t, ok)
	require.Equal(t, uint64(1999), distinct)
	granularity, ok := inspector.Granularity()
	require.True(t, ok)
	require.NotZero(t, granularity)
}

func TestInspectReportsRuntimeExecutionMetrics(t *testing.T) {
	var inspector Inspector
	index, err := New[inspectConstraint, string](Inspect(&inspector, All(
		Include(func(v inspectConstraint) (string, bool) { return v.country, v.country != "" }),
		Include(func(v inspectConstraint) (string, bool) { return v.country, true }),
	))).Build(Zip(
		[]inspectConstraint{{country: "DE"}, {country: "US"}, {}},
		[]string{"one", "two", "three"},
	))
	require.NoError(t, err)

	var matches []string
	require.True(t, index.Search(inspectConstraint{country: "DE"}, &matches))
	matches = matches[:0]
	require.False(t, index.Search(inspectConstraint{country: "FR"}, &matches))

	require.Equal(t, uint64(2), inspector.Searches())
	require.Zero(t, inspector.Materializations())
	require.Zero(t, inspector.CandidateChecks())
	require.Equal(t, uint64(1), inspector.EmptyResults())
	require.Equal(t, ResultCardinalityHistogram{Zero: 1, One: 1}, inspector.ResultCardinality())
}

func TestInspectCountsCandidateChecksWithoutForcingMaterialization(t *testing.T) {
	type constraint struct{ selective, broad string }
	var broad Inspector
	index, err := New[constraint, int](All(
		Include(func(v constraint) (string, bool) { return v.selective, true }),
		Inspect(&broad, Include(func(v constraint) (string, bool) { return v.broad, true })),
	)).Build(Zip(
		[]constraint{{selective: "one", broad: "yes"}, {selective: "two", broad: "yes"}},
		[]int{1, 2},
	))
	require.NoError(t, err)

	var matches []int
	require.True(t, index.Search(constraint{selective: "one", broad: "yes"}, &matches))
	require.Equal(t, uint64(1), broad.CandidateChecks())
	require.Zero(t, broad.Materializations())
	require.Zero(t, broad.Searches())
}
