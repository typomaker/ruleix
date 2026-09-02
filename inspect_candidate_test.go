package ruleix

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInspectReportsCommonCompoundStrategyForLossyMode(t *testing.T) {
	type constraint struct {
		value, from, until int
		op                 Operator
	}
	constraints := make([]constraint, 512)
	ids := make([]int, len(constraints))
	for index := range constraints {
		constraints[index] = constraint{value: index, from: index, until: index + 10, op: Operator(index % 5)}
		ids[index] = index
	}
	value := func(v constraint) (int, bool) { return v.value, true }
	tests := []struct {
		name, strategy string
		rule           Rule[constraint]
	}{
		{"Between", "between", Between(
			func(v constraint) (int, bool) { return v.from, true },
			func(v constraint) (int, bool) { return v.until, true }, cmp.Compare[int],
		)},
		{"CompareBy", "compare-by", CompareBy(
			value, func(v constraint) (Operator, bool) { return v.op, true }, cmp.Compare[int],
		)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var inspector Inspector
			_, err := New[constraint, int](Inspect(&inspector, Lossy(
				test.rule, MemoryLimit(8192),
			))).Build(Zip(constraints, ids))
			require.NoError(t, err)
			snapshot := inspector.Snapshot()
			require.Equal(t, RuleModeLossy, snapshot.Mode())
			require.Equal(t, test.strategy, snapshot.Strategy())
			granularity, ok := snapshot.Granularity()
			require.True(t, ok)
			require.NotZero(t, granularity)
		})
	}
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
	before := inspector.Snapshot()

	var matches []string
	observed := newBitmapPool()
	require.True(t, index.search(inspectConstraint{country: "DE"}, &matches, observed))
	matches = matches[:0]
	require.False(t, index.search(inspectConstraint{country: "FR"}, &matches, observed))

	snapshot := inspector.Snapshot()
	require.Zero(t, before.EmptyResult(), "a captured snapshot does not change")
	require.Zero(t, snapshot.CandidateCheck())
	require.Equal(t, uint64(1), snapshot.EmptyResult())
	require.Equal(t, Histogram{Zero: 1, One: 1}, snapshot.ResultCardinality())
}

func TestInspectorRuntimeMetricStoragePaths(t *testing.T) {
	shared := &inspectorRuntime{}
	sharedObserver := inspectorRuntimeObserver{shared: shared}
	local := &inspectorRuntimeValues{}
	localObserver := inspectorRuntimeObserver{local: local}
	for _, observer := range []inspectorRuntimeObserver{sharedObserver, localObserver} {
		observer.candidateCheck()
		observer.rangePruning()
		observer.cacheHit()
		observer.cacheMiss()
		observer.cacheAdmission()
		observer.cacheEviction()
		observer.cacheExpansion()
		for _, cardinality := range []uint64{0, 1, 2, 5, 17, 257} {
			observer.observeCardinality(cardinality)
		}
	}
	require.Equal(t, uint64(1), shared.candidateChecks.Load())
	require.Equal(t, uint64(1), shared.emptyResults.Load())
	require.Equal(t, uint64(1), local.candidateChecks)
	require.Equal(t, uint64(1), local.emptyResults)
	for index := range local.cardinality {
		require.Equal(t, uint64(1), shared.cardinality[index].Load())
		require.Equal(t, uint64(1), local.cardinality[index])
	}
}

func TestInspectorSnapshotStorageDefaults(t *testing.T) {
	implementation := &inspector{}
	snapshot := implementation.Snapshot()
	require.False(t, snapshot.Bound())
	require.Empty(t, snapshot.Mode())
	require.Empty(t, snapshot.Strategy())
	require.Zero(t, snapshot.EntryCount())
	require.Zero(t, snapshot.RuleCount())
	_, available := snapshot.MemoryUsage()
	require.False(t, available)

	exact := exactInspectorSnapshot{modeName: RuleModeLossy}
	require.Equal(t, RuleModeLossy, exact.mode())
}

func TestInspectCountsCandidateChecksWithoutBitmapSearch(t *testing.T) {
	type constraint struct{ selective, broad string }
	var broad Inspector
	index, err := New[constraint, int](All(
		Include(func(v constraint) (string, bool) { return v.selective, true }),
		Inspect(&broad, Include(func(v constraint) (string, bool) { return v.broad, true })),
	)).Build(Zip(
		[]constraint{
			{selective: "one", broad: "yes"},
			{selective: "one", broad: "yes"},
			{selective: "one", broad: "yes"},
			{selective: "one", broad: "yes"},
			{selective: "two", broad: "yes"},
		},
		[]int{1, 2, 3, 4, 5},
	))
	require.NoError(t, err)

	var matches []int
	require.True(t, index.search(constraint{selective: "one", broad: "yes"}, &matches, newBitmapPool()))
	require.Equal(t, uint64(4), broad.Snapshot().CandidateCheck())
}
