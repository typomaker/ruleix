package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

func TestRankedBitmapWorkUsesSerializedShape(t *testing.T) {
	dense := roaring.New()
	dense.AddRange(0, 4096)
	sparse := roaring.New()
	for id := uint32(0); id < 4096; id++ {
		sparse.Add(id * 32)
	}
	denseWork, denseKnown := rankedBitmapWork(rankedBitmap{bits: dense, card: 4096})
	sparseWork, sparseKnown := rankedBitmapWork(rankedBitmap{bits: sparse, card: 4096})
	require.True(t, denseKnown)
	require.True(t, sparseKnown)
	require.Less(t, denseWork, sparseWork)
}

func TestAllTotalCostPrefersCheapBroaderPosting(t *testing.T) {
	broad := roaring.New()
	broad.AddRange(0, 100)
	rule := &allRule[int]{children: []Rule[int]{
		&planningCountingRule{countingRule: &countingRule{}, bits: broad},
		&countingRule{},
	}}
	rule.prepareSearch()
	children := []rankedBitmap{
		{bits: broad, card: 100, childIdx: 0},
		{card: 90, childIdx: 1},
	}
	broadCost, broadKnown := rule.allSourceTotalWork(children[0], children)
	narrowCost, narrowKnown := rule.allSourceTotalWork(children[1], children)
	require.True(t, broadKnown)
	require.True(t, narrowKnown)
	require.Less(t, broadCost, narrowCost)
}

func TestEstimatedBitmapWorkUnknownAndSaturating(t *testing.T) {
	_, known := estimatedBitmapWork(^uint64(0))
	require.False(t, known)
	work, known := estimatedBitmapWork(^uint64(0) - 1)
	require.True(t, known)
	require.Equal(t, ^uint64(0), work)
}

func TestValidationMaterializationBoundary(t *testing.T) {
	bits := roaring.New()
	for id := uint32(0); id < 10_000; id += 10 {
		bits.Add(id)
	}
	rule := &allRule[int]{children: []Rule[int]{&countingRule{}, &countingRule{}}}
	rule.prepareSearch()
	remaining := []rankedBitmap{{bits: bits, card: bits.GetCardinality(), childIdx: 1}}
	require.True(t, rule.shouldValidateRemaining(8, remaining))
	require.False(t, rule.shouldValidateRemaining(1_000, remaining))
}

func TestValidationComparesUnmaterializedRemainingChild(t *testing.T) {
	rule := &allRule[int]{children: []Rule[int]{&countingRule{}, &countingRule{}}}
	rule.prepareSearch()

	require.True(t, rule.shouldValidateRemaining(16, []rankedBitmap{{card: 100_000, childIdx: 1}}))
	require.False(t, rule.shouldValidateRemaining(1_000, []rankedBitmap{{card: 10, childIdx: 1}}))
	require.False(t, rule.shouldValidateRemaining(allCandidateScanLimit+1, []rankedBitmap{{card: ^uint64(0), childIdx: 1}}))
}

func TestSelectNextBitmapOperationPrefersCheapBroaderPosting(t *testing.T) {
	cheapBroad := roaring.New()
	cheapBroad.AddRange(0, 10_000)
	expensiveSparse := roaring.New()
	for id := uint32(0); id < 9_000; id++ {
		expensiveSparse.Add(id * 32)
	}
	remaining := []rankedBitmap{
		{bits: expensiveSparse, card: expensiveSparse.GetCardinality(), childIdx: 1},
		{bits: cheapBroad, card: cheapBroad.GetCardinality(), childIdx: 2},
	}

	require.Equal(t, 1, selectNextBitmapOperation(remaining))
}

func TestSelectNextBitmapOperationLeavesUnknownWorkLast(t *testing.T) {
	posting := roaring.BitmapOf(1, 2, 3)
	remaining := []rankedBitmap{
		{card: ^uint64(0), childIdx: 1},
		{bits: posting, card: posting.GetCardinality(), childIdx: 2},
	}

	require.Equal(t, 1, selectNextBitmapOperation(remaining))
}
