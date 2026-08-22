package ruleix

import (
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
	var country RuleInspector
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
	var inspector RuleInspector
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
	var inspector RuleInspector
	schema := All(
		Inspect(&inspector, Include(func(v inspectConstraint) (string, bool) { return v.country, true })),
		Inspect(&inspector, Include(func(v inspectConstraint) (string, bool) { return v.tier, true })),
	)
	_, err := New[inspectConstraint, string](schema).Build(Zip([]inspectConstraint{{}}, []string{"one"}))
	require.EqualError(t, err, "ruleix: one RuleInspector cannot inspect multiple rules")
	require.False(t, inspector.Bound())
}

func TestInspectMethodsAreSafeDuringRepeatedBuildsAndResets(t *testing.T) {
	var inspector RuleInspector
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
