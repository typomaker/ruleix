package ruleix

import (
	"cmp"
	"testing"
)

// materializationBudget is deliberately confined to this _test.go file. It is
// a replay counter, not production telemetry: the ordinary Search path gains
// no fields, hooks, branches, atomics, or timers.
type materializationBudget struct {
	acquisitions       uint64
	acquiredBytes      uint64
	materializations   uint64
	materializedBytes  uint64
	candidateFilters   uint64
	intersections      uint64
	exclusions         uint64
	peakScratchBitmaps uint64
	finalCardinality   uint64
	finalBytes         uint64
	byRepresentation   map[string]materializationRepresentationBudget
}

type materializationRepresentationBudget struct {
	acquisitions      uint64
	acquiredBytes     uint64
	materializations  uint64
	materializedBytes uint64
	candidateFilters  uint64
}

func (m *materializationBudget) representation(name string) materializationRepresentationBudget {
	return m.byRepresentation[name]
}

func (m *materializationBudget) updateRepresentation(name string, update func(*materializationRepresentationBudget)) {
	entry := m.byRepresentation[name]
	update(&entry)
	m.byRepresentation[name] = entry
}

func materializationRepresentation[T any](rule Rule[T]) string {
	if _, ok := rule.(*lossyRule[T]); ok {
		return "lossy-wrapper"
	}
	rule = unwrapExecutionRule(rule)
	if strategy, ok := rule.(interface{ inspectionStrategy() string }); ok {
		return strategy.inspectionStrategy()
	}
	return "other"
}

// replayAllMaterializationBudget executes a benchmark-only, materialize-first
// All plan. Immutable planning postings are consumed directly; every other
// child is materialized once, so the counters expose the allocation budget a
// production planner can avoid. Candidate filters can be enabled to isolate
// their work without changing the real executor.
func replayAllMaterializationBudget[T any](
	root *allRule[T],
	value T,
	pool *bitmapPool,
	exclusions []exclusionRule[T],
	useCandidateFilters bool,
) materializationBudget {
	budget := materializationBudget{byRepresentation: make(map[string]materializationRepresentationBudget)}
	candidates := pool.get()
	liveScratch := uint64(1)
	budget.peakScratchBitmaps = liveScratch
	haveCandidates := false

	for i, child := range root.children {
		name := materializationRepresentation(child)
		if bits, found := root.lookupPlanningBitmap(i, child, value); found {
			bytes := bits.GetSerializedSizeInBytes()
			budget.acquisitions++
			budget.acquiredBytes += bytes
			budget.updateRepresentation(name, func(v *materializationRepresentationBudget) {
				v.acquisitions++
				v.acquiredBytes += bytes
			})
			if !haveCandidates {
				candidates.Or(bits)
				haveCandidates = true
			} else {
				candidates.And(bits)
				budget.intersections++
			}
			continue
		}
		if useCandidateFilters && haveCandidates {
			if filter, ok := unwrapExecutionRule(child).(candidateFilter[T]); ok {
				filter.filterCandidates(value, candidates, pool)
				budget.candidateFilters++
				budget.updateRepresentation(name, func(v *materializationRepresentationBudget) { v.candidateFilters++ })
				continue
			}
		}
		bits := pool.get()
		liveScratch++
		budget.peakScratchBitmaps = max(budget.peakScratchBitmaps, liveScratch)
		child.search(value, bits, pool)
		bytes := bits.GetSerializedSizeInBytes()
		budget.materializations++
		budget.materializedBytes += bytes
		budget.updateRepresentation(name, func(v *materializationRepresentationBudget) {
			v.materializations++
			v.materializedBytes += bytes
		})
		if !haveCandidates {
			candidates.Or(bits)
			haveCandidates = true
		} else {
			candidates.And(bits)
			budget.intersections++
		}
		pool.put(bits)
		liveScratch--
	}

	if len(exclusions) != 0 {
		excluded := pool.get()
		liveScratch++
		budget.peakScratchBitmaps = max(budget.peakScratchBitmaps, liveScratch)
		for _, exclusion := range exclusions {
			exclusion.exclude(value, excluded, pool)
			budget.exclusions++
		}
		candidates.AndNot(excluded)
		pool.put(excluded)
	}
	budget.finalCardinality = candidates.GetCardinality()
	budget.finalBytes = candidates.GetSerializedSizeInBytes()
	pool.put(candidates)
	return budget
}

type materializationBenchmarkValue struct {
	equality *int
	ordered  *int
	from     *int
	until    *int
	operator *Operator
	excluded *int
}

