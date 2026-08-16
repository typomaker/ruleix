package ruleix_test

import (
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

func TestBuilderIsSingleUseAfterFailedBuild(t *testing.T) {
	builder := ruleix.New[buildConstraint, string](buildSchema())
	_, err := builder.Build(nil)
	require.EqualError(t, err, "ruleix: nil entry sequence")

	entries, err := ruleix.Zip([]buildConstraint{{value: 10, operator: ">="}}, []string{"first"})
	require.NoError(t, err)
	_, err = builder.Build(entries)
	require.EqualError(t, err, "ruleix: builder has already been used")
}

func TestRebuilderCreatesIndependentIndexesAndRecoversAfterError(t *testing.T) {
	rebuilder := ruleix.NewRebuilder[buildConstraint, string](buildSchema())

	firstEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 10, operator: ">="}},
		[]string{"first"},
	)
	require.NoError(t, err)
	first, err := rebuilder.Build(firstEntries)
	require.NoError(t, err)

	invalidEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 20, operator: "invalid"}},
		[]string{"invalid"},
	)
	require.NoError(t, err)
	invalid, err := rebuilder.Build(invalidEntries)
	require.Nil(t, invalid)
	require.EqualError(t, err, `ruleix: entry 0: ruleix: unsupported operator "invalid"`)

	secondEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 20, operator: ">="}},
		[]string{"second"},
	)
	require.NoError(t, err)
	second, err := rebuilder.Build(secondEntries)
	require.NoError(t, err)

	var got []string
	first.Search(buildConstraint{value: 20}, &got)
	require.Equal(t, []string{"first"}, got)
	second.Search(buildConstraint{value: 10}, &got)
	require.Empty(t, got)
	second.Search(buildConstraint{value: 20}, &got)
	require.Equal(t, []string{"second"}, got)
}

func TestRebuilderSerializesConcurrentBuilds(t *testing.T) {
	rebuilder := ruleix.NewRebuilder[buildConstraint, int](buildSchema())
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{})
	secondInvoked := make(chan struct{})
	var active atomic.Int32
	var overlap atomic.Bool

	sequence := func(started chan<- struct{}, release <-chan struct{}, value int) iter.Seq2[buildConstraint, int] {
		return func(yield func(buildConstraint, int) bool) {
			if active.Add(1) != 1 {
				overlap.Store(true)
			}
			defer active.Add(-1)
			close(started)
			if release != nil {
				<-release
			}
			yield(buildConstraint{value: value, operator: ">="}, value)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := rebuilder.Build(sequence(firstStarted, releaseFirst, 1))
		require.NoError(t, err)
	}()
	<-firstStarted
	go func() {
		defer wg.Done()
		close(secondInvoked)
		_, err := rebuilder.Build(sequence(secondStarted, nil, 2))
		require.NoError(t, err)
	}()
	<-secondInvoked

	select {
	case <-secondStarted:
		t.Fatal("second build started before the first build completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	wg.Wait()
	require.False(t, overlap.Load())
}

func TestSchemaBuildsIndependentIndexes(t *testing.T) {
	schema := ruleix.All(
		ruleix.Include(func(v buildConstraint) *int { return &v.value }),
		buildSchema(),
	)

	firstEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 10, operator: ">="}},
		[]string{"first"},
	)
	require.NoError(t, err)
	first, err := ruleix.New[buildConstraint, string](schema).Build(firstEntries)
	require.NoError(t, err)

	secondEntries, err := ruleix.Zip(
		[]buildConstraint{{value: 20, operator: ">="}},
		[]string{"second"},
	)
	require.NoError(t, err)
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
