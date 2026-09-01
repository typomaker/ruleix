package ruleix

import (
	"cmp"
	"fmt"
	"iter"
	"runtime"
	"runtime/debug"
	"testing"
)

const lossyAllBenchmarkEntries = 10_000

type lossyAllBenchmarkConstraint struct {
	values     [16]string
	thresholds [16]int
	present    [16]bool
}

func lossyAllOrderedBenchmarkSchema(children int) Rule[lossyAllBenchmarkConstraint] {
	rules := make([]Rule[lossyAllBenchmarkConstraint], children)
	for field := range rules {
		field := field
		rules[field] = GreaterOrEqual(func(value lossyAllBenchmarkConstraint) (int, bool) {
			return value.thresholds[field], value.present[field]
		}, cmp.Compare[int])
	}
	return All(rules...)
}

var lossyAllBenchmarkIndex *Index[lossyAllBenchmarkConstraint, int]

func lossyAllBenchmarkData(entries int) ([]lossyAllBenchmarkConstraint, []int) {
	constraints := make([]lossyAllBenchmarkConstraint, entries)
	ids := make([]int, entries)
	for i := range constraints {
		ids[i] = i
		for field := range constraints[i].values {
			// Coprime multipliers keep each leaf's distribution distinct while
			// retaining enough overlap to exercise composite false positives.
			constraints[i].values[field] = fmt.Sprintf("f%d-v%d", field, (i*(field*2+3)+field)%entries)
			constraints[i].thresholds[field] = (i*(field*2+3) + field) % entries
			constraints[i].present[field] = true
		}
	}
	return constraints, ids
}

func lossyAllBenchmarkSchema(children int) Rule[lossyAllBenchmarkConstraint] {
	rules := make([]Rule[lossyAllBenchmarkConstraint], children)
	for field := range rules {
		field := field
		rules[field] = Include(func(value lossyAllBenchmarkConstraint) (string, bool) {
			return value.values[field], value.present[field]
		})
	}
	return All(rules...)
}

type lossySelectionBenchmarkDistribution string

const (
	lossySelectionBalanced    lossySelectionBenchmarkDistribution = "Balanced"
	lossySelectionSingleHeavy lossySelectionBenchmarkDistribution = "SingleHeavy"
)

func lossySelectionBenchmarkData(
	entries, children int,
	distribution lossySelectionBenchmarkDistribution,
) ([]lossyAllBenchmarkConstraint, []int) {
	constraints := make([]lossyAllBenchmarkConstraint, entries)
	ids := make([]int, entries)
	for row := range constraints {
		ids[row] = row
		for field := range children {
			distinct := entries
			if distribution == lossySelectionSingleHeavy && field != 0 {
				distinct = 16
			}
			value := (row*(field*2+3) + field) % distinct
			constraints[row].values[field] = fmt.Sprintf("f%d-v%d", field, value)
			constraints[row].thresholds[field] = value
			constraints[row].present[field] = true
		}
	}
	return constraints, ids
}

func lossySelectionBenchmarkSchema(
	operator string,
	children int,
	inspectors []Inspector,
) Rule[lossyAllBenchmarkConstraint] {
	rules := make([]Rule[lossyAllBenchmarkConstraint], children)
	for field := range rules {
		field := field
		var rule Rule[lossyAllBenchmarkConstraint]
		ordered := operator == "Ordered" || operator == "Mixed" && field%2 == 1
		if ordered {
			rule = GreaterOrEqual(func(value lossyAllBenchmarkConstraint) (int, bool) {
				return value.thresholds[field], value.present[field]
			}, cmp.Compare[int])
		} else {
			rule = Include(func(value lossyAllBenchmarkConstraint) (string, bool) {
				return value.values[field], value.present[field]
			})
		}
		if inspectors != nil {
			rule = Inspect(&inspectors[field], rule)
		}
		rules[field] = rule
	}
	return All(rules...)
}

func lossySelectionBenchmarkMinimum(
	b testing.TB,
	constraints []lossyAllBenchmarkConstraint,
	schema Rule[lossyAllBenchmarkConstraint],
) uint64 {
	b.Helper()
	state := schema.newState(&nodeIDAllocator{}, &buildStatistics{})
	for id, constraint := range constraints {
		state.insert(constraint, uint32(id))
	}
	leaves := make([]lossyAllLeaf[lossyAllBenchmarkConstraint], 0, 16)
	if err := collectLossyAllLeaves(state, &leaves); err != nil {
		b.Fatal(err)
	}
	var minimum uint64
	for _, leaf := range leaves {
		ladder, err := leaf.planner.representationLadder()
		if err != nil {
			b.Fatal(err)
		}
		minimum, _ = addLossyMemory(minimum, ladder[len(ladder)-1].details.MemoryUsageBytes)
	}
	return minimum
}