func materializationPtr[V any](value V) *V { return &value }

func materializationBenchmarkSchema(kind string) Rule[materializationBenchmarkValue] {
	include := Include(GetterFromPointer(func(v materializationBenchmarkValue) *int { return v.equality }))
	ordered := GreaterOrEqual(
		GetterFromPointer(func(v materializationBenchmarkValue) *int { return v.ordered }),
		cmp.Compare[int],
	)
	between := Between(
		GetterFromPointer(func(v materializationBenchmarkValue) *int { return v.from }),
		GetterFromPointer(func(v materializationBenchmarkValue) *int { return v.until }), cmp.Compare[int],
	)
	compareBy := CompareBy(
		GetterFromPointer(func(v materializationBenchmarkValue) *int { return v.ordered }),
		GetterFromPointer(func(v materializationBenchmarkValue) *Operator { return v.operator }), cmp.Compare[int],
	)
	exclude := Exclude(GetterFromPointer(func(v materializationBenchmarkValue) *int { return v.excluded }))
	switch kind {
	case "Equality":
		return All(include, include, include, include)
	case "Ordered":
		return All(include, ordered)
	case "Between":
		return All(include, between)
	case "CompareBy":
		return All(include, compareBy)
	case "NestedAll":
		return All(include, All(ordered, between), compareBy)
	case "Lossy":
		return All(include, Lossy(Include(GetterFromPointer(func(v materializationBenchmarkValue) *int {
			return v.ordered
		})), MemoryLimit(64<<10)))
	case "Exclusion":
		return All(include, ordered, exclude)
	default:
		return All(include, ordered, between, compareBy, All(include, ordered), exclude)
	}
}

func materializationBenchmarkData(entries int) ([]materializationBenchmarkValue, []uint32) {
	values := make([]materializationBenchmarkValue, entries)
	ids := make([]uint32, entries)
	for i := range values {
		ids[i] = uint32(i)
		values[i].equality = materializationPtr(i % 37)
		values[i].ordered = materializationPtr(i % 10_000)
		values[i].from = materializationPtr(i % 365)
		values[i].until = materializationPtr(i%365 + 30)
		values[i].operator = materializationPtr(OperatorGTE)
		if i%19 == 0 {
			values[i].excluded = materializationPtr(i % 11)
		}
	}
	return values, ids
}

func TestAllMaterializationBudgetAccounting(t *testing.T) {
	values, ids := materializationBenchmarkData(1_000)
	index, err := New[materializationBenchmarkValue, uint32](
		materializationBenchmarkSchema("Full"),
	).Build(Zip(values, ids))
	if err != nil {
		t.Fatal(err)
	}
	root := index.root.(*allRule[materializationBenchmarkValue])
	query := materializationBenchmarkValue{
		equality: materializationPtr(17), ordered: materializationPtr(500),
		from: materializationPtr(180), until: materializationPtr(190), excluded: materializationPtr(3),
	}
	materialized := replayAllMaterializationBudget(root, query, index.pool, index.exclusions, false)
	filtered := replayAllMaterializationBudget(root, query, index.pool, index.exclusions, true)
	if materialized.materializations == 0 || materialized.materializedBytes == 0 || materialized.intersections == 0 {
		t.Fatalf("materialization work was not accounted: %+v", materialized)
	}
	if materialized.exclusions != uint64(len(index.exclusions)) {
		t.Fatalf("accounted %d exclusions, want %d", materialized.exclusions, len(index.exclusions))
	}
	if materialized.peakScratchBitmaps < 2 || materialized.finalCardinality == 0 || materialized.finalBytes == 0 {
		t.Fatalf("scratch or final result was not accounted: %+v", materialized)
	}
	if filtered.candidateFilters == 0 || filtered.materializations >= materialized.materializations {
		t.Fatalf(
			"candidate filters did not replace materialization: materialized=%+v filtered=%+v",
			materialized,
			filtered,
		)
	}
	for _, representation := range []string{"equality", "ordered", "between", "compare-by"} {
		entry := materialized.representation(representation)
		if entry.materializations == 0 || entry.materializedBytes == 0 {
			t.Errorf("%s attribution is empty: %+v", representation, entry)
		}
	}
}

