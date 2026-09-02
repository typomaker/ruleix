package ruleix_test

import (
	"cmp"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/typomaker/ruleix"
)

type productionAttributionMode string

const (
	productionAttributionExact    productionAttributionMode = "Exact"
	productionAttributionIdentity productionAttributionMode = "IdentityLossy"
	productionAttributionLossy50  productionAttributionMode = "Lossy50OrMinimum"
)

type productionAttributionFamily struct {
	name             string
	schema           func() ruleix.Rule[productionBenchmarkConstraint]
	minimumPercent50 uint64
}

func productionAttributionFamilies() []productionAttributionFamily {
	return []productionAttributionFamily{
		{name: "Equality", schema: productionBenchmarkEqualityOnlySchema},
		{name: "StandaloneOrdered", schema: productionBenchmarkStandaloneOrderedSchema},
		{name: "Between", schema: productionBenchmarkBetweenOnlySchema},
		// CompareBy's smallest key-only representation is 56.75% of exact on
		// this fixture, so the standalone attribution uses that hard minimum.
		{name: "CompareBy", schema: productionBenchmarkCompareByOnlySchema, minimumPercent50: 57},
		{name: "All", schema: productionBenchmarkSchema},
	}
}

func productionBenchmarkStandaloneOrderedSchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.GreaterOrEqual(func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.activity == nil {
			return time.Time{}, false
		}
		return value.activity.since.Truncate(time.Second), true
	}, time.Time.Compare)
}

func productionBenchmarkBetweenOnlySchema() ruleix.Rule[productionBenchmarkConstraint] {
	return productionBenchmarkActivityRule()
}

func productionBenchmarkCompareByOnlySchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.CompareBy(func(value productionBenchmarkConstraint) ([3]int, bool) {
		if value.platform == nil || value.platform.version == nil {
			return [3]int{}, false
		}
		version := value.platform.version
		return [3]int{version.major, version.minor, version.patch}, true
	}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
		if value.platform == nil || value.platform.version == nil {
			return 0, false
		}
		return ruleix.OperatorGTE, true
	}, func(a, b [3]int) int {
		for i := range a {
			if result := cmp.Compare(a[i], b[i]); result != 0 {
				return result
			}
		}
		return 0
	})
}

func buildProductionAttributionIndex(
	b testing.TB,
	schema ruleix.Rule[productionBenchmarkConstraint],
	mode productionAttributionMode,
	limit uint64,
	constraints []productionBenchmarkConstraint,
	ids []productionBenchmarkID,
) (*ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID], uint64) {
	b.Helper()
	var inspector ruleix.Inspector
	inspected := ruleix.Inspect(&inspector, schema)
	if mode != productionAttributionExact {
		inspected = ruleix.Inspect(&inspector, ruleix.Lossy(schema, ruleix.MemoryLimit(limit)))
	}
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](inspected).
		Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	accounted, ok := inspector.Snapshot().MemoryUsage()
	if !ok {
		if mode == productionAttributionExact {
			return index, 0
		}
		b.Fatalf("%s accounted memory is unavailable", mode)
	}
	return index, accounted
}

func productionAttributionCandidateAverage(
	index *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID],
	queries []productionBenchmarkConstraint,
) float64 {
	matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
	total := 0
	for _, query := range queries {
		matches = matches[:0]
		index.Search(query, &matches)
		total += len(matches)
	}
	return float64(total) / float64(len(queries))
}

