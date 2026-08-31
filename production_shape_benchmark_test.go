//nolint:lll // Production-shaped getters stay inline for benchmark fidelity.
package ruleix_test

import (
	"cmp"
	"encoding/binary"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/typomaker/ruleix"
)

const productionBenchmarkEntries = 38_098

type productionBenchmarkID [16]byte

type productionBenchmarkTimeRange struct {
	since time.Time
	until time.Time
}

type productionBenchmarkOrderCount struct {
	total int
}

type productionBenchmarkVersion struct {
	major int
	minor int
	patch int
}

type productionBenchmarkPlatform struct {
	name    string
	version *productionBenchmarkVersion
}

type productionBenchmarkConstraint struct {
	activity           *productionBenchmarkTimeRange
	customerOrderCount *productionBenchmarkOrderCount
	slotTime           *productionBenchmarkTimeRange
	customerUUID       *[16]byte
	customerSegment    *uint8
	customerFraud      *bool
	storeUUID          *[16]byte
	deliveryAreaID     *int
	regionID           *int
	retailerUUID       *[16]byte
	vertical           *uint8
	slotType           *uint8
	slotDayOfWeek      *int
	platform           *productionBenchmarkPlatform
	dbs                *bool
	marketType         *uint8
	abTest             *[2]string
}

var productionBenchmarkIndexResult *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID]

func productionBenchmarkSchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		productionBenchmarkActivityRule(),
		ruleix.CompareBy(func(value productionBenchmarkConstraint) (int, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return value.customerOrderCount.total, true
		}, func(value productionBenchmarkConstraint) (ruleix.Operator, bool) {
			if value.customerOrderCount == nil {
				return 0, false
			}
			return ruleix.OperatorGTE, true
		}, cmp.Compare[int],
		),
		productionBenchmarkSlotTimeRule(),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.customerUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.customerSegment)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (bool, bool) { return benchmarkOptional(value.customerFraud) }),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) { return benchmarkOptional(value.storeUUID) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) { return benchmarkOptional(value.deliveryAreaID) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) { return benchmarkOptional(value.regionID) }),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.retailerUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) { return benchmarkOptional(value.vertical) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) { return benchmarkOptional(value.slotType) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) { return benchmarkOptional(value.slotDayOfWeek) }),
		productionBenchmarkPlatformRule(),
		ruleix.Include(func(value productionBenchmarkConstraint) (bool, bool) { return benchmarkOptional(value.dbs) }),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) { return benchmarkOptional(value.marketType) }),
		ruleix.Include(func(value productionBenchmarkConstraint) ([2]string, bool) { return benchmarkOptional(value.abTest) }),
	)
}

// productionBenchmarkEqualityOnlySchema is a control shape for separating the
// cost of Include planning from ordered range planning on the same data set.
func productionBenchmarkEqualityOnlySchema() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.customerUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.customerSegment)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (bool, bool) {
			return benchmarkOptional(value.customerFraud)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.storeUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(value.deliveryAreaID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(value.regionID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(value.retailerUUID)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.vertical)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.slotType)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(value.slotDayOfWeek)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (string, bool) {
			if value.platform == nil {
				return "", false
			}
			return value.platform.name, true
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (bool, bool) {
			return benchmarkOptional(value.dbs)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(value.marketType)
		}),
		ruleix.Include(func(value productionBenchmarkConstraint) ([2]string, bool) {
			return benchmarkOptional(value.abTest)
		}),
	)
}

func benchmarkOptional[V any](value *V) (V, bool) {
	if value == nil {
		var zero V
		return zero, false
	}
	return *value, true
}

func productionBenchmarkActivityRule() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.Between(func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.activity == nil {
			return time.Time{}, false
		}
		return value.activity.since.Truncate(time.Second), true
	}, func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.activity == nil {
			return time.Time{}, false
		}
		return value.activity.until.Truncate(time.Second), true
	}, time.Time.Compare,
	)
}

func productionBenchmarkSlotTimeRule() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.Between(func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.slotTime == nil {
			return time.Time{}, false
		}
		return value.slotTime.since.Truncate(time.Second), true
	}, func(value productionBenchmarkConstraint) (time.Time, bool) {
		if value.slotTime == nil {
			return time.Time{}, false
		}
		return value.slotTime.until.Truncate(time.Second), true
	}, time.Time.Compare,
	)
}