// BenchmarkAllMaterializationBudget last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkAllMaterializationBudget/' -benchtime=200ms -count=3 .
// Full: 6 materializations/op, 33,160 serialized materialized-B/op,
// 5 intersections/op, 1 exclusion/op, peak 2 scratch/op, 31 final IDs/op.
// FullCandidateFilters: 2 materializations/op, 4,152 serialized
// materialized-B/op, and 4 candidate filters/op.
// The representation sub-benchmarks keep attribution stable when a planner
// change moves work between equality, ordered, Between, CompareBy, nested All,
// exclusions, and candidate filtering.
func BenchmarkAllMaterializationBudget(b *testing.B) {
	const entries = 38_098
	values, ids := materializationBenchmarkData(entries)
	query := materializationBenchmarkValue{
		equality: materializationPtr(17), ordered: materializationPtr(5_000),
		from: materializationPtr(180), until: materializationPtr(190), excluded: materializationPtr(3),
	}
	kinds := []string{
		"Full", "Equality", "Ordered", "Between", "CompareBy", "NestedAll", "Lossy", "Exclusion",
	}
	for _, kind := range kinds {
		b.Run(kind, func(b *testing.B) {
			index, err := New[materializationBenchmarkValue, uint32](
				materializationBenchmarkSchema(kind),
			).Build(Zip(values, ids))
			if err != nil {
				b.Fatal(err)
			}
			root, ok := index.root.(*allRule[materializationBenchmarkValue])
			if !ok {
				b.Fatalf("expected All root, got %T", index.root)
			}
			b.ResetTimer()
			total := materializationBudget{byRepresentation: make(map[string]materializationRepresentationBudget)}
			for range b.N {
				budget := replayAllMaterializationBudget(root, query, index.pool, index.exclusions, false)
				total.acquisitions += budget.acquisitions
				total.acquiredBytes += budget.acquiredBytes
				total.materializations += budget.materializations
				total.materializedBytes += budget.materializedBytes
				total.intersections += budget.intersections
				total.exclusions += budget.exclusions
				total.peakScratchBitmaps += budget.peakScratchBitmaps
				total.finalCardinality += budget.finalCardinality
				total.finalBytes += budget.finalBytes
				for name, representation := range budget.byRepresentation {
					entry := total.byRepresentation[name]
					entry.acquisitions += representation.acquisitions
					entry.acquiredBytes += representation.acquiredBytes
					entry.materializations += representation.materializations
					entry.materializedBytes += representation.materializedBytes
					entry.candidateFilters += representation.candidateFilters
					total.byRepresentation[name] = entry
				}
			}
			b.StopTimer()
			n := float64(b.N)
			b.ReportMetric(float64(total.acquisitions)/n, "acquires/op")
			b.ReportMetric(float64(total.acquiredBytes)/n, "acquired-B/op")
			b.ReportMetric(float64(total.materializations)/n, "materializes/op")
			b.ReportMetric(float64(total.materializedBytes)/n, "materialized-B/op")
			b.ReportMetric(float64(total.intersections)/n, "intersections/op")
			b.ReportMetric(float64(total.exclusions)/n, "exclusions/op")
			b.ReportMetric(float64(total.peakScratchBitmaps)/n, "peak-scratch/op")
			b.ReportMetric(float64(total.finalCardinality)/n, "result-IDs/op")
			b.ReportMetric(float64(total.finalBytes)/n, "result-B/op")
			for name, representation := range total.byRepresentation {
				b.ReportMetric(float64(representation.materializations)/n, name+"-materializes/op")
				b.ReportMetric(float64(representation.materializedBytes)/n, name+"-materialized-B/op")
				b.ReportMetric(float64(representation.acquisitions)/n, name+"-acquires/op")
				b.ReportMetric(float64(representation.acquiredBytes)/n, name+"-acquired-B/op")
			}
		})
	}

	// The filter variant isolates candidate filtering from complete child
	// materialization on the same compiled production-sized index.
	b.Run("FullCandidateFilters", func(b *testing.B) {
		index, err := New[materializationBenchmarkValue, uint32](
			materializationBenchmarkSchema("Full"),
		).Build(Zip(values, ids))
		if err != nil {
			b.Fatal(err)
		}
		root := index.root.(*allRule[materializationBenchmarkValue])
		var filters, materializations, bytes uint64
		b.ResetTimer()
		for range b.N {
			budget := replayAllMaterializationBudget(root, query, index.pool, index.exclusions, true)
			filters += budget.candidateFilters
			materializations += budget.materializations
			bytes += budget.materializedBytes
		}
		b.StopTimer()
		n := float64(b.N)
		b.ReportMetric(float64(filters)/n, "filters/op")
		b.ReportMetric(float64(materializations)/n, "materializes/op")
		b.ReportMetric(float64(bytes)/n, "materialized-B/op")
	})
}
