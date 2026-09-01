package ruleix

import (
	"cmp"
	"fmt"
	"testing"
)

var sharedKeyBaselineIndex *Index[lossyDifferentialConstraint, int]

type sharedKeyBaselineMode uint8

const (
	sharedKeyBaselineExact sharedKeyBaselineMode = iota
	sharedKeyBaselineIdentity
	sharedKeyBaselineLossy50
)

func (mode sharedKeyBaselineMode) String() string {
	switch mode {
	case sharedKeyBaselineExact:
		return "Exact"
	case sharedKeyBaselineIdentity:
		return "IdentityLossy"
	case sharedKeyBaselineLossy50:
		return "Lossy50"
	default:
		return fmt.Sprintf("Mode%d", mode)
	}
}

func sharedKeyBaselineSchema() Rule[lossyDifferentialConstraint] {
	name := func(v lossyDifferentialConstraint) (string, bool) { return v.name, v.namePresent }
	value := func(v lossyDifferentialConstraint) (int, bool) { return v.value, v.valuePresent }
	from := func(v lossyDifferentialConstraint) (int, bool) { return v.from, v.fromPresent }
	until := func(v lossyDifferentialConstraint) (int, bool) { return v.until, v.untilPresent }
	return All(
		Include(name),
		GreaterOrEqual(value, cmp.Compare[int]),
		Between(from, until, cmp.Compare[int]),
	)
}

func buildSharedKeyBaseline(
	b testing.TB,
	mode sharedKeyBaselineMode,
	limit uint64,
	constraints []lossyDifferentialConstraint,
	ids []int,
	inspector *Inspector,
) *Index[lossyDifferentialConstraint, int] {
	b.Helper()
	schema := sharedKeyBaselineSchema()
	var (
		index *Index[lossyDifferentialConstraint, int]
		err   error
	)
	switch mode {
	case sharedKeyBaselineExact:
		index, err = New[lossyDifferentialConstraint, int](Inspect(inspector, schema)).Build(Zip(constraints, ids))
	case sharedKeyBaselineIdentity:
		index, _, err = buildIndexPhysicalAliases(
			Inspect(inspector, Lossy(schema, MemoryLimit(1))), Zip(constraints, ids), false, nil,
			buildOptions{compilePhysicalAliases: true, identityLossy: true},
		)
	case sharedKeyBaselineLossy50:
		index, err = New[lossyDifferentialConstraint, int](
			Inspect(inspector, Lossy(schema, MemoryLimit(limit))),
		).Build(Zip(constraints, ids))
	default:
		b.Fatalf("unknown shared-key baseline mode %d", mode)
	}
	if err != nil {
		b.Fatal(err)
	}
	return index
}

// BenchmarkSharedKeyBaseline freezes the pre-migration Exact, identity-lossy,
// and 50%-budget Lossy lifecycle on one mixed equality/ordered/range schema.
// The separate differential gate covers every public Lossy-supported rule;
// this deliberately smaller schema keeps the repeated performance gate useful.
//
// Reproduce on an otherwise idle machine with:
//
//	GOMAXPROCS=1 go test -run '^$' -bench '^BenchmarkSharedKeyBaseline/' \
//	  -benchmem -benchtime=500ms -count=5 .
func BenchmarkSharedKeyBaseline(b *testing.B) {
	constraints, ids := lossyDifferentialData()
	queries := lossyDifferentialQueries()
	var exactInspector Inspector
	_, err := New[lossyDifferentialConstraint, int](Inspect(
		&exactInspector,
		Lossy(sharedKeyBaselineSchema(), MemoryLimit(^uint64(0))),
	)).Build(Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	exactBytes, ok := exactInspector.Snapshot().MemoryUsage()
	if !ok {
		b.Fatal("exact accounted memory is unavailable")
	}
	limit := exactBytes / 2

	for _, mode := range []sharedKeyBaselineMode{
		sharedKeyBaselineExact,
		sharedKeyBaselineIdentity,
		sharedKeyBaselineLossy50,
	} {
		b.Run(mode.String(), func(b *testing.B) {
			var inspector Inspector
			index := buildSharedKeyBaseline(b, mode, limit, constraints, ids, &inspector)
			accounted, available := inspector.Snapshot().MemoryUsage()
			if mode == sharedKeyBaselineExact {
				accounted, available = exactBytes, true
			}
			if !available {
				b.Fatal("accounted memory is unavailable")
			}
			var matches []int
			totalCandidates := 0
			for _, query := range queries {
				matches = matches[:0]
				index.Search(query, &matches)
				totalCandidates += len(matches)
			}
			candidates := float64(totalCandidates) / float64(len(queries))

			b.Run("Build", func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var buildInspector Inspector
					sharedKeyBaselineIndex = buildSharedKeyBaseline(
						b, mode, limit, constraints, ids, &buildInspector,
					)
				}
				b.ReportMetric(float64(accounted), "accounted-B/index")
			})

			for _, local := range []bool{false, true} {
				path := "IndexSearch"
				if local {
					path = "WarmLocalSearch"
				}
				b.Run(path, func(b *testing.B) {
					searcher := index.Local()
					b.Cleanup(searcher.Close)
					matches := make([]int, 0, len(constraints))
					query := queries[len(queries)/2]
					if local {
						searcher.Search(query, &matches)
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
					b.ReportMetric(candidates, "candidates/query")
				})
			}
		})
	}
}
