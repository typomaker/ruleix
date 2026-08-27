package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestExecutionDescriptorMakesMissingCapabilitiesExplicit(t *testing.T) {
	rule := &checkerOnlyRule{child: &countingRule{ids: []uint32{1}}}
	descriptor := describeRuleExecution[int](rule)

	require.Equal(t, executionMaterialize, descriptor.capabilities)
	require.Equal(t, executionCostUnavailable, descriptor.matchID)
	require.Equal(t, executionCostUnavailable, descriptor.filter)
	require.Equal(t, executionCostUnavailable, descriptor.stream)
	require.Equal(t, executionCostPerPosting, descriptor.materialize)
}

func TestExecutionDescriptorRecordsRepresentationOperations(t *testing.T) {
	ordered := &orderedRule[int, int]{
		get:      func(value int) (int, bool) { return value, true },
		compare:  func(a, b int) int { return a - b },
		dir:      lessThan,
		wildcard: roaring.New(),
		index:    newOrderedIndex[int](func(a, b int) int { return a - b }),
	}
	descriptor := describeRuleExecution[int](ordered)

	require.Equal(t,
		executionEstimate|executionMatchID|executionFilterCandidates|executionOrderedStream|executionMaterialize,
		descriptor.capabilities,
	)
	require.Equal(t, executionCostOrderedWalk, descriptor.estimate)
	require.Equal(t, executionCostPerCandidate, descriptor.matchID)
	require.Equal(t, executionCostPerPosting, descriptor.filter)
}

func TestShadowDecisionSelectsSmallCandidateValidation(t *testing.T) {
	broad := &countingRule{ids: []uint32{1, 2, 3, 4, 5, 6, 7, 8, 9}}
	selective := &countingRule{ids: []uint32{3}}
	rule := &allRule[int]{children: []Rule[int]{broad, selective}}
	rule.prepareSearch()

	decision := rule.shadowDecision(0, newBitmapPool())

	require.Equal(t, shadowExecutionDecision{
		child: 1, operation: shadowValidateCandidates, cardinality: 1,
	}, decision)
	require.Zero(t, broad.searchCalls)
	require.Zero(t, selective.searchCalls)
}

func TestShadowDecisionDoesNotInventMatchIDFallback(t *testing.T) {
	selective := &countingRule{ids: []uint32{3}}
	withoutMatcher := &checkerOnlyRule{child: &countingRule{ids: []uint32{3}}}
	rule := &allRule[int]{children: []Rule[int]{selective, withoutMatcher}}
	rule.prepareSearch()

	decision := rule.shadowDecision(0, newBitmapPool())

	require.Equal(t, shadowMaterialize, decision.operation)
	require.Zero(t, withoutMatcher.child.searchCalls)
}

// BenchmarkAllShadowDecision measures only the opt-in shadow planner; the
// production Search path does not invoke it. Last local run on Apple M1 Max:
// 104.6 ns/op, 0 B/op, 0 allocs/op (median of five 1 s runs). Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkAllShadowDecision$' -benchmem -benchtime=1s -count=5 .
func BenchmarkAllShadowDecision(b *testing.B) {
	rule := &allRule[int]{children: []Rule[int]{
		&matchAllRule[int]{bits: roaring.BitmapOf(1, 2, 3, 4)},
		&matchAllRule[int]{bits: roaring.BitmapOf(3)},
		&matchAllRule[int]{bits: roaring.BitmapOf(2, 3, 4)},
	}}
	rule.prepareSearch()
	pool := newBitmapPool()
	b.ReportAllocs()
	for range b.N {
		decision := rule.shadowDecision(0, pool)
		plannerBenchmarkCardinality = decision.cardinality
	}
}
