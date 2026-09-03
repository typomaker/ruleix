package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

func requireProductionIDSuperset(
	t testing.TB,
	exact, approximate []productionBenchmarkID,
) {
	t.Helper()
	available := make(map[productionBenchmarkID]struct{}, len(approximate))
	for _, id := range approximate {
		available[id] = struct{}{}
	}
	for _, id := range exact {
		_, found := available[id]
		require.True(t, found, "chunked result omitted exact ID %x", id)
	}
}

func requireProductionIDEqual(t testing.TB, expected, actual []productionBenchmarkID) {
	t.Helper()
	require.Len(t, actual, len(expected))
	for index := range expected {
		require.Equal(t, expected[index], actual[index], "result differs at index %d", index)
	}
}

func TestProductionShapeIDChunkingIsConservativeAndStable(t *testing.T) {
	constraints, ids := productionBenchmarkData()
	queries := []productionBenchmarkConstraint{productionBenchmarkQuery(100), productionBenchmarkQuery(101)}
	for _, schema := range []ruleix.Rule[productionBenchmarkConstraint]{
		productionBenchmarkSchema(), productionBenchmarkEqualityOnlySchema(),
	} {
		exact, _, err := ruleix.BuildIDChunkExperiment(schema, ruleix.Zip(constraints, ids), 0)
		require.NoError(t, err)
		for _, shift := range []uint8{1, 2, 3} {
			chunked, _, buildErr := ruleix.BuildIDChunkExperiment(schema, ruleix.Zip(constraints, ids), shift)
			require.NoError(t, buildErr)
			local := chunked.Local()
			for _, query := range queries {
				var exactMatches, indexMatches, localMatches []productionBenchmarkID
				exact.Search(query, &exactMatches)
				chunked.Search(query, &indexMatches)
				local.Search(query, &localMatches)
				requireProductionIDEqual(t, indexMatches, localMatches)
				requireProductionIDSuperset(t, exactMatches, indexMatches)
			}
			local.Close()
		}
	}
}

// BenchmarkProductionShapeIDChunking measures only ID-space coarsening: the
// lossy key ladder is held at its exact identity level. Apple M1 Max,
// GOMAXPROCS=1, 38,098 IDs, 500ms x3: full shift 0/1/2/3 retained
// 567590/547648/505446/477808 bytes and returned 45/90/194/446 candidates;
// Index medians were 35.1/119.9/81.3/67.0 us. Full results and the comparable
// invocation are recorded in docs/lossy-id-chunk-experiment.md.
func BenchmarkProductionShapeIDChunking(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for _, shape := range []struct {
		name   string
		schema ruleix.Rule[productionBenchmarkConstraint]
	}{
		{name: "Full", schema: productionBenchmarkSchema()},
		{name: "EqualityOnly", schema: productionBenchmarkEqualityOnlySchema()},
	} {
		exact, _, err := ruleix.BuildIDChunkExperiment(
			shape.schema, ruleix.Zip(constraints, ids), 0,
		)
		if err != nil {
			b.Fatal(err)
		}
		for _, shift := range []uint8{0, 1, 2, 3} {
			index, retained, err := ruleix.BuildIDChunkExperiment(
				shape.schema, ruleix.Zip(constraints, ids), shift,
			)
			if err != nil {
				b.Fatal(err)
			}
			candidateTotal := 0
			validationLocal := index.Local()
			for _, query := range queries {
				var exactMatches, indexMatches, localMatches []productionBenchmarkID
				exact.Search(query, &exactMatches)
				index.Search(query, &indexMatches)
				validationLocal.Search(query, &localMatches)
				requireProductionIDEqual(b, indexMatches, localMatches)
				requireProductionIDSuperset(b, exactMatches, indexMatches)
				candidateTotal += len(indexMatches)
			}
			validationLocal.Close()
			for _, local := range []bool{false, true} {
				mode := "Index"
				if local {
					mode = "Local"
				}
				b.Run(fmt.Sprintf("%s/Shift%d/%s", shape.name, shift, mode), func(b *testing.B) {
					searcher := index.Local()
					b.Cleanup(searcher.Close)
					matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
					b.ReportAllocs()
					b.ResetTimer()
					for iteration := range b.N {
						matches = matches[:0]
						query := queries[iteration%len(queries)]
						if local {
							searcher.Search(query, &matches)
						} else {
							index.Search(query, &matches)
						}
					}
					b.ReportMetric(float64(candidateTotal)/float64(len(queries)), "candidates/op")
					b.ReportMetric(float64(retained), "posting-bytes")
				})
			}
		}
	}
}