// BenchmarkLossySelectionMatrix measures the complete selective-planning gate.
// Latest local result (Apple M1 Max, Go 1.26.0, 10k entries, one iteration):
// 120 cases took 15.3--779.1 ms/build with at most 188.1 MB peak-live memory.
// The 16-child single-heavy equality 50% case retained 15 exact leaves and
// measured 5.859 candidates/query at 0.000486 observed false-positive rate.
// See ROADMAP_HISTORY.md for the proportional-parent comparison.
//
// Reproduce:
//
//	go test -run '^$' -bench '^BenchmarkLossySelectionMatrix/' -benchmem -benchtime=1x -count=1 .
//
//nolint:gocognit // Keeping the dimensions together makes benchmark names reproducible.
func BenchmarkLossySelectionMatrix(b *testing.B) {
	const entries = lossyAllBenchmarkEntries
	for _, children := range []int{2, 4, 8, 16} {
		distributions := []lossySelectionBenchmarkDistribution{
			lossySelectionBalanced,
			lossySelectionSingleHeavy,
		}
		for _, distribution := range distributions {
			constraints, ids := lossySelectionBenchmarkData(entries, children, distribution)
			queries := constraints[:64]
			for _, operator := range []string{"Equality", "Ordered", "Mixed"} {
				exactSchema := lossySelectionBenchmarkSchema(operator, children, nil)
				exactBytes := lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, exactSchema)
				minimumBytes := lossySelectionBenchmarkMinimum(b, constraints, exactSchema)
				exactIndex, err := New[lossyAllBenchmarkConstraint, int](exactSchema).Build(Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				var exactMatches []int
				var exactTotal uint64
				for _, query := range queries {
					exactMatches = exactMatches[:0]
					exactIndex.Search(query, &exactMatches)
					exactTotal += uint64(len(exactMatches))
				}
				budgets := []struct {
					name  string
					limit uint64
				}{
					{name: "BelowExact", limit: exactBytes - 1},
					{name: "Budget75", limit: exactBytes * 75 / 100},
					{name: "Budget50", limit: exactBytes * 50 / 100},
					{name: "Budget25", limit: max(exactBytes*25/100, minimumBytes)},
					{name: "Minimum", limit: minimumBytes},
				}
				for _, budget := range budgets {
					name := fmt.Sprintf("Children%d/%s/%s/%s", children, distribution, operator, budget.name)
					b.Run(name, func(b *testing.B) {
						inspectors := make([]Inspector, children)
						var aggregate Inspector
						schema := Inspect(
							&aggregate,
							Lossy(
								lossySelectionBenchmarkSchema(operator, children, inspectors),
								MemoryLimit(budget.limit),
							),
						)

						runtime.GC()
						var before, after runtime.MemStats
						runtime.ReadMemStats(&before)
						previousGC := debug.SetGCPercent(-1)
						probe, err := New[lossyAllBenchmarkConstraint, int](schema).Build(Zip(constraints, ids))
						debug.SetGCPercent(previousGC)
						runtime.ReadMemStats(&after)
						if err != nil {
							b.Fatal(err)
						}

						accounted, ok := aggregate.Snapshot().MemoryUsage()
						if !ok || accounted > budget.limit {
							b.Fatalf("accounted bytes %d exceed limit %d", accounted, budget.limit)
						}
						downgraded := 0
						for i := range inspectors {
							if inspectors[i].Snapshot().Mode() == RuleModeLossy {
								downgraded++
							}
						}
						var matches []int
						var totalMatches uint64
						for _, query := range queries {
							matches = matches[:0]
							probe.Search(query, &matches)
							totalMatches += uint64(len(matches))
						}
						falsePositives := totalMatches - exactTotal
						possibleFalsePositives := uint64(len(queries)*entries) - exactTotal
						peak := uint64(0)
						if after.HeapAlloc > before.HeapAlloc {
							peak = after.HeapAlloc - before.HeapAlloc
						}

						builder := New[lossyAllBenchmarkConstraint, int](schema)
						b.ReportAllocs()
						b.ResetTimer()
						for range b.N {
							index, buildErr := builder.Build(Zip(constraints, ids))
							if buildErr != nil {
								b.Fatal(buildErr)
							}
							lossyAllBenchmarkIndex = index
						}
						b.ReportMetric(float64(accounted), "accounted-B/index")
						b.ReportMetric(float64(downgraded), "downgraded-leaves")
						b.ReportMetric(float64(peak), "peak-live-B/build")
						b.ReportMetric(float64(totalMatches)/float64(len(queries)), "candidates/query")
						if possibleFalsePositives != 0 {
							b.ReportMetric(
								float64(falsePositives)/float64(possibleFalsePositives),
								"false-positive-rate",
							)
						}
					})
				}
			}
		}
	}
}

