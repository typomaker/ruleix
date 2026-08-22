package ruleix

import (
	"cmp"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalScalarIsStableAndTypeSeparated(t *testing.T) {
	t.Parallel()

	negativeZero := math.Copysign(0, -1)
	cases := []struct {
		left, right any
		equal       bool
	}{
		{left: "ruleix", right: "ruleix", equal: true},
		{left: int32(-1), right: int32(-1), equal: true},
		{left: uint32(1), right: uint32(1), equal: true},
		{left: negativeZero, right: float64(0), equal: true},
		{left: math.NaN(), right: math.Float64frombits(0x7ff0000000000001), equal: true},
		{left: int64(1), right: uint64(1), equal: false},
		{left: float32(1), right: float64(1), equal: false},
	}
	for _, tc := range cases {
		left, ok := canonicalScalar(nil, tc.left)
		require.True(t, ok)
		right, ok := canonicalScalar(nil, tc.right)
		require.True(t, ok)
		if tc.equal {
			require.Equal(t, left, right)
		} else {
			require.NotEqual(t, left, right)
		}
	}

	encoded, ok := canonicalScalar([]byte{0xaa}, "x")
	require.True(t, ok)
	require.Equal(t, byte(0xaa), encoded[0])
	_, ok = canonicalScalar(nil, struct{}{})
	require.False(t, ok)
}

func TestOrderedScalarKeyPreservesIntegerOrder(t *testing.T) {
	t.Parallel()

	assertOrderedKeys(t, []int8{math.MinInt8, -1, 0, 1, math.MaxInt8}, cmp.Compare[int8])
	assertOrderedKeys(t, []int16{math.MinInt16, -1, 0, 1, math.MaxInt16}, cmp.Compare[int16])
	assertOrderedKeys(t, []int32{math.MinInt32, -1, 0, 1, math.MaxInt32}, cmp.Compare[int32])
	assertOrderedKeys(t, []int64{math.MinInt64, -1, 0, 1, math.MaxInt64}, cmp.Compare[int64])
	assertOrderedKeys(t, []uint8{0, 1, math.MaxUint8}, cmp.Compare[uint8])
	assertOrderedKeys(t, []uint16{0, 1, math.MaxUint16}, cmp.Compare[uint16])
	assertOrderedKeys(t, []uint32{0, 1, math.MaxUint32}, cmp.Compare[uint32])
	assertOrderedKeys(t, []uint64{0, 1, math.MaxUint64}, cmp.Compare[uint64])
}

func TestOrderedScalarKeyPreservesFloatOrder(t *testing.T) {
	t.Parallel()

	assertOrderedKeys(t, []float32{
		float32(math.NaN()), float32(math.Inf(-1)), -math.MaxFloat32, -1,
		float32(math.Copysign(0, -1)), 0, 1, math.MaxFloat32, float32(math.Inf(1)),
	}, cmp.Compare[float32])
	assertOrderedKeys(t, []float64{
		math.NaN(), math.Inf(-1), -math.MaxFloat64, -1,
		math.Copysign(0, -1), 0, 1, math.MaxFloat64, math.Inf(1),
	}, cmp.Compare[float64])
}

func assertOrderedKeys[V any](t *testing.T, values []V, compare func(V, V) int) {
	t.Helper()
	for i := range values {
		left, ok := orderedScalarKey(values[i])
		require.True(t, ok)
		for j := range values {
			right, ok := orderedScalarKey(values[j])
			require.True(t, ok)
			switch result := compare(values[i], values[j]); {
			case result < 0:
				require.Less(t, left, right, "values[%d], values[%d]", i, j)
			case result > 0:
				require.Greater(t, left, right, "values[%d], values[%d]", i, j)
			default:
				require.Equal(t, left, right, "values[%d], values[%d]", i, j)
			}
		}
	}
}

func TestOrderedScalarKeyRejectsUnsupportedTypes(t *testing.T) {
	t.Parallel()

	_, ok := orderedScalarKey("not ordered by this encoder")
	require.False(t, ok)
}