// BenchmarkProductionShapeAttribution isolates search amplification by rule
// family on the production fixture. Exact and IdentityLossy establish physical
// parity; Lossy50 attributes the candidate expansion caused by each quantizer.
//
// Reproduce on an otherwise idle machine with:
//
//	GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkProductionShapeAttribution/' \
//	  -benchmem -benchtime=500ms -count=5 .
//
// Latest local attribution (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1,
// 38,098 entries, 200ms x3): Lossy candidate amplification was 254x equality,
// 3.567x standalone ordered, 2.640x Between, 1.057x CompareBy at its 57%
// minimum, and 38.27x for production All. Full latency/allocation medians and
// the comparable command are recorded in docs/unified-index-production-shape.md.
func BenchmarkProductionShapeAttribution(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	queries := []productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for _, family := range productionAttributionFamilies() {
		b.Run(family.name, func(b *testing.B) {
			_, exactBytes := buildProductionAttributionIndex(
				b, family.schema(), productionAttributionIdentity, math.MaxUint64, constraints, ids,
			)
			limit := exactBytes / 2
			if family.minimumPercent50 > 50 {
				limit = exactBytes * family.minimumPercent50 / 100
			}
			exact, _ := buildProductionAttributionIndex(
				b, family.schema(), productionAttributionExact, math.MaxUint64, constraints, ids,
			)
			exactCandidates := productionAttributionCandidateAverage(exact, queries)
			for _, mode := range []productionAttributionMode{
				productionAttributionExact,
				productionAttributionIdentity,
				productionAttributionLossy50,
			} {
				modeLimit := uint64(math.MaxUint64)
				if mode == productionAttributionLossy50 {
					modeLimit = limit
				}
				index, accounted := buildProductionAttributionIndex(
					b, family.schema(), mode, modeLimit, constraints, ids,
				)
				if mode == productionAttributionExact {
					accounted = exactBytes
				}
				candidates := productionAttributionCandidateAverage(index, queries)
				for _, local := range []bool{false, true} {
					path := "IndexSearch"
					if local {
						path = "WarmLocalSearch"
					}
					b.Run(string(mode)+"/"+path, func(b *testing.B) {
						searcher := index.Local()
						b.Cleanup(searcher.Close)
						matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
						query := queries[0]
						if local {
							for range 4 {
								matches = matches[:0]
								searcher.Search(query, &matches)
							}
						}
						b.ReportAllocs()
						b.ResetTimer()
						for range b.N {
							matches = matches[:0]
							if local {
								searcher.Search(query, &matches)
							} else {
								index.Search(query, &matches)
							}
						}
						b.ReportMetric(float64(accounted), "accounted-B/index")
						b.ReportMetric(float64(limit)*100/float64(exactBytes), "budget-percent")
						b.ReportMetric(candidates, "candidates/query")
						b.ReportMetric(candidates/exactCandidates, "candidate-amplification")
					})
				}
			}
		})
	}
}

// BenchmarkProductionEqualityPrecisionShape reports the physical posting
// distribution at every budget selected by the production equality planner.
// The timed search benchmark above remains the end-to-end acceptance gate.
// Latest local shape (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 38,098 entries,
// 1x): 75% retained 534 buckets, max posting 29,137, weighted collision
// 13,067 and 0.04693 observed query FP rate; 50% retained six buckets, max
// posting 38,097, weighted collision 34,764 and 1.0 observed query FP rate.
func BenchmarkProductionEqualityPrecisionShape(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	queries := []productionBenchmarkConstraint{
		productionBenchmarkQuery(100), productionBenchmarkQuery(101),
	}
	_, exactBytes := buildProductionAttributionIndex(
		b, productionBenchmarkEqualityOnlySchema(), productionAttributionIdentity,
		math.MaxUint64, constraints, ids,
	)
	exact, _ := buildProductionAttributionIndex(
		b, productionBenchmarkEqualityOnlySchema(), productionAttributionExact,
		math.MaxUint64, constraints, ids,
	)
	exactCandidates := productionAttributionCandidateAverage(exact, queries)
	for _, percent := range []uint64{100, 75, 50} {
		index, accounted := buildProductionAttributionIndex(
			b, productionBenchmarkEqualityOnlySchema(), productionAttributionLossy50,
			exactBytes*percent/100, constraints, ids,
		)
		diagnostics := ruleix.EqualityDiagnostics(index)
		candidates := productionAttributionCandidateAverage(index, queries)
		b.Run(fmt.Sprintf("Budget%d", percent), func(b *testing.B) {
			var buckets, items, maxPosting, maxMedianPosting, maxP95Posting uint64
			var weighted float64
			for _, diagnostic := range diagnostics {
				buckets += diagnostic.Buckets
				items += diagnostic.Items
				maxPosting = max(maxPosting, diagnostic.MaxPosting)
				maxMedianPosting = max(maxMedianPosting, diagnostic.MedianPosting)
				maxP95Posting = max(maxP95Posting, diagnostic.P95Posting)
				weighted += diagnostic.WeightedCollision * float64(diagnostic.Items)
			}
			b.ReportMetric(float64(accounted), "accounted-B/index")
			b.ReportMetric(float64(buckets), "buckets")
			b.ReportMetric(candidates, "candidates/query")
			b.ReportMetric(float64(len(diagnostics)), "lossy-leaves")
			b.ReportMetric(float64(maxPosting), "max-posting")
			b.ReportMetric(float64(maxMedianPosting), "max-leaf-median-posting")
			b.ReportMetric(float64(maxP95Posting), "max-leaf-p95-posting")
			denominator := float64(productionBenchmarkEntries) - exactCandidates
			if denominator > 0 {
				b.ReportMetric((candidates-exactCandidates)/denominator, "observed-query-fp-rate")
			}
			if items != 0 {
				b.ReportMetric(weighted/float64(items), "weighted-collision")
			}
		})
	}
}