func BenchmarkLossyAllPlanning(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	for _, children := range []int{2, 4, 8} {
		exactBytes := lossyAllBenchmarkExactBytes(b, constraints, ids, children)
		for _, tc := range []struct {
			name    string
			percent uint64
		}{
			{name: "Exact", percent: 100},
			{name: "Budget50", percent: 50},
			{name: "Budget25", percent: 25},
		} {
			b.Run(fmt.Sprintf("Children%d/%s", children, tc.name), func(b *testing.B) {
				limit := exactBytes * tc.percent / 100
				schema := lossyAllBenchmarkSchema(children)
				if tc.percent != 100 {
					schema = Lossy(schema, MemoryLimit(limit))
				}
				builder := New[lossyAllBenchmarkConstraint, int](schema)
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					index, err := builder.Build(Zip(constraints, ids))
					if err != nil {
						b.Fatal(err)
					}
					lossyAllBenchmarkIndex = index
				}
				b.ReportMetric(float64(limit), "limit-bytes")
			})
		}
	}
}

func BenchmarkLossyAllOrderedPlanning(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	for _, children := range []int{2, 4} {
		exactBytes := lossyAllBenchmarkExactBytesForSchema(
			b,
			constraints,
			ids,
			lossyAllOrderedBenchmarkSchema(children),
		)
		b.Run(fmt.Sprintf("Children%d/Budget75", children), func(b *testing.B) {
			limit := exactBytes * 3 / 4
			builder := New[lossyAllBenchmarkConstraint, int](
				Lossy(lossyAllOrderedBenchmarkSchema(children), MemoryLimit(limit)),
			)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				index, err := builder.Build(Zip(constraints, ids))
				if err != nil {
					b.Fatal(err)
				}
				lossyAllBenchmarkIndex = index
			}
			b.ReportMetric(float64(limit), "limit-bytes")
		})
	}
}

func BenchmarkLossyAllSearchQuality(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	const children = 4
	exactBytes := lossyAllBenchmarkExactBytes(b, constraints, ids, children)
	for _, tc := range []struct {
		name    string
		percent uint64
	}{
		{name: "Exact", percent: 100},
		{name: "Budget50", percent: 50},
		{name: "Budget25", percent: 25},
	} {
		b.Run(tc.name, func(b *testing.B) {
			schema := lossyAllBenchmarkSchema(children)
			if tc.percent != 100 {
				schema = Lossy(schema, MemoryLimit(exactBytes*tc.percent/100))
			}
			index, err := New[lossyAllBenchmarkConstraint, int](schema).Build(Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			queries := constraints[:256]
			var matches []int
			var totalMatches uint64
			for _, query := range queries {
				matches = matches[:0]
				index.Search(query, &matches)
				totalMatches += uint64(len(matches))
			}
			exactMatches := uint64(len(queries))
			falsePositives := totalMatches - exactMatches
			possibleFalsePositives := uint64(len(queries)) * uint64(len(constraints)-1)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				matches = matches[:0]
				index.Search(queries[i%len(queries)], &matches)
			}
			b.ReportMetric(float64(totalMatches)/float64(len(queries)), "candidates/query")
			b.ReportMetric(float64(falsePositives)/float64(possibleFalsePositives), "false-positive-rate")
			lossyAllBenchmarkIndex = index
		})
	}
}