func productionBenchmarkPlatformRule() ruleix.Rule[productionBenchmarkConstraint] {
	return ruleix.All(
		ruleix.Include(func(value productionBenchmarkConstraint) (string, bool) {
			if value.platform == nil {
				return "", false
			}
			return value.platform.name, true
		}),
		ruleix.CompareBy(func(value productionBenchmarkConstraint) ([3]int, bool) {
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
		},
		),
	)
}

func productionBenchmarkData() ([]productionBenchmarkConstraint, []productionBenchmarkID) {
	return productionBenchmarkDataN(productionBenchmarkEntries)
}

func productionBenchmarkDataN(entries int) ([]productionBenchmarkConstraint, []productionBenchmarkID) {
	constraints := make([]productionBenchmarkConstraint, entries)
	ids := make([]productionBenchmarkID, entries)
	customerUUIDs := make([][16]byte, 536)
	storeUUIDs := make([][16]byte, 179)
	for i := range customerUUIDs {
		binary.BigEndian.PutUint64(customerUUIDs[i][8:], uint64(i))
	}
	for i := range storeUUIDs {
		binary.BigEndian.PutUint64(storeUUIDs[i][8:], uint64(i))
	}
	baseTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for i := range constraints {
		binary.BigEndian.PutUint64(ids[i][8:], uint64(i))
		if i < productionBenchmarkScaledCount(entries, 38_074) {
			day := i % 365
			constraints[i].activity = &productionBenchmarkTimeRange{
				since: baseTime.AddDate(0, 0, day),
				until: baseTime.AddDate(0, 0, day+7),
			}
		}
		if i < productionBenchmarkScaledCount(entries, 38_095) {
			constraints[i].customerOrderCount = &productionBenchmarkOrderCount{total: 1}
		}
		if i < productionBenchmarkScaledCount(entries, 33_176) {
			constraints[i].customerUUID = &customerUUIDs[i%len(customerUUIDs)]
			constraints[i].slotType = ptr(uint8(i%2 + 1))
		}
		if i < productionBenchmarkScaledCount(entries, 4_919) {
			constraints[i].storeUUID = &storeUUIDs[i%len(storeUUIDs)]
		}
		if i < productionBenchmarkScaledCount(entries, 24) {
			constraints[i].regionID = ptr(1)
		}
		if i < productionBenchmarkScaledCount(entries, 38_097) {
			constraints[i].platform = &productionBenchmarkPlatform{name: productionPlatformName(i % 34)}
			if i < productionBenchmarkScaledCount(entries, 6_163) {
				constraints[i].platform.version = &productionBenchmarkVersion{major: i%3 + 1}
			}
		}
		if i < productionBenchmarkScaledCount(entries, 38_074) {
			constraints[i].dbs = ptr(i%2 == 0)
			constraints[i].marketType = ptr(uint8(1))
		}
	}
	return constraints, ids
}

func productionBenchmarkScaledCount(entries, baseline int) int {
	return (entries*baseline + productionBenchmarkEntries - 1) / productionBenchmarkEntries
}

func productionPlatformName(value int) string {
	return string([]byte{'p', byte('a' + value/26), byte('a' + value%26)})
}

func productionBenchmarkQuery(day int) productionBenchmarkConstraint {
	baseTime := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return productionBenchmarkConstraint{
		activity:           &productionBenchmarkTimeRange{since: baseTime.AddDate(0, 0, day)},
		customerOrderCount: &productionBenchmarkOrderCount{total: 1},
		customerUUID:       ptr([16]byte{}),
		storeUUID:          ptr([16]byte{}),
		regionID:           ptr(1),
		slotType:           ptr(uint8(1)),
		platform: &productionBenchmarkPlatform{
			name: "paa", version: &productionBenchmarkVersion{major: 2},
		},
		dbs:        ptr(true),
		marketType: ptr(uint8(1)),
	}
}

// BenchmarkProductionShapeSearch last local run (Apple M1 Max, Go 1.26.0,
// GOMAXPROCS=1):
// go test -run '^$' -bench '^BenchmarkProductionShapeSearch/(Index|Local)$' -benchmem -benchtime=1s -count=5 .
// Medians: Index 35,580 ns/op, 40,805 B/op, 28 allocs/op;
// Local 227.9 ns/op, 0 B/op, 0 allocs/op.
func BenchmarkProductionShapeSearch(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for _, local := range []bool{false, true} {
		name := "Index"
		if local {
			name = "Local"
		}
		b.Run(name, func(b *testing.B) {
			searcher := index.Local()
			b.Cleanup(searcher.Close)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				query := queries[i%len(queries)]
				matches = matches[:0]
				if local {
					searcher.Search(query, &matches)
				} else {
					index.Search(query, &matches)
				}
			}
		})
	}
}

