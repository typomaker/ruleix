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

func TestExecutionDescriptorCompilesPostingFacts(t *testing.T) {
	rule := &eqRule[int, int]{
		wildcard: roaring.BitmapOf(1, 2),
		values: equalityIndex[int]{sets: []equalitySet{
			{single: 3},
			{small: []uint32{4, 5, 6}},
		}},
	}

	descriptor := describeRuleExecution[int](rule)

	require.Equal(t, ruleExecutionFacts{
		postingCount: 3, minPostingSize: 1, maxPostingSize: 3, totalPostingSize: 6,
		wildcardCardinality: 2, wildcard: wildcardMatchesQueries,
	}, descriptor.facts)
}

type expensiveEstimateRule struct {
	child         *countingRule
	estimateCalls int
}

func (*expensiveEstimateRule) rule()                                                   {}
func (r *expensiveEstimateRule) newState(*nodeIDAllocator, *buildStatistics) Rule[int] { return r }
func (*expensiveEstimateRule) validate(int) error                                      { return nil }
func (*expensiveEstimateRule) insert(int, uint32)                                      {}
func (r *expensiveEstimateRule) cardinality(value int, pool *bitmapPool) uint64 {
	return r.child.cardinality(value, pool)
}
func (r *expensiveEstimateRule) search(value int, dst *roaring.Bitmap, pool *bitmapPool) {
	r.child.search(value, dst, pool)
}
func (*expensiveEstimateRule) exclude(int, *roaring.Bitmap, *bitmapPool)    {}
func (*expensiveEstimateRule) collectBuildStatistics([]nodeBuildStatistics) {}

func (r *expensiveEstimateRule) estimateCardinality(int) uint64 {
	r.estimateCalls++
	return 1
}

func TestShadowDecisionDoesNotRunOrderedEstimate(t *testing.T) {
	expensive := &expensiveEstimateRule{child: &countingRule{ids: []uint32{1}}}
	rule := &allRule[int]{children: []Rule[int]{expensive}}
	rule.prepareSearch()

	decision := rule.shadowDecision(0, newBitmapPool())

	require.Equal(t, shadowExecutionDecision{child: -1, operation: shadowMaterialize, cardinality: ^uint64(0)}, decision)
	require.Zero(t, expensive.estimateCalls)
	require.Zero(t, expensive.child.searchCalls)
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
// 91.46 ns/op, 0 B/op, 0 allocs/op (median of five 1 s runs). Reproduce with:
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
