package ruleix

import (
	"slices"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type streamingEqualityFixture struct {
	value   string
	present bool
}

func streamingEqualityValue(v streamingEqualityFixture) (string, bool) { return v.value, v.present }

func buildExactStreamingEquality(order []int) *eqRule[streamingEqualityFixture, string] {
	rule := &eqRule[streamingEqualityFixture, string]{
		get: streamingEqualityValue, wildcard: roaring.New(),
		values: newEqualityIndex[equalityPhysicalKey[string]](len(order)),
	}
	values := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	for _, id := range order {
		rule.insert(streamingEqualityFixture{value: values[id], present: true}, uint32(id))
		rule.insert(streamingEqualityFixture{value: values[id], present: true}, uint32(id))
	}
	rule.insert(streamingEqualityFixture{}, 99)
	return rule
}

func equalityGenerationShape(rule *eqRule[streamingEqualityFixture, string]) []uint64 {
	keys := make([]uint64, 0, len(rule.values.sets))
	rule.values.visit(func(key equalityPhysicalKey[string], _ *equalitySet) { keys = append(keys, key.bucket) })
	slices.Sort(keys)
	return keys
}

func TestEqualityStreamingFirstGenerationIsDeterministic(t *testing.T) {
	ordered := buildExactStreamingEquality([]int{0, 1, 2, 3, 4, 5})
	shuffled := buildExactStreamingEquality([]int{4, 1, 5, 0, 3, 2})

	orderedUsage, orderedNext, ok := ordered.prepareStreamingFirstGeneration()
	require.True(t, ok)
	shuffledUsage, shuffledNext, ok := shuffled.prepareStreamingFirstGeneration()
	require.True(t, ok)
	require.Equal(t, orderedUsage, shuffledUsage)
	require.Equal(t,
		equalityGenerationShape(orderedNext.(*eqRule[streamingEqualityFixture, string])),
		equalityGenerationShape(shuffledNext.(*eqRule[streamingEqualityFixture, string])),
	)
}

func TestEqualityAdaptiveLeafPublishesOnlyPreparedGeneration(t *testing.T) {
	exact := buildExactStreamingEquality([]int{0, 1, 2, 3, 4, 5})
	adaptive := &streamingAdaptiveLeaf[streamingEqualityFixture]{child: exact}
	usage, ok := adaptive.nextStreamingUsage()
	require.True(t, ok)
	require.Positive(t, usage)
	require.IsType(t, exact, adaptive.child)

	adaptive.fitStreamingNext()
	require.IsType(t, &eqRule[streamingEqualityFixture, string]{}, adaptive.child)
	require.IsType(t, exact, adaptive.child, "identity and quantized generations use one rule type")
	lossy := adaptive.child.(*eqRule[streamingEqualityFixture, string])
	lossy.values.visit(func(key equalityPhysicalKey[string], _ *equalitySet) {
		require.True(t, key.bucketed)
		require.Empty(t, key.exact, "a bucket generation must not retain the exact value")
	})

	unsupported := &eqRule[streamingEqualityFixture, any]{
		wildcard: roaring.New(), values: newEqualityIndex[equalityPhysicalKey[any]](0),
	}
	_, _, ok = unsupported.prepareStreamingFirstGeneration()
	require.False(t, ok)
}

func TestEqualityPhysicalKeyPayloadsAreMutuallyExclusive(t *testing.T) {
	exact := exactEqualityKey("retained")
	require.False(t, exact.bucketed)
	require.Equal(t, "retained", exact.exact)
	require.Zero(t, exact.bucket)

	bucket := bucketEqualityKey[string](42)
	require.True(t, bucket.bucketed)
	require.Empty(t, bucket.exact)
	require.Equal(t, uint64(42), bucket.bucket)
}

func TestEqualityStreamingLateValuesAndRepeatedDowngradesKeepExactMatches(t *testing.T) {
	exact := buildExactStreamingEquality([]int{0, 1, 2, 3, 4, 5})
	_, next, ok := exact.prepareStreamingFirstGeneration()
	require.True(t, ok)
	lossy := next.(*eqRule[streamingEqualityFixture, string])

	late := streamingEqualityFixture{value: "late-value", present: true}
	lossy.insert(late, 100)
	for range 12 {
		lossy.fitStreamingNext()
	}
	for id, value := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		require.True(t, lossy.matchesID(streamingEqualityFixture{value: value, present: true}, uint32(id)))
	}
	require.True(t, lossy.matchesID(late, 100))
	require.True(t, lossy.matchesID(streamingEqualityFixture{value: "anything", present: true}, 99))
}