// BenchmarkLossyAllSearchRuntime isolates the Index and Local search paths for
// CPU and allocation profiles. On Apple M1 Max with Go 1.26.0, 10k entries,
// four equality children, either one repeated or 256 rotating queries, and the
// command below, Exact measured IndexRepeated 502-506 ns/op, 32 B/op,
// 2 allocs/op; LocalRepeated 355-361 ns/op, 0 B/op, 0 allocs/op;
// IndexRotating 514-520 ns/op, 32 B/op, 2 allocs/op; and LocalRotating
// 542-550 ns/op, 32 B/op, 2 allocs/op. Budget50 measured IndexRepeated
// 241-242 ns/op, 0 B/op, 0 allocs/op; LocalRepeated 247-255 ns/op, 0 B/op,
// 0 allocs/op; IndexRotating 369-374 ns/op, 0 B/op, 0 allocs/op; and
// LocalRotating 477-481 ns/op, 0 B/op, 0 allocs/op. Budget25 measured
// IndexRepeated 289-291 ns/op, 0 B/op, 0 allocs/op; LocalRepeated
// 319-322 ns/op, 0 B/op, 0 allocs/op; IndexRotating 440-444 ns/op, 4 B/op,
// 0 allocs/op; and LocalRotating 515-519 ns/op, 5 B/op, 0 allocs/op.
//
//	go test -run '^$' -bench '^BenchmarkLossyAllSearchRuntime/' -benchmem -benchtime=1s -count=3 .
func BenchmarkLossyAllSearchRuntime(b *testing.B) {
	constraints, ids := lossyAllBenchmarkData(lossyAllBenchmarkEntries)
	const children = 4
	exactBytes := lossyAllBenchmarkExactBytes(b, constraints, ids, children)
	queries := constraints[:256]
	for _, percent := range []uint64{100, 50, 25} {
		b.Run(fmt.Sprintf("Budget%d", percent), func(b *testing.B) {
			schema := lossyAllBenchmarkSchema(children)
			if percent != 100 {
				schema = Lossy(schema, MemoryLimit(exactBytes*percent/100))
			}
			index, err := New[lossyAllBenchmarkConstraint, int](schema).Build(Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			for _, test := range []struct {
				name     string
				local    bool
				rotating bool
			}{
				{name: "IndexRepeated"},
				{name: "LocalRepeated", local: true},
				{name: "IndexRotating", rotating: true},
				{name: "LocalRotating", local: true, rotating: true},
			} {
				b.Run(test.name, func(b *testing.B) {
					searcher := index.Local()
					defer searcher.Close()
					var matches []int
					b.ReportAllocs()
					b.ResetTimer()
					for i := range b.N {
						matches = matches[:0]
						query := queries[0]
						if test.rotating {
							query = queries[i%len(queries)]
						}
						if test.local {
							searcher.Search(query, &matches)
						} else {
							index.Search(query, &matches)
						}
					}
				})
			}
			lossyAllBenchmarkIndex = index
		})
	}
}

// BenchmarkLossyScalePlanning extends the lossy build and retained-accounting
// baseline to the production scale points required by docs/lossy-index.md.
// Run with a fixed iteration count when comparing the larger cases:
//
//	go test -run '^$' -bench '^BenchmarkLossyScalePlanning/' -benchmem -benchtime=1x -count=3 .
func BenchmarkLossyScalePlanning(b *testing.B) {
	for _, entries := range []int{10_000, 100_000, 1_000_000} {
		constraints, ids := lossyAllBenchmarkData(entries)
		for _, operator := range []struct {
			name   string
			schema Rule[lossyAllBenchmarkConstraint]
		}{
			{name: "Equality", schema: lossyAllBenchmarkSchema(4)},
			{name: "Ordered", schema: lossyAllOrderedBenchmarkSchema(4)},
		} {
			exactBytes := lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, operator.schema)
			for _, percent := range []uint64{100, 50, 25} {
				b.Run(fmt.Sprintf("Entries%d/%s/Budget%d", entries, operator.name, percent), func(b *testing.B) {
					limit := exactBytes * percent / 100
					schema := operator.schema
					if percent != 100 {
						schema = Lossy(schema, MemoryLimit(limit))
					}
					accountedBytes := exactBytes
					if percent != 100 {
						accountedBytes = lossyAllBenchmarkAccountedBytes(b, constraints, ids, schema)
					}
					builder := New[lossyAllBenchmarkConstraint, int](schema)
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						index, err := builder.Build(Zip(constraints, ids))
						if err != nil {
							b.Fatal(err)
						}
						lossyAllBenchmarkIndex = index
					}
					b.ReportMetric(float64(entries), "rules/op")
					b.ReportMetric(float64(accountedBytes), "accounted-B/index")
				})
			}
		}
	}
}

