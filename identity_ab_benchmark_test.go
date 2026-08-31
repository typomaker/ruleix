package ruleix

import (
	"cmp"
	"fmt"
	"testing"
)

// BenchmarkPhysicalIdentityAB keeps both executor modes in one binary and
// exercises the public root All path. BaselineFirst and IntegratedFirst make
// order effects visible without comparing measurements from separate builds.
// Counters are reported beside timing so later integrated work must explain
// any win by reduced physical operations.
//
// Latest local calibration (Apple M1 Max, Go 1.26.0, 2s, count=10), children
// 2/4/8: Baseline medians 1331/2226/4090 ns/op at 536 B/op and 2 allocs/op;
// Integrated medians 834/922/1087 ns/op at 0 B/op and 0 allocs/op. Baseline
// performs 2/4/8 physical searches and 1/3/7 intersections; Integrated performs
// one physical search, 2/4/8 mask tests, and skips 1/3/7 operands. This is
// structural evidence only; the complete performance decision remains step E.
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkPhysicalIdentityAB/' \
//	  -benchmem -benchtime=2s -count=10 .
//
//nolint:gocognit // The benchmark matrix deliberately shares one setup path.
func BenchmarkPhysicalIdentityAB(b *testing.B) {
	for _, children := range []int{2, 4, 8} {
		b.Run(fmt.Sprintf("Children%d", children), func(b *testing.B) {
			for _, order := range []struct {
				name  string
				modes []identityExecutionMode
			}{
				{"BaselineFirst", []identityExecutionMode{identityBaseline, identityIntegrated}},
				{"IntegratedFirst", []identityExecutionMode{identityIntegrated, identityBaseline}},
			} {
				b.Run(order.name, func(b *testing.B) {
					for _, mode := range order.modes {
						name := "Baseline"
						if mode == identityIntegrated {
							name = "Integrated"
						}
						b.Run(name, func(b *testing.B) {
							schema, constraints, ids := identityABFixture(children, false)
							harness := buildIdentityABIndex(b, mode, schema, constraints, ids)
							query := identityABConstraint{}
							for child := range children {
								query.values[child] = 1
							}
							result := make([]int, 0, 256)
							b.ReportAllocs()
							b.ResetTimer()
							for range b.N {
								result = result[:0]
								harness.index.Search(query, &result)
							}
							b.StopTimer()
							if len(result) != 256 {
								b.Fatalf("got %d results, want 256", len(result))
							}
							n := float64(b.N)
							b.ReportMetric(float64(harness.counters.materializations)/n, "materializations/op")
							b.ReportMetric(float64(harness.counters.intersections)/n, "intersections/op")
							b.ReportMetric(float64(harness.counters.containsChecks)/n, "contains/op")
							b.ReportMetric(float64(harness.counters.skippedOperands)/n, "skipped/op")
							b.ReportMetric(float64(harness.counters.maskTests)/n, "mask-tests/op")
							b.ReportMetric(float64(harness.counters.physicalInspectorSearches)/n, "physical-searches/op")
							if harness.counters.linearEqualityDedupRuns != 0 {
								b.Fatal("root All unexpectedly ran nested linear equality deduplication")
							}
						})
					}
				})
			}
		})
	}
}

