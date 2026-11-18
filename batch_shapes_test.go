package ruleix

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Screening uses Go 1.27.3 / M1 Max / GOMAXPROCS=1 and includes Local
// acquisition, batch analysis and Close. See docs/search-batch.md for final
// results and the measured single-offer fallback threshold.
func BenchmarkBatchShapes(b *testing.B) {
	for _, candidates := range []int{1, 8, 16, 32, 64, 65, 256, 4096} {
		b.Run(fmt.Sprint(candidates), func(b *testing.B) {
			rows, ids := make([]batchFixture, 4*candidates), make([]int, 4*candidates)
			for i := range rows {
				rows[i] = batchFixture{group: i / candidates, from: i % 11, until: 20 + i%13, missing: 2 | 4}
				ids[i] = i * 17
			}
			index, err := New[batchFixture, int](batchSchema(false)).Build(Zip(rows, ids))
			require.NoError(b, err)
			for _, mode := range []string{"common", "diverse", "empty"} {
				b.Run(mode, func(b *testing.B) {
					for _, length := range []int{1, 2, 4, 16, 100} {
						b.Run(fmt.Sprint(length), func(b *testing.B) {
							for _, strategy := range []string{"Local", "Auto"} {
								b.Run(strategy, func(b *testing.B) {
									benchmarkBatchShapeCase(b, index, mode, length, strategy)
								})
							}
						})
					}
				})
			}
		})
	}
}

func benchmarkBatchShapeCase(b *testing.B, index *Index[batchFixture, int], mode string, length int, strategy string) {
	queries, found := make([]batchFixture, length), make([][]int, length)
	for i := range queries {
		queries[i] = batchFixture{group: 1, from: 15 + i%10, until: 15 + i%10}
		if mode == "diverse" {
			queries[i].group = i % 4
		}
		if mode == "empty" {
			queries[i].from, queries[i].until = 200, 200
		}
	}
	want := make([][]int, length)
	local := index.TestLocal()
	for i, query := range queries {
		local.SearchOne(query, &want[i])
	}
	local.Close()
	index.Search(queries, found)
	require.Equal(b, want, found)
	if mode == "empty" {
		for _, result := range want {
			require.Empty(b, result)
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		for i := range found {
			found[i] = found[i][:0]
		}
		if strategy == "Auto" {
			index.Search(queries, found)
		} else {
			local := index.TestLocal()
			for i, query := range queries {
				local.SearchOne(query, &found[i])
			}
			local.Close()
		}
	}
}
