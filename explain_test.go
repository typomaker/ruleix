package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type explainConstraint struct {
	country *string
	tier    *string
}

func TestExplainCandidateScan(t *testing.T) {
	de := "DE"
	us := "US"
	gold := "gold"
	ix, err := New[explainConstraint, int](All(
		Include(func(value explainConstraint) (string, bool) {
			if value.country == nil {
				return "", false
			}
			return *value.country, true
		}),
		Include(func(value explainConstraint) (string, bool) {
			if value.tier == nil {
				return "", false
			}
			return *value.tier, true
		}),
	)).Build(Zip(
		[]explainConstraint{
			{country: &de, tier: &gold},
			{country: &us, tier: &gold},
			{country: &us},
		},
		[]int{1, 2, 3},
	))
	require.NoError(t, err)

	plan := ix.Explain(explainConstraint{country: &de, tier: &gold})

	require.Equal(t, SearchStrategyCandidateScan, plan.Strategy)
	require.Equal(t, uint64(1), plan.CandidateCardinality)
	require.Equal(t, uint64(1), plan.ResultCardinality)
	require.Len(t, plan.Children, 2)
	require.Equal(t, 0, plan.Children[0].ExecutionOrder)
	require.True(t, plan.Children[0].EstimateAvailable)
	require.Equal(t, uint64(1), plan.Children[0].EstimatedMatches)
	require.Equal(t, uint64(1), plan.Children[0].ActualMatches)
	require.True(t, plan.Children[0].Materialized)
	require.False(t, plan.Children[1].Materialized)
}

func TestExplainBitmapIntersectionOrdersByCardinality(t *testing.T) {
	left := &countingRule{ids: []uint32{0, 1, 2, 3, 4, 5}}
	right := &countingRule{ids: []uint32{0, 1, 2, 3, 4}}
	ix := &Index[int, int]{
		root:   &allRule[int]{children: []Rule[int]{left, right}},
		values: []int{0, 1, 2, 3, 4, 5},
		pool:   newBitmapPool(),
	}

	plan := ix.Explain(0)

	require.Equal(t, SearchStrategyBitmapIntersection, plan.Strategy)
	require.Equal(t, uint64(5), plan.CandidateCardinality)
	require.Equal(t, uint64(5), plan.ResultCardinality)
	require.Equal(t, 1, plan.Children[0].ExecutionOrder)
	require.Equal(t, 0, plan.Children[1].ExecutionOrder)
	require.True(t, plan.Children[0].Materialized)
	require.True(t, plan.Children[1].Materialized)
}

func TestExplainSingleAndEmpty(t *testing.T) {
	value := "present"
	ix, err := New[explainConstraint, int](
		Include(func(value explainConstraint) (string, bool) {
			if value.country == nil {
				return "", false
			}
			return *value.country, true
		}),
	).Build(Zip([]explainConstraint{{country: &value}}, []int{1}))
	require.NoError(t, err)

	require.Equal(t, SearchStrategySingle, ix.Explain(explainConstraint{country: &value}).Strategy)
	empty := "absent"
	plan := ix.Explain(explainConstraint{country: &empty})
	require.Equal(t, SearchStrategySingle, plan.Strategy)
	require.True(t, plan.Empty)
	require.Zero(t, plan.ResultCardinality)
}
