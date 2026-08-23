package ruleix

import (
	"cmp"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type inspectConstraint struct {
	country string
	tier    string
}

type snapshotAPI interface {
	Bound() bool
	Mode() RuleMode
	Strategy() string
	EntryCount() uint64
	RuleCount() uint64
	MemoryUsage() (uint64, bool)
	MemoryLimit() (uint64, bool)
	ItemCount() (uint64, bool)
	DistinctValueCount() (uint64, bool)
	Granularity() (uint64, bool)
	FalsePositiveRate() (float64, bool)
	Search() uint64
	CacheHit() uint64
	CacheMiss() uint64
	CacheAdmission() uint64
	CacheEviction() uint64
	Materialization() uint64
	CandidateCheck() uint64
	RangePruning() uint64
	EmptyResult() uint64
	CacheEntry() uint64
	CacheCapacity() uint64
	ResultCardinality() Histogram
}

var _ snapshotAPI = InspectorSnapshot{}

func TestInspectReportsAllRangePruning(t *testing.T) {
	type constraint struct{ left, right int }
	constraints := make([]constraint, 12)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = constraint{left: 1, right: 0}
		if i >= len(constraints)/2 {
			constraints[i] = constraint{left: 0, right: 1}
		}
		ids[i] = i
	}

	var inspector Inspector
	index, err := New[constraint, int](Inspect(&inspector, All(
		Include(func(v constraint) (int, bool) { return v.left, true }),
		Include(func(v constraint) (int, bool) { return v.right, true }),
	))).Build(Zip(constraints, ids))
	require.NoError(t, err)

	var matches []int
	require.False(t, index.Search(constraint{left: 1, right: 1}, &matches))
	snapshot := inspector.Snapshot()
	require.Equal(t, uint64(1), snapshot.Search())
	require.Equal(t, uint64(1), snapshot.RangePruning())
	require.Equal(t, uint64(1), snapshot.EmptyResult())
}

func TestInspectReportsLaterAllRangePruning(t *testing.T) {
	type constraint struct{ first, second, third int }
	constraints := make([]constraint, 17)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = constraint{
			first:  boolInt(i < 5),
			second: boolInt(i >= 3 && i < 9),
			third:  boolInt(i >= 10),
		}
		ids[i] = i
	}

	var inspector Inspector
	index, err := New[constraint, int](Inspect(&inspector, All(
		Include(func(v constraint) (int, bool) { return v.first, true }),
		Include(func(v constraint) (int, bool) { return v.second, true }),
		Include(func(v constraint) (int, bool) { return v.third, true }),
	))).Build(Zip(constraints, ids))
	require.NoError(t, err)

	var matches []int
	require.False(t, index.Search(constraint{first: 1, second: 1, third: 1}, &matches))
	require.Equal(t, uint64(1), inspector.Snapshot().RangePruning())
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
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
	snapshot := country.Snapshot()
	require.True(t, snapshot.Bound())
	require.Equal(t, RuleModeExact, snapshot.Mode())
	require.Equal(t, "equality-unary", snapshot.Strategy())
	require.Equal(t, uint64(3), snapshot.EntryCount())
	require.Equal(t, uint64(3), snapshot.RuleCount())
}

func TestInspectLifecycleTracksLatestSuccessfulBuild(t *testing.T) {
	require.False(t, (InspectorSnapshot{}).Bound(), "the zero snapshot is unbound")

	var inspector Inspector
	builder := New[inspectConstraint, string](Inspect(
		&inspector,
		Include(func(v inspectConstraint) (string, bool) { return v.country, true }),
	))
	require.False(t, inspector.Snapshot().Bound())

	_, err := builder.Build(Zip([]inspectConstraint{{country: "DE"}}, []string{"one"}))
	require.NoError(t, err)
	require.True(t, inspector.Snapshot().Bound())
	require.Equal(t, uint64(1), inspector.Snapshot().EntryCount())
	first := inspector.Snapshot()

	_, err = builder.Build(nil)
	require.EqualError(t, err, "ruleix: nil entry sequence")
	require.Equal(t, uint64(1), inspector.Snapshot().EntryCount())

	constraints := []inspectConstraint{{country: "DE"}, {country: "US"}}
	_, err = builder.Build(Zip(constraints, []string{"one", "two"}))
	require.NoError(t, err)
	require.Equal(t, uint64(2), inspector.Snapshot().EntryCount())
	require.Equal(t, uint64(1), first.EntryCount(), "a captured snapshot remains on its build generation")
}