// BenchmarkIdentityDecisionSearch is the step-E decision matrix for equality
// sources. It keeps both executors in one process, includes concrete and
// wildcard queries, and makes the zero-duplicate no-mask control explicit.
//
// Latest local 8-child run (Apple M1 Max, Go 1.26.0, 500ms, count=3):
// Baseline/Integrated medians were 1,022/1,024 ns concrete and 41.10/41.32 ns
// wildcard at 0% duplication; 952.8/951.0 ns and 98.64/98.62 ns at 50%; and
// 322.2/322.1 ns and 306.6/306.4 ns at 100%. All remained at 0 B/op and
// 0 allocs/op; the zero-duplicate cases performed no mask checks. Reproduce
// the complete matrix with:
//
// \tgo test -run '^$' -bench '^BenchmarkIdentityDecisionSearch/' \
// \t  -benchmem -benchtime=2s -count=10 .
//
//nolint:gocognit // The benchmark matrix deliberately shares one setup path.
func BenchmarkIdentityDecisionSearch(b *testing.B) {
	for _, children := range []int{2, 4, 8} {
		for _, duplication := range []int{0, 50, 100} {
			for _, wildcard := range []bool{false, true} {
				name := fmt.Sprintf("Children%d/Duplication%d/Concrete", children, duplication)
				if wildcard {
					name = fmt.Sprintf("Children%d/Duplication%d/Wildcard", children, duplication)
				}
				b.Run(name, func(b *testing.B) {
					constraints, ids := identityMatrixEntries(children, 32, duplication)
					query := constraints[1]
					if wildcard {
						query = identityMatrixConstraint{}
					}
					for _, mode := range []identityExecutionMode{identityBaseline, identityIntegrated} {
						modeName := "Baseline"
						if mode == identityIntegrated {
							modeName = "Integrated"
						}
						b.Run(modeName, func(b *testing.B) {
							harness := buildIdentityABIndex(b, mode,
								identityMatrixEqualitySchema(children, nil), constraints, ids)
							matches := make([]int, 0, len(ids))
							local := harness.index.Local()
							defer local.Close()
							for range 3 {
								matches = matches[:0]
								local.Search(query, &matches)
							}
							b.ReportAllocs()
							b.ResetTimer()
							for range b.N {
								matches = matches[:0]
								local.Search(query, &matches)
							}
							b.StopTimer()
							n := float64(b.N + 3)
							b.ReportMetric(float64(harness.counters.maskTests)/n, "mask-tests/op")
							b.ReportMetric(float64(harness.counters.skippedOperands)/n, "skipped/op")
							if duplication == 0 && harness.counters.maskTests != 0 {
								b.Fatal("zero-duplicate control executed a mask check")
							}
						})
					}
				})
			}
		}
	}
}

// BenchmarkIdentityDecisionLifecycle isolates allocation-sensitive lifecycle
// stages from steady-state search. The second search is the admission event;
// Warm measures an already admitted result.
//
// Latest local run (Apple M1 Max, Go 1.26.0, 500ms, count=3): Baseline versus
// Integrated medians were 5,483 versus 1,630 ns/op for ColdFirst, 5,264 versus
// 1,299 ns/op for SecondAdmission, and 880.7 versus 875.6 ns/op for Warm.
// Warm remained at 0 B/op and 0 allocs/op. Reproduce with:
//
// \tgo test -run '^$' -bench '^BenchmarkIdentityDecisionLifecycle/' \
// \t  -benchmem -benchtime=2s -count=10 .
//
//nolint:gocognit // The benchmark matrix deliberately shares one setup path.
func BenchmarkIdentityDecisionLifecycle(b *testing.B) {
	schema, constraints, ids := identityABFixture(8, false)
	query := identityABConstraint{}
	for child := range 8 {
		query.values[child] = 1
	}
	for _, mode := range []identityExecutionMode{identityBaseline, identityIntegrated} {
		modeName := "Baseline"
		if mode == identityIntegrated {
			modeName = "Integrated"
		}
		b.Run(modeName, func(b *testing.B) {
			harness := buildIdentityABIndex(b, mode, schema, constraints, ids)
			b.Run("LocalClose", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					harness.index.Local().Close()
				}
			})
			for _, stage := range []struct {
				name      string
				presearch int
			}{{"ColdFirst", 0}, {"SecondAdmission", 1}, {"Warm", 3}} {
				b.Run(stage.name, func(b *testing.B) {
					matches := make([]int, 0, 256)
					b.ReportAllocs()
					if stage.name == "Warm" {
						local := harness.index.Local()
						defer local.Close()
						for range stage.presearch {
							local.Search(query, &matches)
							matches = matches[:0]
						}
						b.ResetTimer()
						for range b.N {
							matches = matches[:0]
							local.Search(query, &matches)
						}
						return
					}
					const batchSize = 256
					locals := make([]*Local[identityABConstraint, int], batchSize)
					for completed := 0; completed < b.N; {
						batch := min(batchSize, b.N-completed)
						b.StopTimer()
						for i := range batch {
							locals[i] = harness.index.Local()
							for range stage.presearch {
								matches = matches[:0]
								locals[i].Search(query, &matches)
							}
						}
						b.StartTimer()
						for _, local := range locals[:batch] {
							matches = matches[:0]
							local.Search(query, &matches)
						}
						b.StopTimer()
						for _, local := range locals[:batch] {
							local.Close()
						}
						completed += batch
						b.StartTimer()
					}
				})
			}
		})
	}
}

