package ruleix

import (
	"fmt"
	"iter"
	"math"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

// BuildIDChunkExperiment exposes the internal control to external-package
// production fixtures without making it part of the library API.
func BuildIDChunkExperiment[C any, ID comparable](
	schema Rule[C],
	entries iter.Seq2[C, ID],
	shift uint8,
) (*Index[C, ID], uint64, error) {
	index, _, err := buildIndexPhysicalAliases(
		Lossy(schema, MemoryLimit(math.MaxUint64)), entries, false, nil,
		buildOptions{compilePhysicalAliases: true, enableStreaming: true, identityLossy: true, idChunkShift: shift},
	)
	if err != nil {
		return nil, 0, err
	}
	retained, _ := currentLossyUsage(index.root)
	return index, retained, nil
}

type idChunkFixture struct {
	left  int
	right int
}

func idChunkSchema() Rule[idChunkFixture] {
	return Lossy(All(
		Include(func(value idChunkFixture) (int, bool) { return value.left, true }),
		Include(func(value idChunkFixture) (int, bool) { return value.right, true }),
	), MemoryLimit(math.MaxUint64))
}

func buildIDChunkFixture(t testing.TB, shift uint8, entries int) *Index[idChunkFixture, int] {
	t.Helper()
	constraints := make([]idChunkFixture, entries)
	ids := make([]int, entries)
	for id := range entries {
		constraints[id] = idChunkFixture{left: id % 17, right: id % 31}
		ids[id] = id
	}
	index, _, err := buildIndexPhysicalAliases(
		idChunkSchema(), Zip(constraints, ids), false, nil,
		buildOptions{compilePhysicalAliases: true, enableStreaming: true, identityLossy: true, idChunkShift: shift},
	)
	require.NoError(t, err)
	return index
}

func TestExperimentalIDChunkingExpandsConservativeResults(t *testing.T) {
	exact := buildIDChunkFixture(t, 0, 257)
	chunked := buildIDChunkFixture(t, 2, 257)
	query := idChunkFixture{left: 3, right: 3}

	var exactMatches, chunkedMatches []int
	exact.Search(query, &exactMatches)
	chunked.Search(query, &chunkedMatches)
	requireSupersetComparable(t, exactMatches, chunkedMatches)
	require.IsIncreasing(t, chunkedMatches)

	local := chunked.Local()
	defer local.Close()
	var localMatches []int
	local.Search(query, &localMatches)
	require.Equal(t, chunkedMatches, localMatches)

	var visited []int
	chunked.Visit(query, func(id int) bool {
		visited = append(visited, id)
		return len(visited) != 3
	})
	require.Equal(t, chunkedMatches[:3], visited)
}

func TestExperimentalIDChunkingRejectsExclusions(t *testing.T) {
	_, _, err := buildIndexPhysicalAliases(
		All(
			Include(func(value idChunkFixture) (int, bool) { return value.right, true }),
			Exclude(func(value idChunkFixture) (int, bool) { return value.left, true }),
		),
		Zip([]idChunkFixture{{left: 1}}, []int{1}), false, nil,
		buildOptions{compilePhysicalAliases: true, enableStreaming: true, identityLossy: true, idChunkShift: 1},
	)
	require.ErrorContains(t, err, "does not support exclusions")
}

func TestExperimentalIDChunkingValidationAndWideDecode(t *testing.T) {
	_, _, err := buildIndexPhysicalAliases(
		idChunkSchema(), Zip([]idChunkFixture{}, []int{}), false, nil,
		buildOptions{idChunkShift: 32},
	)
	require.ErrorContains(t, err, "below 32")

	values := make([]int, 10_000)
	bits := roaring.New()
	for id := range values {
		values[id] = id
		if id%2 == 0 {
			bits.Add(uint32(id / 2))
		}
	}
	result := appendChunkedBitmapValues(bits, values, 1, nil)
	require.Len(t, result, len(values))
	require.Equal(t, 0, result[0])
	require.Equal(t, len(values)-1, result[len(result)-1])

	var inspector Inspector
	constraints := []idChunkFixture{{left: 1, right: 1}, {left: 1, right: 2}}
	observed, _, observedErr := buildIndexPhysicalAliases(
		Inspect(&inspector, idChunkSchema()), Zip(constraints, []int{1, 2}), false, nil,
		buildOptions{compilePhysicalAliases: true, enableStreaming: true, identityLossy: true, idChunkShift: 1},
	)
	require.NoError(t, observedErr)
	var observedMatches []int
	require.True(t, observed.Search(idChunkFixture{left: 1, right: 1}, &observedMatches))
	require.Equal(t, []int{1, 2}, observedMatches)
}

// BenchmarkExperimentalIDChunking isolates ID-space coarsening from key
// quantization. Apple M1 Max, 100k unique IDs, 500ms x3: shift 0/1/2/3/4
// retained 401632/340512/340512/340512/340512 posting bytes and returned
// 190/760/3036/12152/48592 IDs; search was 9.1-9.8/5.7/7.8/13.1/27.4 us.
// The comparable command and interpretation live in docs/lossy-index.md.
func BenchmarkExperimentalIDChunking(b *testing.B) {
	const entries = 100_000
	query := idChunkFixture{left: 3, right: 3}
	for _, shift := range []uint8{0, 1, 2, 3, 4} {
		index := buildIDChunkFixture(b, shift, entries)
		retained, retainedAvailable := currentLossyUsage(index.root)
		require.True(b, retainedAvailable)
		var sample []int
		index.Search(query, &sample)
		b.Run(fmt.Sprintf("Shift%d", shift), func(b *testing.B) {
			b.ReportAllocs()
			b.ReportMetric(float64(len(sample)), "ids/op")
			b.ReportMetric(float64(retained), "posting-bytes")
			result := make([]int, 0, len(sample))
			for range b.N {
				result = result[:0]
				index.Search(query, &result)
			}
		})
	}
}