func TestInspectReportsLocalCacheMetricsAndCloseGauges(t *testing.T) {
	var inspector Inspector
	index, err := New[inspectConstraint, string](Inspect(
		&inspector,
		Include(func(v inspectConstraint) (string, bool) { return v.country, true }),
	)).Build(Zip(
		[]inspectConstraint{{country: "DE"}, {country: "US"}, {country: "FR"}},
		[]string{"one", "two", "three"},
	))
	require.NoError(t, err)
	local := index.Local()
	var dst []string
	for range 3 {
		dst = dst[:0]
		local.Search(inspectConstraint{country: "DE"}, &dst)
	}
	snapshot := inspector.Snapshot()
	require.Equal(t, uint64(1), snapshot.CacheHit())
	require.Equal(t, uint64(2), snapshot.CacheMiss())
	require.Equal(t, uint64(1), snapshot.CacheAdmission())
	require.Zero(t, snapshot.CacheEviction())
	require.Equal(t, uint64(1), snapshot.CacheEntry())
	require.Equal(t, uint64(2), snapshot.CacheCapacity())

	local.Close()
	snapshot = inspector.Snapshot()
	require.Equal(t, uint64(1), snapshot.CacheAdmission(), "counters remain monotonic")
	require.Zero(t, snapshot.CacheEntry())
	require.Zero(t, snapshot.CacheCapacity())
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
	require.False(t, inspector.Snapshot().Bound())
}

func TestInspectMethodsAreSafeDuringRepeatedBuilds(t *testing.T) {
	var inspector Inspector
	builder := New[inspectConstraint, string](Inspect(
		&inspector,
		Include(func(v inspectConstraint) (string, bool) { return v.country, true }),
	))

	var readers sync.WaitGroup
	var mixed atomic.Bool
	readers.Add(1)
	done := make(chan struct{})
	go func() {
		defer readers.Done()
		for {
			select {
			case <-done:
				return
			default:
				snapshot := inspector.Snapshot()
				if snapshot.Bound() && snapshot.EntryCount() != 2*snapshot.RuleCount() {
					mixed.Store(true)
				}
			}
		}
	}()
	for size := 1; size <= 10; size++ {
		_, err := builder.Build(func(yield func(inspectConstraint, string) bool) {
			for i := range size {
				yield(inspectConstraint{country: fmt.Sprint(i)}, fmt.Sprint(i))
				yield(inspectConstraint{country: fmt.Sprint(i)}, fmt.Sprint(i))
			}
		})
		require.NoError(t, err)
	}
	close(done)
	readers.Wait()
	require.False(t, mixed.Load(), "one snapshot must not mix build generations")
	require.Equal(t, uint64(10), inspector.Snapshot().RuleCount())
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

	snapshot := inspector.Snapshot()
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(5000))
	limit, ok := snapshot.MemoryLimit()
	require.True(t, ok)
	require.Equal(t, uint64(5000), limit)
	items, ok := snapshot.ItemCount()
	require.True(t, ok)
	require.Equal(t, uint64(2000), items)
	distinct, ok := snapshot.DistinctValueCount()
	require.True(t, ok)
	require.Equal(t, uint64(1999), distinct)
	granularity, ok := snapshot.Granularity()
	require.True(t, ok)
	require.NotZero(t, granularity)
	falsePositiveRate, ok := snapshot.FalsePositiveRate()
	require.True(t, ok)
	require.GreaterOrEqual(t, falsePositiveRate, 0.0)
	require.LessOrEqual(t, falsePositiveRate, 1.0)
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
	snapshot := inspector.Snapshot()
	require.Equal(t, RuleModeExact, snapshot.Mode())
	require.Equal(t, "equality-binary", snapshot.Strategy())
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(1000))
	limit, ok := snapshot.MemoryLimit()
	require.True(t, ok)
	require.Equal(t, uint64(1000), limit)
	items, ok := snapshot.ItemCount()
	require.True(t, ok)
	require.Equal(t, uint64(3), items)
	distinct, ok := snapshot.DistinctValueCount()
	require.True(t, ok)
	require.Equal(t, uint64(2), distinct)
	_, ok = snapshot.Granularity()
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
	snapshot := inspector.Snapshot()
	require.Equal(t, RuleModeLossy, snapshot.Mode())
	require.Equal(t, "lossy-ordered-buckets", snapshot.Strategy())
	usage, ok := snapshot.MemoryUsage()
	require.True(t, ok)
	require.LessOrEqual(t, usage, uint64(5000))
	items, ok := snapshot.ItemCount()
	require.True(t, ok)
	require.Equal(t, uint64(2000), items)
	distinct, ok := snapshot.DistinctValueCount()
	require.True(t, ok)
	require.Equal(t, uint64(1999), distinct)
	granularity, ok := snapshot.Granularity()
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
	beforeSearches := inspector.Snapshot()

	var matches []string
	require.True(t, index.Search(inspectConstraint{country: "DE"}, &matches))
	matches = matches[:0]
	require.False(t, index.Search(inspectConstraint{country: "FR"}, &matches))

	snapshot := inspector.Snapshot()
	require.Zero(t, beforeSearches.Search(), "a captured snapshot does not change")
	require.Equal(t, uint64(2), snapshot.Search())
	require.Zero(t, snapshot.Materialization())
	require.Zero(t, snapshot.CandidateCheck())
	require.Equal(t, uint64(1), snapshot.EmptyResult())
	require.Equal(t, Histogram{Zero: 1, One: 1}, snapshot.ResultCardinality())
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
	snapshot := broad.Snapshot()
	require.Equal(t, uint64(1), snapshot.CandidateCheck())
	require.Zero(t, snapshot.Materialization())
	require.Zero(t, snapshot.Search())
}