// BenchmarkIdentityDecisionOperations compares the non-equality identity
// proofs that are compiled away before execution. Each case deliberately uses
// several inspected aliases of one schema Rule; Nested also proves that alias
// elimination survives All flattening. The independent control uses distinct
// Rule instances and must retain every operand in both modes.
//
// Latest local calibration (Apple M1 Max, Go 1.26.0, 300ms, count=3): eight
// aliases reduced Ordered from 64.2 to 9.78 us/op and 32,298 to 6,940 B/op,
// Between from 86.8 to 12.8 us/op and 32,299 to 6,940 B/op, and CompareBy
// from 63.4 to 9.74 us/op and 30,729 to 6,940 B/op. Independent controls
// retained the same allocation classes and had overlapping timings. Reproduce
// the complete decision matrix with:
//
// \tgo test -run '^$' -bench '^BenchmarkIdentityDecisionOperations/' \
// \t  -benchmem -benchtime=2s -count=10 .
//
//nolint:gocognit // The benchmark matrix deliberately shares one setup path.
func BenchmarkIdentityDecisionOperations(b *testing.B) {
	type operation struct {
		name string
		make func() Rule[identityMatrixConstraint]
	}
	operations := []operation{
		{"Ordered", func() Rule[identityMatrixConstraint] {
			return GreaterOrEqual(func(v identityMatrixConstraint) (int, bool) {
				return v.values[0], v.present[0]
			}, cmp.Compare[int])
		}},
		{"Between", func() Rule[identityMatrixConstraint] {
			return Between(
				func(v identityMatrixConstraint) (int, bool) { return v.values[0], v.present[0] },
				func(v identityMatrixConstraint) (int, bool) { return v.until, v.present[0] },
				cmp.Compare[int])
		}},
		{"CompareBy", func() Rule[identityMatrixConstraint] {
			return CompareBy(
				func(v identityMatrixConstraint) (int, bool) { return v.values[0], v.present[0] },
				func(v identityMatrixConstraint) (Operator, bool) { return v.op, v.present[0] },
				cmp.Compare[int])
		}},
	}
	entries := make([]identityMatrixConstraint, 4096)
	ids := make([]int, len(entries))
	for i := range entries {
		entries[i] = identityMatrixConstraint{
			values:  [8]int{i % 64},
			present: [8]bool{true},
			until:   48,
			op:      OperatorGTE,
		}
		ids[i] = i
	}
	query := identityMatrixConstraint{values: [8]int{16}, present: [8]bool{true}, until: 48, op: OperatorGTE}

	for _, operation := range operations {
		for _, children := range []int{2, 4, 8} {
			for _, shape := range []string{"Aliases", "Nested", "Independent"} {
				name := fmt.Sprintf("%s/Children%d/%s", operation.name, children, shape)
				b.Run(name, func(b *testing.B) {
					var inspectors [8]Inspector
					rules := make([]Rule[identityMatrixConstraint], children)
					shared := operation.make()
					for i := range rules {
						rule := shared
						if shape == "Independent" {
							rule = operation.make()
						}
						rules[i] = Inspect(&inspectors[i], rule)
					}
					schema := All(rules...)
					if shape == "Nested" {
						schema = All(All(rules[:children/2]...), All(rules[children/2:]...))
					}
					for _, mode := range []identityExecutionMode{identityBaseline, identityIntegrated} {
						modeName := "Baseline"
						if mode == identityIntegrated {
							modeName = "Integrated"
						}
						b.Run(modeName, func(b *testing.B) {
							harness := buildIdentityABIndex(b, mode, schema, entries, ids)
							matches := make([]int, 0, len(ids))
							b.ReportAllocs()
							b.ResetTimer()
							for range b.N {
								matches = matches[:0]
								harness.index.Search(query, &matches)
							}
							b.StopTimer()
							b.ReportMetric(float64(harness.counters.materializations)/float64(b.N), "materializations/op")
							b.ReportMetric(float64(harness.counters.skippedOperands)/float64(b.N), "skipped/op")
						})
					}
				})
			}
		}
	}
}
