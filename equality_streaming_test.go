package ruleix

import (
	"slices"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type streamingEqualityFixture struct {
	value   string
	present bool
}

func streamingEqualityValue(v streamingEqualityFixture) (string, bool) { return v.value, v.present }

func buildExactStreamingEquality(order []int) *eqRule[streamingEqualityFixture, string, string] {
	codec, err := compileEqualityCodec[string]()
	if err != nil {
		panic(err)
	}
	rule := &eqRule[streamingEqualityFixture, string, string]{
		get: streamingEqualityValue, wildcard: roaring.New(), codec: codec,
		encode:          func(value string, _ equalityQuantizer) string { return value },
		firstGeneration: prepareExactEqualityFirstGeneration[streamingEqualityFixture, string],
		values:          newEqualityIndex[string](len(order)),
	}
	values := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	for _, id := range order {
		rule.insert(streamingEqualityFixture{value: values[id], present: true}, uint32(id))
		rule.insert(streamingEqualityFixture{value: values[id], present: true}, uint32(id))
	}
	rule.insert(streamingEqualityFixture{}, 99)
	return rule
}

func TestEqualityModeBelongsToBuildPolicy(t *testing.T) {
	rule := buildExactStreamingEquality([]int{0, 1, 2})
	_, ruleSelectsMode := any(rule).(inspectionModer)
	require.False(t, ruleSelectsMode)

	adaptive := &streamingAdaptiveLeaf[streamingEqualityFixture]{child: rule}
	require.Equal(t, RuleModeExact, lossyPolicyMode[streamingEqualityFixture](adaptive))
	adaptive.fitStreamingNext()
	require.Equal(t, RuleModeLossy, lossyPolicyMode[streamingEqualityFixture](adaptive))
}

func equalityGenerationShape(rule *eqRule[streamingEqualityFixture, string, uint64]) []uint64 {
	keys := make([]uint64, 0, len(rule.values.sets))
	rule.values.visit(func(key uint64, _ *equalitySet) { keys = append(keys, key) })
	slices.Sort(keys)
	return keys
}

func TestEqualityLevelZeroStoresComparableValue(t *testing.T) {
	rule := buildExactStreamingEquality([]int{0})

	require.Equal(t, uint32(0), rule.quantizer.level)
	require.NotNil(t, rule.values.get("alpha"))
}

func TestEqualityLevelZeroDoesNotHash(t *testing.T) {
	rule := buildExactStreamingEquality(nil)
	rule.codec.hash = func(string) uint64 { panic("level zero hashed a value") }
	value := streamingEqualityFixture{value: "alpha", present: true}
	rule.insert(value, 7)

	require.Equal(t, uint64(2), rule.estimateCardinality(value))
	require.True(t, rule.matchesID(value, 7))
	result := roaring.New()
	rule.search(value, result, newBitmapPool())
	require.Equal(t, []uint32{7, 99}, result.ToArray())
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
		equalityGenerationShape(orderedNext.(*eqRule[streamingEqualityFixture, string, uint64])),
		equalityGenerationShape(shuffledNext.(*eqRule[streamingEqualityFixture, string, uint64])),
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
	require.IsType(t, &eqRule[streamingEqualityFixture, string, uint64]{}, adaptive.child)
	lossy := adaptive.child.(*eqRule[streamingEqualityFixture, string, uint64])
	require.Equal(t, uint32(1), lossy.quantizer.level)

	unsupported := &eqRule[streamingEqualityFixture, any, any]{
		wildcard: roaring.New(), values: newEqualityIndex[any](0), codecErr: assert.AnError,
	}
	_, _, ok = unsupported.prepareStreamingFirstGeneration()
	require.False(t, ok)
}

func TestEqualityStreamingLateValuesAndRepeatedDowngradesKeepExactMatches(t *testing.T) {
	exact := buildExactStreamingEquality([]int{0, 1, 2, 3, 4, 5})
	_, next, ok := exact.prepareStreamingFirstGeneration()
	require.True(t, ok)
	lossy := next.(*eqRule[streamingEqualityFixture, string, uint64])

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
