package ruleix_test

import (
	"cmp"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/typomaker/ruleix"
)

type productionOrderedPrecisionCase struct {
	name     string
	schema   ruleix.Rule[productionBenchmarkConstraint]
	queries  []productionBenchmarkConstraint
	percents []uint64
}

func productionOrderedTimeGetter(value productionBenchmarkConstraint) (time.Time, bool) {
	if value.activity == nil {
		return time.Time{}, false
	}
	return value.activity.since.Truncate(time.Second), true
}

func productionOrderedUpperTimeGetter(value productionBenchmarkConstraint) (time.Time, bool) {
	if value.activity == nil {
		return time.Time{}, false
	}
	return value.activity.until.Truncate(time.Second), true
}

func productionOrderedPrecisionQueries() []productionBenchmarkConstraint {
	queries := make([]productionBenchmarkConstraint, 0, 365)
	for day := range 365 {
		query := productionBenchmarkQuery(day)
		query.activity.until = query.activity.since.AddDate(0, 0, 7)
		queries = append(queries, query)
	}
	return queries
}

func productionCompareByPrecisionQueries() []productionBenchmarkConstraint {
	queries := make([]productionBenchmarkConstraint, 0, 5)
	for major := range 5 {
		query := productionBenchmarkQuery(100)
		query.platform.version.major = major
		queries = append(queries, query)
	}
	return queries
}

func productionCompareByPrecisionSchema(operator ruleix.Operator) ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.CompareBy(func(value productionBenchmarkConstraint) (int, bool) {
		if value.platform == nil || value.platform.version == nil {
			return 0, false
		}
		return value.platform.version.major, true
	}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
		if value.platform == nil || value.platform.version == nil {
			return 0, false
		}
		return operator, true
	}, cmp.Compare[int])
}

//nolint:lll // Keeping each diagnostic boundary on one line makes the matrix auditable.
func productionOrderedPrecisionCases() []productionOrderedPrecisionCase {
	timeQueries := productionOrderedPrecisionQueries()
	compareQueries := productionCompareByPrecisionQueries()
	return []productionOrderedPrecisionCase{
		{name: "Lower/GT/Strict", schema: ruleix.Greater(productionOrderedTimeGetter, time.Time.Compare), queries: timeQueries, percents: []uint64{75, 50}},
		{name: "Lower/GTE/Inclusive", schema: ruleix.GreaterOrEqual(productionOrderedTimeGetter, time.Time.Compare), queries: timeQueries, percents: []uint64{75, 50}},
		{name: "Upper/LT/Strict", schema: ruleix.Less(productionOrderedUpperTimeGetter, time.Time.Compare), queries: timeQueries, percents: []uint64{75, 50}},
		{name: "Upper/LTE/Inclusive", schema: ruleix.LessOrEqual(productionOrderedUpperTimeGetter, time.Time.Compare), queries: timeQueries, percents: []uint64{75, 50}},
		{name: "CompareBy/EQ", schema: productionCompareByPrecisionSchema(ruleix.OperatorEQ), queries: compareQueries, percents: []uint64{75, 57}},
		{name: "CompareBy/LT/Strict", schema: productionCompareByPrecisionSchema(ruleix.OperatorLT), queries: compareQueries, percents: []uint64{75, 57}},
		{name: "CompareBy/LTE/Inclusive", schema: productionCompareByPrecisionSchema(ruleix.OperatorLTE), queries: compareQueries, percents: []uint64{75, 57}},
		{name: "CompareBy/GT/Strict", schema: productionCompareByPrecisionSchema(ruleix.OperatorGT), queries: compareQueries, percents: []uint64{75, 57}},
		{name: "CompareBy/GTE/Inclusive", schema: productionCompareByPrecisionSchema(ruleix.OperatorGTE), queries: compareQueries, percents: []uint64{75, 57}},
	}
}

func buildProductionOrderedPrecisionIndex(
	b testing.TB,
	schema ruleix.Rule[productionBenchmarkConstraint],
	limit uint64,
	constraints []productionBenchmarkConstraint,
	ids []productionBenchmarkID,
) (*ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID], ruleix.InspectorSnapshot) {
	b.Helper()
	var inspector ruleix.Inspector
	inspected := ruleix.Inspect(&inspector, ruleix.Lossy(schema, ruleix.MemoryLimit(limit)))
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](inspected).
		Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	return index, inspector.Snapshot()
}

// BenchmarkProductionOrderedPrecisionShape measures outward candidate
// expansion independently for lower/upper and strict/inclusive boundaries.
// CompareBy is split by stored operator. The benchmark deliberately samples
// the observed value domain; its ns/op is diagnostic setup time, not a search
// performance gate.
// Latest local shape (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 38,098
// entries, 1x): standalone lower/upper retained two classes at 75% and one at
// 50%; amplification was 1.80x/1.99x lower and 1.50x/2.00x upper. Every
// CompareBy operator retained one class at 75% and 57%, with 1.035x-1.074x
// amplification. Full per-boundary values are recorded in
// docs/unified-index-production-shape.md.
//
// Reproduce on an otherwise idle machine with:
//
//	GOMAXPROCS=1 go test -run '^$' \
//	  -bench '^BenchmarkProductionOrderedPrecisionShape/' -benchtime=1x -count=1 .
func BenchmarkProductionOrderedPrecisionShape(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	for _, test := range productionOrderedPrecisionCases() {
		b.Run(test.name, func(b *testing.B) {
			identity, identitySnapshot := buildProductionOrderedPrecisionIndex(
				b, test.schema, math.MaxUint64, constraints, ids,
			)
			exactBytes, ok := identitySnapshot.MemoryUsage()
			if !ok {
				b.Fatal("identity accounted memory unavailable")
			}
			exactCandidates := productionAttributionCandidateAverage(identity, test.queries)
			for _, percent := range test.percents {
				b.Run(fmt.Sprintf("Budget%d", percent), func(b *testing.B) {
					lossy, lossySnapshot := buildProductionOrderedPrecisionIndex(
						b, test.schema, exactBytes*percent/100, constraints, ids,
					)
					lossyCandidates := productionAttributionCandidateAverage(lossy, test.queries)
					accounted, ok := lossySnapshot.MemoryUsage()
					if !ok {
						b.Fatal("lossy accounted memory unavailable")
					}
					classes, ok := lossySnapshot.Granularity()
					if !ok {
						b.Fatal("lossy granularity unavailable")
					}
					b.ReportMetric(float64(accounted), "accounted-B/index")
					b.ReportMetric(float64(classes), "classes")
					b.ReportMetric(exactCandidates, "exact-candidates/query")
					b.ReportMetric(lossyCandidates, "lossy-candidates/query")
					if exactCandidates != 0 {
						b.ReportMetric(lossyCandidates/exactCandidates, "candidate-amplification")
					}
				})
			}
		})
	}
}
