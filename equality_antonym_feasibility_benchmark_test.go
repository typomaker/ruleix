package ruleix_test

import (
	"math"
	"testing"

	"github.com/typomaker/ruleix"
)

// BenchmarkProductionStrictEqualityAntonymPairs reports whether the existing
// production fixture contains strict wildcard-complement pairs. Latest local
// command: GOMAXPROCS=1 go test -run '^$' -bench
// '^BenchmarkProductionStrictEqualityAntonymPairs$' -benchtime=1x -count=1 .
// Apple M1 Max, Go 1.26.0, 38,098 entries: Exact, identity-compressed, and
// Lossy50 each found zero strict pairs.
func BenchmarkProductionStrictEqualityAntonymPairs(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	for _, mode := range []struct {
		name string
		rule ruleix.Rule[productionBenchmarkConstraint]
	}{
		{name: "Exact", rule: productionBenchmarkSchema()},
		{name: "IdentityLossy", rule: ruleix.Lossy(productionBenchmarkSchema(), ruleix.MemoryLimit(math.MaxUint64))},
		{name: "Lossy50", rule: ruleix.Lossy(productionBenchmarkSchema(), ruleix.MemoryLimit(320895))},
	} {
		index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](mode.rule).
			Build(ruleix.Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		pairs := ruleix.StrictEqualityAntonymPairsForTest(index)
		b.Run(mode.name, func(b *testing.B) {
			b.ReportMetric(float64(len(pairs)), "strict-pairs")
		})
	}
}
