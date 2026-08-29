package ruleix

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// identityExecutionMode is deliberately confined to tests. The integrated
// experiment can change compilation and execution without publishing a
// planner option or making separately-built indexes incomparable.
type identityExecutionMode uint8

const (
	identityBaseline identityExecutionMode = iota
	identityIntegrated
)

type identityABIndex[C any, ID comparable] struct {
	index    *Index[C, ID]
	counters *allExecutionCounters
}

func buildIdentityABIndex[C any, ID comparable](
	t testing.TB,
	mode identityExecutionMode,
	schema Rule[C],
	constraints []C,
	ids []ID,
) identityABIndex[C, ID] {
	t.Helper()
	index, _, err := buildIndexPhysicalAliases(schema, Zip(constraints, ids), false, nil, mode == identityIntegrated)
	require.NoError(t, err)
	result := identityABIndex[C, ID]{index: index, counters: &allExecutionCounters{}}
	attachIdentityABCounters(index.root, result.counters)
	if index.observedRoot != index.root {
		attachIdentityABCounters(index.observedRoot, result.counters)
	}
	configureIdentityExecutionMode(index.root, mode)
	return result
}

func attachIdentityABCounters[T any](rule Rule[T], counters *allExecutionCounters) {
	switch typed := rule.(type) {
	case *allRule[T]:
		typed.executionCounters = counters
		for _, child := range typed.children {
			attachIdentityABCounters(child, counters)
		}
	case *inspectedRuntimeRule[T]:
		attachIdentityABCounters(typed.child, counters)
	}
}

func configureIdentityExecutionMode[T any](rule Rule[T], mode identityExecutionMode) {
	// The mode switch is intentionally explicit before the integrated compiler
	// is added. Keeping both cases callable makes every later milestone extend
	// this harness instead of replacing its baseline.
	switch mode {
	case identityBaseline, identityIntegrated:
	default:
		panic(fmt.Sprintf("ruleix: unknown identity execution mode %d", mode))
	}
}

type identityABConstraint struct{ values [8]int }

func identityABFixture(children int, nested bool) (Rule[identityABConstraint], []identityABConstraint, []int) {
	rules := make([]Rule[identityABConstraint], children)
	for child := range children {
		index := child
		rules[child] = Include(func(value identityABConstraint) (int, bool) {
			return value.values[index], true
		})
	}
	schema := All(rules...)
	if nested {
		// A match-all sibling retains the inner All as an executable child and
		// therefore exercises allRule.searchRanked rather than searchAllMatches.
		schema = All(schema, Include(func(identityABConstraint) (int, bool) { return 0, false }))
	}
	constraints := make([]identityABConstraint, 4096)
	ids := make([]int, len(constraints))
	for id := range constraints {
		for child := range children {
			constraints[id].values[child] = id % 16
		}
		ids[id] = id
	}
	return schema, constraints, ids
}

func TestIdentityABModesUsePublicRootAllContract(t *testing.T) {
	var baseline []int
	for _, mode := range []identityExecutionMode{identityBaseline, identityIntegrated} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			schema, constraints, ids := identityABFixture(4, false)
			harness := buildIdentityABIndex(t, mode, schema, constraints, ids)
			query := identityABConstraint{}
			for child := range 4 {
				query.values[child] = 1
			}
			var got []int
			harness.index.Search(query, &got)
			require.Len(t, got, 256)
			if mode == identityBaseline {
				baseline = append([]int(nil), got...)
			} else {
				require.Equal(t, baseline, got)
			}
			require.Zero(t, harness.counters.linearEqualityDedupRuns,
				"root All must execute through searchAllMatches, not allRule.searchRanked")
		})
	}
}

func TestIdentityABNestedAllAccountsLinearEqualityDedup(t *testing.T) {
	schema, constraints, ids := identityABFixture(4, false)
	harness := buildIdentityABIndex(t, identityBaseline, schema, constraints, ids)
	query := identityABConstraint{}
	for child := range 4 {
		query.values[child] = 1
	}
	root := harness.index.root.(*allRule[identityABConstraint])
	require.NotNil(t, root.duplicateBitmapIDs)
	bits := harness.index.pool.get()
	root.search(query, bits, harness.index.pool)
	require.Equal(t, uint64(256), bits.GetCardinality())
	harness.index.pool.put(bits)
	require.NotZero(t, harness.counters.linearEqualityDedupRuns)
}