// The benchmark drives one search worker. Production code must retain the
// documented one-Local-per-goroutine rule rather than sharing this holder's
// Local between concurrent readers.
type productionBenchmarkPublishedLocal struct {
	mu    sync.RWMutex
	local *ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID]
}

func (published *productionBenchmarkPublishedLocal) replace(
	next *ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID],
) *ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID] {
	published.mu.Lock()
	previous := published.local
	published.local = next
	published.mu.Unlock()
	return previous
}

func (published *productionBenchmarkPublishedLocal) search(
	query productionBenchmarkConstraint,
	matches *[]productionBenchmarkID,
) bool {
	published.mu.RLock()
	matched := published.local.Search(query, matches)
	published.mu.RUnlock()
	return matched
}

// BenchmarkProductionShapeLocalAfterSequentialBuilds detects generation-based
// Local.Search degradation while repeatedly building and publishing independent
// indexes. Build is outside every timed region. A replacement creates the next
// Local before taking the write lock, swaps it under the lock, and closes the
// previous Local after releasing the lock, matching the production lifecycle.
// Direct isolates Local.Search; Published also includes the uncontended read
// lock used to load the current Local.
//
// Reproduce with:
//
//	GOMAXPROCS=1 go test -run '^$' \
//	  -bench '^BenchmarkProductionShapeLocalAfterSequentialBuilds/' \
//	  -benchmem -benchtime=500ms -count=5 .
//
// Latest local run (Apple M1 Max, Go 1.26.0, GOMAXPROCS=1): generation 1 to
// 64 Direct medians 227.0-228.5 ns/op; Published medians 234.2-235.3 ns/op;
// every generation remained at 0 B/op and 0 allocs/op.
func BenchmarkProductionShapeLocalAfterSequentialBuilds(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	)
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	var published productionBenchmarkPublishedLocal
	var current *ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID]
	b.Cleanup(func() {
		if previous := published.replace(nil); previous != nil {
			previous.Close()
		}
		runtime.KeepAlive(current)
	})

	built := 0
	for _, generation := range [...]int{1, 2, 4, 8, 16, 32, 64} {
		for built < generation {
			next, err := builder.Build(ruleix.Zip(constraints, ids))
			if err != nil {
				b.Fatal(err)
			}
			nextLocal := next.Local()
			previous := published.replace(nextLocal)
			if previous != nil {
				previous.Close()
			}
			current = next
			built++
		}

		local := published.local
		matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
		for warm := range 4 {
			matches = matches[:0]
			published.search(queries[warm%len(queries)], &matches)
		}
		if len(matches) == 0 {
			b.Fatalf("generation %d: warm search returned no matches", generation)
		}

		b.Run(fmt.Sprintf("Generation%02d/Direct", generation), func(b *testing.B) {
			b.ReportAllocs()
			for i := range b.N {
				matches = matches[:0]
				local.Search(queries[i%len(queries)], &matches)
			}
		})
		b.Run(fmt.Sprintf("Generation%02d/Published", generation), func(b *testing.B) {
			b.ReportAllocs()
			for i := range b.N {
				matches = matches[:0]
				published.search(queries[i%len(queries)], &matches)
			}
		})
		runtime.KeepAlive(current)
	}
}

// BenchmarkProductionShapeExactCacheEntryHit isolates lookup of each of the
// two exact-query cache entries. The warm-up alternates the production queries
// so query 100 occupies the first entry and query 101 the second entry.
//
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkProductionShapeExactCacheEntryHit/' -benchmem -benchtime=1s -count=7 .
func BenchmarkProductionShapeExactCacheEntryHit(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for entry, query := range queries {
		name := "FirstEntry"
		if entry == 1 {
			name = "SecondEntry"
		}
		b.Run(name, func(b *testing.B) {
			local := index.Local()
			b.Cleanup(local.Close)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			for warm := range 4 {
				matches = matches[:0]
				local.Search(queries[warm%len(queries)], &matches)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				matches = matches[:0]
				local.Search(query, &matches)
			}
		})
	}
}

