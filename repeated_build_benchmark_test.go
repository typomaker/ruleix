//nolint:lll // Migration benchmark keeps legacy pointer getters inline.
package ruleix

import (
	"cmp"
	"runtime"
	"testing"
)

const repeatedBuildEntries = 10_000

type repeatedBuildConstraint struct {
	equality *int
	ordered  *int
}

type repeatedBuildData struct {
	constraints []repeatedBuildConstraint
	ids         []int
}

func repeatedBuildSchema() Rule[repeatedBuildConstraint] {
	return All(
		Include(GetterFromPointer(func(value repeatedBuildConstraint) *int { return value.equality })),
		GreaterOrEqual(GetterFromPointer(func(value repeatedBuildConstraint) *int { return value.ordered }), cmp.Compare[int]),
	)
}

func makeRepeatedBuildData(entries, idsPerEquality int, repeatedIDs, randomOrder bool) repeatedBuildData {
	values := make([]int, entries*2)
	data := repeatedBuildData{
		constraints: make([]repeatedBuildConstraint, entries),
		ids:         make([]int, entries),
	}
	order := make([]int, entries)
	for i := range order {
		order[i] = i
	}
	if randomOrder {
		// A fixed permutation keeps runs comparable without making benchmark
		// setup depend on the global pseudo-random source.
		var seed uint64 = 0x9e3779b97f4a7c15
		for i := entries - 1; i > 0; i-- {
			seed = seed*6364136223846793005 + 1
			j := int(seed % uint64(i+1))
			order[i], order[j] = order[j], order[i]
		}
	}
	uniqueIDs := entries
	if repeatedIDs {
		uniqueIDs = entries / 10
		if uniqueIDs == 0 {
			uniqueIDs = 1
		}
	}
	for position, source := range order {
		values[position*2] = source / idsPerEquality
		values[position*2+1] = source
		data.constraints[position] = repeatedBuildConstraint{
			equality: &values[position*2],
			ordered:  &values[position*2+1],
		}
		data.ids[position] = source % uniqueIDs
	}
	return data
}

func (data repeatedBuildData) entries(yield func(repeatedBuildConstraint, int) bool) {
	for i := range data.constraints {
		if !yield(data.constraints[i], data.ids[i]) {
			return
		}
	}
}

type repeatedBuildHintMode uint8

const (
	repeatedBuildOneShot repeatedBuildHintMode = iota
	repeatedBuildGlobal
	repeatedBuildPerNode
)

func runRepeatedBuild(
	schema Rule[repeatedBuildConstraint],
	data repeatedBuildData,
	mode repeatedBuildHintMode,
	history *buildStatistics,
) (*Index[repeatedBuildConstraint, int], error) {
	if mode == repeatedBuildOneShot {
		index, _, err := buildIndex[repeatedBuildConstraint, int](schema, data.entries, false, nil)
		return index, err
	}
	hints := *history
	if mode == repeatedBuildGlobal {
		hints.nodes = nil
	}
	index, measured, err := buildIndex[repeatedBuildConstraint, int](schema, data.entries, true, &hints)
	if err == nil {
		*history = measured
	}
	return index, err
}

func BenchmarkRepeatedBuild(b *testing.B) {
	type workload struct {
		name           string
		sizes          []int
		card           int
		repeat, random bool
	}
	workloads := []workload{
		{
			name:  "Stable/UniqueIDs/LowEqualityCardinality/Sequential",
			sizes: []int{repeatedBuildEntries, repeatedBuildEntries},
			card:  1,
		},
		{
			name:   "Stable/RepeatedIDs/HighEqualityCardinality/Random",
			sizes:  []int{repeatedBuildEntries, repeatedBuildEntries},
			card:   100,
			repeat: true,
			random: true,
		},
		{name: "Grow5Percent", sizes: []int{repeatedBuildEntries, repeatedBuildEntries * 105 / 100}, card: 10},
		{name: "Grow25Percent", sizes: []int{repeatedBuildEntries, repeatedBuildEntries * 125 / 100}, card: 10},
		{name: "Grow50Percent", sizes: []int{repeatedBuildEntries, repeatedBuildEntries * 150 / 100}, card: 10},
		{name: "Shrink5Percent", sizes: []int{repeatedBuildEntries, repeatedBuildEntries * 95 / 100}, card: 10},
		{name: "Shrink25Percent", sizes: []int{repeatedBuildEntries, repeatedBuildEntries * 75 / 100}, card: 10},
		{name: "Shrink50Percent", sizes: []int{repeatedBuildEntries, repeatedBuildEntries / 2}, card: 10},
		{
			name:  "LargeSmallSmall",
			sizes: []int{repeatedBuildEntries, repeatedBuildEntries / 2, repeatedBuildEntries / 2},
			card:  10,
		},
	}
	for _, workload := range workloads {
		data := make([]repeatedBuildData, len(workload.sizes))
		for i, size := range workload.sizes {
			data[i] = makeRepeatedBuildData(size, workload.card, workload.repeat, workload.random)
		}
		for _, mode := range []struct {
			name string
			mode repeatedBuildHintMode
		}{{"OneShot", repeatedBuildOneShot}, {"GlobalHints", repeatedBuildGlobal}, {"PerNodeHints", repeatedBuildPerNode}} {
			b.Run(workload.name+"/"+mode.name, func(b *testing.B) {
				schema := repeatedBuildSchema()
				b.ReportAllocs()
				for range b.N {
					var history buildStatistics
					var previous *Index[repeatedBuildConstraint, int]
					for _, input := range data {
						index, err := runRepeatedBuild(schema, input, mode.mode, &history)
						if err != nil {
							b.Fatal(err)
						}
						runtime.KeepAlive(previous) // measure overlapping old and new indexes
						previous = index
					}
					runtime.KeepAlive(previous)
				}
				b.ReportMetric(float64(len(data)), "builds/op")
			})
		}
	}
}

func BenchmarkRepeatedBuildMemory(b *testing.B) {
	schema := repeatedBuildSchema()
	large := makeRepeatedBuildData(repeatedBuildEntries, 10, false, true)
	small := makeRepeatedBuildData(repeatedBuildEntries/2, 10, false, true)
	for _, mode := range []struct {
		name string
		mode repeatedBuildHintMode
	}{{"OneShot", repeatedBuildOneShot}, {"GlobalHints", repeatedBuildGlobal}, {"PerNodeHints", repeatedBuildPerNode}} {
		b.Run(mode.name, func(b *testing.B) {
			var peakTotal, retainedTotal uint64
			for range b.N {
				runtime.GC()
				var before, peak, after runtime.MemStats
				runtime.ReadMemStats(&before)
				var history buildStatistics
				previous, err := runRepeatedBuild(schema, large, mode.mode, &history)
				if err != nil {
					b.Fatal(err)
				}
				current, err := runRepeatedBuild(schema, small, mode.mode, &history)
				if err != nil {
					b.Fatal(err)
				}
				runtime.ReadMemStats(&peak)
				runtime.KeepAlive(previous)
				runtime.KeepAlive(current)
				runtime.GC()
				runtime.ReadMemStats(&after)
				if peak.HeapAlloc > before.HeapAlloc {
					peakTotal += peak.HeapAlloc - before.HeapAlloc
				}
				if after.HeapAlloc > before.HeapAlloc {
					retainedTotal += after.HeapAlloc - before.HeapAlloc
				}
			}
			b.ReportMetric(float64(peakTotal)/float64(b.N), "peak-live-bytes/op")
			b.ReportMetric(float64(retainedTotal)/float64(b.N), "retained-bytes/op")
		})
	}
}