// BenchmarkLossyStreamingBuild isolates the cost of consuming large
// constraints. Build must index each yielded value before the iterator resumes
// instead of retaining a second full copy until planning begins.
func BenchmarkLossyStreamingBuild(b *testing.B) {
	type constraint struct {
		value   uint64
		padding [248]byte
	}
	const entries = 100_000
	get := func(value constraint) (uint64, bool) { return value.value, true }
	b.ReportAllocs()
	b.ReportMetric(float64(entries), "entries/build")
	b.ResetTimer()
	for range b.N {
		index, err := New[constraint, int](
			Lossy(Include(get), MemoryLimit(1<<20)),
		).Build(func(yield func(constraint, int) bool) {
			for id := range entries {
				if !yield(constraint{value: uint64(id)}, id) {
					return
				}
			}
		})
		if err != nil {
			b.Fatal(err)
		}
		if len(index.values) != entries {
			b.Fatalf("built %d rules, want %d", len(index.values), entries)
		}
	}
}

// BenchmarkLossyStreamingTradeoff compares the production one-pass pressure
// policy with the same lossy planner deferred until the complete exact state is
// available. Latest local results and complete run parameters are maintained
// in docs/performance-history.md so this already-large matrix has one canonical
// measurement record.
//
// Reproduce the complete scale matrix with:
//
//	go test -run '^$' -bench '^BenchmarkLossyStreamingTradeoff/' -benchmem -benchtime=1x -count=1 .
//
// Latest focused result (Apple M1 Max, Go 1.26.0, 10K entries, four equality
// leaves, 65% budget, 1x): streaming retained 9.797 candidates/query and zero
// observed false positives for ordered and shuffled input. Accounted working
// peak was 261,928 B versus 290,856 B exact-first.
//
// The fixed permutation is bijective for every decimal scale below (its
// multiplier is coprime to 10), so Ordered and Shuffled consume identical
// constraints and IDs without retaining a second large input copy.
//
//nolint:gocognit // Keeping all dimensions in one benchmark preserves comparable names.
func BenchmarkLossyStreamingTradeoff(b *testing.B) {
	for _, entries := range []int{10_000, 100_000, 1_000_000} {
		constraints, ids := lossyStreamingBenchmarkData(entries)
		for _, operator := range []struct {
			name   string
			schema Rule[lossyAllBenchmarkConstraint]
		}{
			{name: "Equality", schema: lossyAllBenchmarkSchema(4)},
		} {
			exactBytes := lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, operator.schema)
			// A 65% retained budget is low enough to cross the 78% soft
			// working target, while leaving room for four conservative tails.
			limit := exactBytes * 65 / 100
			for _, order := range []string{"Ordered", "Shuffled"} {
				entriesSeq := lossyAllBenchmarkOrder(constraints, ids, order == "Shuffled")
				for _, policy := range []struct {
					name      string
					streaming bool
				}{
					{name: "ExactFirst", streaming: false},
					{name: "Streaming", streaming: true},
				} {
					b.Run(fmt.Sprintf("Entries%d/%s/%s/%s", entries, operator.name, order, policy.name), func(b *testing.B) {
						benchmarkLossyStreamingTradeoffCase(b, operator.schema, entriesSeq, constraints, limit, policy.streaming)
					})
				}
			}
		}
	}
}

func lossyStreamingBenchmarkData(entries int) ([]lossyAllBenchmarkConstraint, []int) {
	const distinct = 1024
	var dictionary [4][distinct]string
	for field := range dictionary {
		for value := range dictionary[field] {
			dictionary[field][value] = fmt.Sprintf("f%d-v%d", field, value)
		}
	}
	constraints := make([]lossyAllBenchmarkConstraint, entries)
	ids := make([]int, entries)
	for row := range constraints {
		ids[row] = row
		for field := range dictionary {
			value := (row*(field*2+3) + field) % distinct
			constraints[row].values[field] = dictionary[field][value]
			constraints[row].thresholds[field] = value
			constraints[row].present[field] = true
		}
	}
	return constraints, ids
}

func lossyAllBenchmarkOrder(
	constraints []lossyAllBenchmarkConstraint,
	ids []int,
	shuffled bool,
) iter.Seq2[lossyAllBenchmarkConstraint, int] {
	return func(yield func(lossyAllBenchmarkConstraint, int) bool) {
		for position := range constraints {
			row := position
			if shuffled {
				row = position * 65_537 % len(constraints)
			}
			if !yield(constraints[row], ids[row]) {
				return
			}
		}
	}
}