// BenchmarkProductionShapeLocalClose measures a complete short-lived Local
// lifecycle: acquire a context, warm its two-value working set, and return the
// context to the Index. This keeps cache teardown and subsequent resource reuse
// visible instead of amortizing them across a long-lived Local.
// Last local run (Apple M1 Max): 298,957 ns/op, 293,341 B/op, 110 allocs/op.
// Reproduce with:
//
//	go test -run '^$' -bench '^BenchmarkProductionShapeLocalClose$' -benchmem -benchtime=1s -count=5 .
func BenchmarkProductionShapeLocalClose(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)

	b.ReportAllocs()
	b.ReportMetric(float64(len(queries)*3), "searches/op")
	b.ResetTimer()
	for range b.N {
		local := index.Local()
		for range 3 {
			for _, query := range queries {
				matches = matches[:0]
				local.Search(query, &matches)
			}
		}
		local.Close()
	}
}

// BenchmarkProductionShapeEqualityOnlySearch last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkProductionShapeEqualityOnlySearch$' -benchmem -benchtime=500ms -count=5 .
// Local median: 708.5 ns/op, 0 B/op, 0 allocs/op.
func BenchmarkProductionShapeEqualityOnlySearch(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkEqualityOnlySchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
	}
	for _, local := range []bool{false, true} {
		name := "Index"
		if local {
			name = "Local"
		}
		b.Run(name, func(b *testing.B) {
			searcher := index.Local()
			b.Cleanup(searcher.Close)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			b.ReportAllocs()
			b.ResetTimer()
			for i := range b.N {
				query := queries[i%len(queries)]
				matches = matches[:0]
				if local {
					searcher.Search(query, &matches)
				} else {
					index.Search(query, &matches)
				}
			}
		})
	}
}

// BenchmarkProductionShapeParallelLocalBatch100 last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkProductionShapeParallelLocalBatch100$' -benchmem -benchtime=500ms -count=5 .
// Median: 46,506 ns/op, 465.1 ns/search, 370 B/op, 1 alloc/op.
func BenchmarkProductionShapeParallelLocalBatch100(b *testing.B) {
	const (
		workers           = 5
		searchesPerWorker = 20
		searchesPerBatch  = workers * searchesPerWorker
	)
	type job struct {
		queries []productionBenchmarkConstraint
		done    *sync.WaitGroup
	}

	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}

	queries := make([]productionBenchmarkConstraint, searchesPerBatch)
	for i := range queries {
		queries[i] = productionBenchmarkQuery(100 + i%2)
	}

	jobs := make([]chan job, workers)
	var workersDone sync.WaitGroup
	workersDone.Add(workers)
	for worker := range workers {
		jobs[worker] = make(chan job, 1)
		go func(jobs <-chan job) {
			defer workersDone.Done()
			local := index.Local()
			defer local.Close()
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			for work := range jobs {
				for _, query := range work.queries {
					matches = matches[:0]
					local.Search(query, &matches)
				}
				work.done.Done()
			}
		}(jobs[worker])
	}
	defer func() {
		for _, workerJobs := range jobs {
			close(workerJobs)
		}
		workersDone.Wait()
	}()

	b.ReportAllocs()
	b.ReportMetric(searchesPerBatch, "searches/op")
	b.ResetTimer()
	for range b.N {
		var batchDone sync.WaitGroup
		batchDone.Add(workers)
		for worker, workerJobs := range jobs {
			from := worker * searchesPerWorker
			workerJobs <- job{
				queries: queries[from : from+searchesPerWorker],
				done:    &batchDone,
			}
		}
		batchDone.Wait()
	}
	b.StopTimer()
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*searchesPerBatch), "ns/search")
}

func BenchmarkProductionShapeBuild(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema())
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		index, err := builder.Build(ruleix.Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		productionBenchmarkIndexResult = index
	}
	b.ReportMetric(productionBenchmarkEntries, "entries/op")
}

// BenchmarkProductionShapeRetainedMemory measures the live heap owned by a
// completed index rather than the allocation traffic reported by B/op. Run it
// with a fixed iteration count so all indexes can remain live until the final
// heap sample, for example:
//
//	go test -run '^$' -bench '^BenchmarkProductionShapeRetainedMemory$' -benchtime=5x -count=5
func BenchmarkProductionShapeRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	benchmarkProductionRetainedMemory(b, constraints, ids)
}

// BenchmarkProductionShapeShuffledRetainedMemory removes insertion-order
// locality while preserving the same production field distribution.
func BenchmarkProductionShapeShuffledRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	state := uint64(0x9e3779b97f4a7c15)
	for i := len(constraints) - 1; i > 0; i-- {
		state = state*6364136223846793005 + 1442695040888963407
		j := int(state % uint64(i+1))
		constraints[i], constraints[j] = constraints[j], constraints[i]
		ids[i], ids[j] = ids[j], ids[i]
	}
	benchmarkProductionRetainedMemory(b, constraints, ids)
}

