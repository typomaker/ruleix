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

func configureIdentityExecutionMode[T any](_ Rule[T], mode identityExecutionMode) {
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
				require.Equal(t, uint64(4), harness.counters.maskTests)
				require.Equal(t, uint64(3), harness.counters.skippedOperands)
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

func TestIntegratedIdentityCompilesDenseEqualityClassOrdinals(t *testing.T) {
	for _, children := range []int{2, 4, 8} {
		t.Run(fmt.Sprint(children), func(t *testing.T) {
			schema, constraints, ids := identityABFixture(children, false)
			harness := buildIdentityABIndex(t, identityIntegrated, schema, constraints, ids)
			root := harness.index.root.(*allRule[identityABConstraint])
			require.True(t, root.compiledEqualityClasses)
			require.Equal(t, uint32(17), root.equalityClassCount)
			require.Nil(t, root.duplicateBitmapIDs)
			require.Nil(t, root.duplicateEqualityProviders)

			classes := make(map[uint32]struct{}, root.equalityClassCount)
			for _, child := range root.children {
				equality := child.(*eqRule[identityABConstraint, int])
				require.NotZero(t, equality.wildcardClass)
				classes[equality.wildcardClass] = struct{}{}
				for i := range equality.values.sets {
					require.NotZero(t, equality.values.sets[i].class)
					classes[equality.values.sets[i].class] = struct{}{}
				}
			}
			require.Len(t, classes, int(root.equalityClassCount))
			for ordinal := uint32(1); ordinal <= root.equalityClassCount; ordinal++ {
				require.Contains(t, classes, ordinal)
			}
		})
	}
}

func TestIntegratedIdentityCompilesMoreThan64EqualityClasses(t *testing.T) {
	type constraint struct{ left, right int }
	schema := All(
		Include(func(value constraint) (int, bool) { return value.left, true }),
		Include(func(value constraint) (int, bool) { return value.right, true }),
	)
	constraints := make([]constraint, 0, 65*40)
	ids := make([]int, 0, cap(constraints))
	for value := range 65 {
		for range 40 {
			ids = append(ids, len(ids))
			constraints = append(constraints, constraint{left: value, right: value})
		}
	}
	harness := buildIdentityABIndex(t, identityIntegrated, schema, constraints, ids)
	root := harness.index.root.(*allRule[constraint])
	require.Equal(t, uint32(66), root.equalityClassCount) // 65 postings plus the wildcard-only result.
	require.Greater(t, root.equalityClassCount, uint32(64))
	require.Nil(t, root.duplicateBitmapIDs)

	bits := harness.index.pool.get()
	root.search(constraint{left: 64, right: 64}, bits, harness.index.pool)
	require.Equal(t, uint64(40), bits.GetCardinality())
	harness.index.pool.put(bits)
	require.Equal(t, uint64(2), harness.counters.maskTests)
	require.Equal(t, uint64(1), harness.counters.skippedOperands)
}

func TestIntegratedIdentityChecksDenseClassOnce(t *testing.T) {
	schema, constraints, ids := identityABFixture(4, false)
	harness := buildIdentityABIndex(t, identityIntegrated, schema, constraints, ids)
	root := harness.index.root.(*allRule[identityABConstraint])
	query := identityABConstraint{}
	for child := range 4 {
		query.values[child] = 1
	}

	bits := harness.index.pool.get()
	root.search(query, bits, harness.index.pool)
	require.Equal(t, uint64(256), bits.GetCardinality())
	harness.index.pool.put(bits)
	require.Equal(t, uint64(4), harness.counters.maskTests)
	require.Equal(t, uint64(3), harness.counters.skippedOperands)
	require.Zero(t, harness.counters.linearEqualityDedupRuns)
}

func TestIntegratedIdentityUniqueOperandsHaveNoMaskCheck(t *testing.T) {
	type constraint struct{ left, right int }
	schema := All(
		Include(func(value constraint) (int, bool) { return value.left, true }),
		Include(func(value constraint) (int, bool) { return value.right, true }),
	)
	constraints := []constraint{{left: 1, right: 1}, {left: 1, right: 2}, {left: 2, right: 1}}
	harness := buildIdentityABIndex(t, identityIntegrated, schema, constraints, []int{0, 1, 2})
	root := harness.index.root.(*allRule[constraint])

	bits := harness.index.pool.get()
	root.search(constraint{left: 1, right: 1}, bits, harness.index.pool)
	require.Equal(t, uint64(1), bits.GetCardinality())
	harness.index.pool.put(bits)
	require.Zero(t, harness.counters.maskTests)
}

func TestIntegratedIdentityWarmLocalSkipsClassLookups(t *testing.T) {
	schema, constraints, ids := identityABFixture(8, false)
	harness := buildIdentityABIndex(t, identityIntegrated, schema, constraints, ids)
	query := identityABConstraint{}
	for child := range 8 {
		query.values[child] = 1
	}
	local := harness.index.Local()
	defer local.Close()
	var matches []int
	for range 4 {
		matches = matches[:0]
		local.Search(query, &matches)
	}

	before := harness.counters.maskTests
	matches = matches[:0]
	local.Search(query, &matches)
	require.Equal(t, before, harness.counters.maskTests,
		"stable cached operands must bypass equality-class lookup")
}