func benchmarkLossyStreamingTradeoffCase(
	b *testing.B,
	schema Rule[lossyAllBenchmarkConstraint],
	entries iter.Seq2[lossyAllBenchmarkConstraint, int],
	queries []lossyAllBenchmarkConstraint,
	limit uint64,
	streaming bool,
) {
	b.Helper()
	sampledQueries := make([]lossyAllBenchmarkConstraint, 0, 64)
	for i := range 64 {
		sampledQueries = append(sampledQueries, queries[i*len(queries)/64])
	}
	var aggregate Inspector
	inspectors := make([]Inspector, 4)
	operator := "Equality"
	if _, ok := schema.(*allRule[lossyAllBenchmarkConstraint]); ok {
		// Rebuild the schema with leaf inspectors. The benchmark passes a
		// four-child canonical schema.
		if _, ordered := schema.(*allRule[lossyAllBenchmarkConstraint]).children[0].(*orderedRule[lossyAllBenchmarkConstraint, int]); ordered {
			operator = "Ordered"
		}
	}
	measuredSchema := Inspect(&aggregate, Lossy(lossySelectionBenchmarkSchema(operator, 4, inspectors), MemoryLimit(limit)))
	var accountedPeak, softTarget uint64
	options := buildOptions{
		compilePhysicalAliases: true,
		enableStreaming:        streaming,
		observeWorkingUsage: func(usage, target uint64) {
			accountedPeak = max(accountedPeak, usage)
			softTarget = target
		},
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	previousGC := debug.SetGCPercent(-1)
	index, _, err := buildIndexPhysicalAliases(measuredSchema, entries, false, nil, options)
	runtime.ReadMemStats(&after)
	debug.SetGCPercent(previousGC)
	if err != nil {
		b.Fatal(err)
	}
	retained, ok := aggregate.Snapshot().MemoryUsage()
	if !ok || retained > limit {
		b.Fatalf("retained accounted bytes %d exceed limit %d", retained, limit)
	}
	downgraded := 0
	for i := range inspectors {
		if inspectors[i].Snapshot().Mode() == RuleModeLossy {
			downgraded++
		}
	}
	exactIndex, err := New[lossyAllBenchmarkConstraint, int](schema).Build(entries)
	if err != nil {
		b.Fatal(err)
	}
	var totalCandidates, exactTotal uint64
	for _, query := range sampledQueries {
		var matches, exactMatches []int
		index.Search(query, &matches)
		exactIndex.Search(query, &exactMatches)
		totalCandidates += uint64(len(matches))
		exactTotal += uint64(len(exactMatches))
	}

	runtime.GC()
	var gcBefore, gcAfter runtime.MemStats
	runtime.ReadMemStats(&gcBefore)
	gcIndex, _, gcErr := buildIndexPhysicalAliases(measuredSchema, entries, false, nil, options)
	if gcErr != nil {
		b.Fatal(gcErr)
	}
	runtime.ReadMemStats(&gcAfter)
	runtime.KeepAlive(gcIndex)
	gcCycles := uint64(gcAfter.NumGC - gcBefore.NumGC)
	gcPause := gcAfter.PauseTotalNs - gcBefore.PauseTotalNs

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		built, _, buildErr := buildIndexPhysicalAliases(measuredSchema, entries, false, nil, options)
		if buildErr != nil {
			b.Fatal(buildErr)
		}
		lossyAllBenchmarkIndex = built
	}
	b.StopTimer()
	heapGrowth := uint64(0)
	if after.HeapAlloc > before.HeapAlloc {
		heapGrowth = after.HeapAlloc - before.HeapAlloc
	}
	b.ReportMetric(float64(accountedPeak), "accounted-working-peak-B")
	b.ReportMetric(float64(softTarget), "soft-target-B")
	b.ReportMetric(float64(retained), "retained-B/index")
	b.ReportMetric(float64(downgraded), "downgraded-leaves")
	b.ReportMetric(float64(totalCandidates)/float64(len(sampledQueries)), "candidates/query")
	if possible := uint64(len(sampledQueries))*aggregate.Snapshot().RuleCount() - exactTotal; possible != 0 {
		b.ReportMetric(float64(totalCandidates-exactTotal)/float64(possible), "false-positive-rate")
	}
	b.ReportMetric(float64(heapGrowth), "live-heap-growth-B")
	b.ReportMetric(float64(gcCycles), "GC-cycles/build")
	b.ReportMetric(float64(gcPause), "GC-pause-ns/build")
}