func benchmarkProductionRetainedMemory(
	b *testing.B,
	constraints []productionBenchmarkConstraint,
	ids []productionBenchmarkID,
) {
	builder := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](productionBenchmarkSchema())
	// Prime reusable Builder hints before the baseline so their retained memory
	// is not incorrectly attributed to the measured indexes.
	if _, err := builder.Build(ruleix.Zip(constraints, ids)); err != nil {
		b.Fatal(err)
	}
	indexes := make([]*ruleix.Index[productionBenchmarkConstraint, productionBenchmarkID], b.N)

	runtime.GC()
	runtime.GC() // A second cycle also drops objects retained by sync.Pool.
	before := heapAlloc()

	b.ResetTimer()
	for i := range b.N {
		index, err := builder.Build(ruleix.Zip(constraints, ids))
		if err != nil {
			b.Fatal(err)
		}
		indexes[i] = index
	}
	b.StopTimer()

	runtime.GC()
	runtime.GC()
	after := heapAlloc()
	runtime.KeepAlive(constraints)
	runtime.KeepAlive(ids)
	runtime.KeepAlive(builder)
	runtime.KeepAlive(indexes)

	var retained uint64
	if after > before {
		retained = after - before
	}
	perIndex := float64(retained) / float64(b.N)
	b.ReportMetric(perIndex, "retained-B/index")
	b.ReportMetric(perIndex/float64(len(constraints)), "retained-B/ID")
}

// BenchmarkProductionShapeLocalRetainedMemory measures the incremental live
// heap of one Local while keeping its shared Index and caller-owned result
// storage in the baseline. Warm alternates the same two queries as the search
// benchmark, while Adaptive cycles four queries to exercise cache growth.
// Last local run (Apple M1 Max):
// go test -run '^$' -bench '^BenchmarkProductionShapeLocalRetainedMemory$' -benchmem -benchtime=3x -count=3 .
// Cold: 2,968; Warm: 91,003; Adaptive: 107,845; Adversarial: 73,984
// retained-B/local.
//
//nolint:gocognit // The retained-memory cases must share identical fixture construction.
func BenchmarkProductionShapeLocalRetainedMemory(b *testing.B) {
	constraints, ids := productionBenchmarkData()
	index, err := ruleix.New[productionBenchmarkConstraint, productionBenchmarkID](
		productionBenchmarkSchema(),
	).Build(ruleix.Zip(constraints, ids))
	if err != nil {
		b.Fatal(err)
	}
	queries := [...]productionBenchmarkConstraint{
		productionBenchmarkQuery(100),
		productionBenchmarkQuery(101),
		productionBenchmarkQuery(102),
		productionBenchmarkQuery(103),
	}

	for _, mode := range []struct {
		name    string
		queries int
	}{{"Cold", 0}, {"Warm", 2}, {"Adaptive", 4}, {"Adversarial", -1}} {
		name := mode.name
		b.Run(name, func(b *testing.B) {
			locals := make([]*ruleix.Local[productionBenchmarkConstraint, productionBenchmarkID], b.N)
			matches := make([]productionBenchmarkID, 0, productionBenchmarkEntries)
			runtime.GC()
			runtime.GC()
			before := heapAlloc()

			b.ResetTimer()
			for i := range b.N {
				local := index.Local()
				if mode.queries < 0 {
					for value := 100; value < 356; value++ {
						matches = matches[:0]
						local.Search(productionBenchmarkQuery(value), &matches)
					}
				} else if mode.queries > 0 {
					for range 6 {
						for _, query := range queries[:mode.queries] {
							matches = matches[:0]
							local.Search(query, &matches)
						}
					}
				}
				locals[i] = local
			}
			b.StopTimer()

			runtime.GC()
			runtime.GC()
			after := heapAlloc()
			runtime.KeepAlive(index)
			runtime.KeepAlive(locals)
			runtime.KeepAlive(matches)

			var retained uint64
			if after > before {
				retained = after - before
			}
			b.ReportMetric(float64(retained)/float64(b.N), "retained-B/local")
			for _, local := range locals {
				local.Close()
			}
		})
	}
}

func heapAlloc() uint64 {
	var statistics runtime.MemStats
	runtime.ReadMemStats(&statistics)
	return statistics.HeapAlloc
}