// BenchmarkLossyScaleSearch measures latency, allocation traffic, candidate
// amplification, and observed false positives over the same scale matrix.
//
//nolint:gocognit // The benchmark matrix stays together to preserve comparability.
func BenchmarkLossyScaleSearch(b *testing.B) {
	for _, entries := range []int{10_000, 100_000, 1_000_000} {
		constraints, ids := lossyAllBenchmarkData(entries)
		queries := constraints[:16]
		for _, operator := range []struct {
			name   string
			schema Rule[lossyAllBenchmarkConstraint]
		}{
			{name: "Equality", schema: lossyAllBenchmarkSchema(4)},
			{name: "Ordered", schema: lossyAllOrderedBenchmarkSchema(4)},
		} {
			exactBytes := lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, operator.schema)
			exactIndex, err := New[lossyAllBenchmarkConstraint, int](operator.schema).Build(Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			var exactMatches []int
			var exactTotal uint64
			for _, query := range queries {
				exactMatches = exactMatches[:0]
				exactIndex.Search(query, &exactMatches)
				exactTotal += uint64(len(exactMatches))
			}

			for _, percent := range []uint64{100, 50, 25} {
				b.Run(fmt.Sprintf("Entries%d/%s/Budget%d", entries, operator.name, percent), func(b *testing.B) {
					schema := operator.schema
					if percent != 100 {
						schema = Lossy(schema, MemoryLimit(exactBytes*percent/100))
					}
					accountedBytes := exactBytes
					if percent != 100 {
						accountedBytes = lossyAllBenchmarkAccountedBytes(b, constraints, ids, schema)
					}
					index, err := New[lossyAllBenchmarkConstraint, int](schema).Build(Zip(constraints, ids))
					if err != nil {
						b.Fatal(err)
					}
					var matches []int
					var totalMatches uint64
					for _, query := range queries {
						matches = matches[:0]
						index.Search(query, &matches)
						totalMatches += uint64(len(matches))
					}
					falsePositives := totalMatches - exactTotal
					possibleFalsePositives := uint64(len(queries)*entries) - exactTotal

					b.ReportAllocs()
					b.ResetTimer()
					for i := range b.N {
						matches = matches[:0]
						index.Search(queries[i%len(queries)], &matches)
					}
					b.ReportMetric(float64(totalMatches)/float64(len(queries)), "candidates/query")
					b.ReportMetric(float64(accountedBytes), "accounted-B/index")
					if possibleFalsePositives != 0 {
						b.ReportMetric(float64(falsePositives)/float64(possibleFalsePositives), "false-positive-rate")
					}
					lossyAllBenchmarkIndex = index
				})
			}
		}
	}
}

func lossyAllBenchmarkExactBytes(
	b testing.TB,
	constraints []lossyAllBenchmarkConstraint,
	ids []int,
	children int,
) uint64 {
	b.Helper()
	return lossyAllBenchmarkExactBytesForSchema(b, constraints, ids, lossyAllBenchmarkSchema(children))
}

func lossyAllBenchmarkExactBytesForSchema(
	b testing.TB,
	constraints []lossyAllBenchmarkConstraint,
	ids []int,
	schema Rule[lossyAllBenchmarkConstraint],
) uint64 {
	b.Helper()
	var inspector Inspector
	_, err := New[lossyAllBenchmarkConstraint, int](Inspect(
		&inspector,
		Lossy(schema, MemoryLimit(^uint64(0))),
	)).Build(Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	usage, ok := inspector.Snapshot().MemoryUsage()
	if !ok {
		b.Fatal("exact memory usage is unavailable")
	}
	return usage
}

func lossyAllBenchmarkAccountedBytes(
	b testing.TB,
	constraints []lossyAllBenchmarkConstraint,
	ids []int,
	schema Rule[lossyAllBenchmarkConstraint],
) uint64 {
	b.Helper()
	var inspector Inspector
	_, err := New[lossyAllBenchmarkConstraint, int](Inspect(&inspector, schema)).Build(Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	usage, ok := inspector.Snapshot().MemoryUsage()
	if !ok {
		b.Fatal("accounted memory usage is unavailable")
	}
	return usage
}
