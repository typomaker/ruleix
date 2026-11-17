package ruleix_test

import (
	"bytes"
	"cmp"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

const productionDumpOffersPerRequest = 100

type productionDumpSlot struct {
	productionDumpOffer
	at time.Time
}

func (slot productionDumpSlot) changes(other productionDumpSlot) int {
	count := 0
	for _, changed := range [...]bool{
		slot.StoreUUID != other.StoreUUID,
		slot.SlotDay != other.SlotDay,
		slot.SlotType != other.SlotType,
		!slot.at.Equal(other.at),
	} {
		if changed {
			count++
		}
	}
	return count
}

func productionDumpSlotFor(row productionDumpConstraint) productionDumpSlot {
	at := row.SlotTime.since.Add(row.SlotTime.until.Sub(row.SlotTime.since) / 2)
	return productionDumpSlot{row.productionDumpOffer, at.Truncate(time.Second).UTC()}
}

func productionDumpRequests(rows []productionDumpConstraint, limit int) [][]productionDumpConstraint {
	var activity time.Time
	unique := make(map[productionDumpSlot]struct{})
	var slots []productionDumpSlot
	for _, row := range rows {
		if row.Activity.since.After(activity) {
			activity = row.Activity.since
		}
		slot := productionDumpSlotFor(row)
		if _, exists := unique[slot]; !exists {
			unique[slot] = struct{}{}
			slots = append(slots, slot)
		}
	}
	slices.SortFunc(slots, func(a, b productionDumpSlot) int {
		return cmp.Or(bytes.Compare(a.StoreUUID[:], b.StoreUUID[:]),
			cmp.Compare(a.SlotDay, b.SlotDay), cmp.Compare(a.SlotType, b.SlotType), a.at.Compare(b.at))
	})
	activity = activity.Truncate(time.Second)

	positions := make(map[productionDumpSlot]int, len(slots))
	for i, slot := range slots {
		positions[slot] = i
	}

	// Spread request seeds across the input and skip repeated customers.
	stride := max(1, len(rows)/limit)
	seen := make(map[uuid.UUID]bool)
	var requests [][]productionDumpConstraint
	for i := 0; i < len(rows) && len(requests) < limit; i += stride {
		row := rows[i]
		if seen[row.CustomerUUID] {
			continue
		}
		seen[row.CustomerUUID] = true
		start := positions[productionDumpSlotFor(row)]
		var request []productionDumpConstraint
		var previous productionDumpSlot
		for offset := range slots {
			slot := slots[(start+offset)%len(slots)]
			if offset != 0 && previous.changes(slot) > 3 {
				continue
			}
			request = append(request, productionDumpConstraint{
				productionDumpOffer: slot.productionDumpOffer,
				CustomerUUID:        row.CustomerUUID,
				Activity:            productionDumpRange{activity, activity},
				SlotTime:            productionDumpRange{slot.at, slot.at},
			})
			previous = slot
			if len(request) == productionDumpOffersPerRequest {
				break
			}
		}
		requests = append(requests, request)
	}
	return requests
}

func searchProductionDumpRequest(
	index *ruleix.Index[productionDumpConstraint, uuid.UUID],
	request []productionDumpConstraint,
	visit func(int, []uuid.UUID),
) {
	local := index.Local()
	defer local.Close()
	searchProductionDumpOffers(local, request, visit)
}

func searchProductionDumpOffers(
	local *ruleix.Local[productionDumpConstraint, uuid.UUID],
	request []productionDumpConstraint,
	visit func(int, []uuid.UUID),
) {
	var matches []uuid.UUID
	for i, offer := range request {
		matches = matches[:0]
		local.Search(offer, &matches)
		if visit != nil {
			visit(i, matches)
		}
	}
}

func checkProductionDumpRequest(t testing.TB, request []productionDumpConstraint) {
	t.Helper()
	require.NotEmpty(t, request)
	require.LessOrEqual(t, len(request), productionDumpOffersPerRequest)
	for i, offer := range request {
		require.Equal(t, request[0].CustomerUUID, offer.CustomerUUID)
		require.Equal(t, request[0].Activity, offer.Activity)
		if i > 0 {
			changes := productionDumpSlotFor(offer).changes(productionDumpSlotFor(request[i-1]))
			require.GreaterOrEqual(t, changes, 1)
			require.LessOrEqual(t, changes, 3)
		}
	}
}

func TestProductionDumpLocalRequest(t *testing.T) {
	const fixture = `[
		{"modifier_uuid":"00000000-0000-0000-0000-000000000001","customer_uuid":"00000000-0000-0000-0000-000000000011","store_uuid":"00000000-0000-0000-0000-000000000021",
		 "activity":"(\"2026-11-17 12:00:00+03\",\"2026-11-17 19:00:00+03\")","slot_time":"(\"2021-01-01 16:00:00+03\",\"2021-01-01 19:00:00+03\")","slot_day_of_week":"Tuesday","slot_type":"planned","ignored":{"extra":true}},
		{"modifier_uuid":"00000000-0000-0000-0000-000000000001","customer_uuid":"00000000-0000-0000-0000-000000000011","store_uuid":"00000000-0000-0000-0000-000000000021",
		 "activity":"(\"2026-11-17 12:30:00+03\",\"2026-11-17 19:00:00+03\")","slot_time":"(\"2021-01-01 16:00:00+03\",\"2021-01-01 19:00:00+03\")","slot_day_of_week":"Tuesday","slot_type":"planned"},
		{"modifier_uuid":"00000000-0000-0000-0000-000000000002","customer_uuid":"00000000-0000-0000-0000-000000000011","store_uuid":"00000000-0000-0000-0000-000000000021",
		 "activity":"(\"2026-11-17 12:00:00+03\",\"2026-11-17 19:00:00+03\")","slot_time":"(\"2021-01-01 19:00:00+03\",\"2021-01-01 22:00:00+03\")","slot_day_of_week":"Tuesday","slot_type":"planned"},
		{"modifier_uuid":"00000000-0000-0000-0000-000000000003","customer_uuid":"00000000-0000-0000-0000-000000000012","store_uuid":"00000000-0000-0000-0000-000000000022",
		 "activity":"(\"2026-11-17 12:00:00+03\",\"2026-11-17 19:00:00+03\")","slot_time":"(\"2021-01-01 16:00:00+03\",\"2021-01-01 19:00:00+03\")","slot_day_of_week":"Wednesday","slot_type":"planned"}
	]`
	rows, ids := readProductionDump(t, strings.NewReader(fixture))
	require.Len(t, rows, 4)
	index, err := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema()).Build(ruleix.Zip(rows, ids))
	require.NoError(t, err)

	request := productionDumpRequests(rows, 1)[0]
	checkProductionDumpRequest(t, request)
	want := [][]uuid.UUID{{ids[0]}, {ids[2]}, nil}
	require.Len(t, request, len(want))
	searchProductionDumpRequest(index, request, func(i int, matches []uuid.UUID) {
		require.ElementsMatch(t, want[i], matches, "offer %d", i)
	})
}

// Apple M1 Max, macOS 26.6.2, Go 1.27.3, GOMAXPROCS=1, 96,630 rows.
// Three 80,000-request samples with portable prepared bounds measured 31.01 µs
// mean, 77.81 µs p99.9, 845 B and 12 allocations per mixed 100-offer request.
// See docs/local-between-go.md for cohort, rotated and pre-assembly controls.
func BenchmarkProductionDumpLocalRequest100(b *testing.B) {
	rows, ids := loadProductionDumpBenchmark(b)
	index, err := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema()).Build(ruleix.Zip(rows, ids))
	require.NoError(b, err)
	const requestCount = 64
	requests := productionDumpRequests(rows, requestCount)
	require.Len(b, requests, requestCount)
	for _, request := range requests {
		checkProductionDumpRequest(b, request)
		require.Len(b, request, productionDumpOffersPerRequest)
	}
	if os.Getenv("RULEIX_PLANNER_ROTATE") == "1" {
		for _, request := range requests {
			ordered := slices.Clone(request)
			for i := range request {
				request[i] = ordered[(i*37)%productionDumpOffersPerRequest]
			}
		}
	}
	b.Logf("loaded %d constraints and generated %d requests", len(rows), len(requests))

	if cohort := os.Getenv("RULEIX_PLANNER_BUCKET"); cohort != "" {
		requests = productionDumpCohort(b, rows, ids, requests, cohort)
	}
	b.ReportAllocs()
	samples := make([]time.Duration, 0, 100_000)
	run := 0
	for b.Loop() {
		start := time.Now()
		searchProductionDumpRequest(index, requests[run%len(requests)], nil)
		samples = append(samples, time.Since(start))
		run++
	}
	slices.Sort(samples)
	b.ReportMetric(float64(samples[(99*len(samples)+99)/100-1].Nanoseconds()), "p99-ns/request")
	b.ReportMetric(float64(samples[(999*len(samples)+999)/1000-1].Nanoseconds()), "p99.9-ns/request")
	b.ReportMetric(productionDumpOffersPerRequest, "offers/op")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*productionDumpOffersPerRequest), "ns/offer")
}

func productionDumpCohort(
	b *testing.B,
	rows []productionDumpConstraint,
	ids []uuid.UUID,
	requests [][]productionDumpConstraint,
	cohort string,
) [][]productionDumpConstraint {
	b.Helper()
	require.Contains(b, []string{"narrow", "broad", "tiny", "medium"}, cohort)
	minimum, maximum := 0, 8
	switch cohort {
	case "tiny":
		minimum, maximum = 1, 4
	case "medium":
		minimum = 5
	case "broad":
		minimum, maximum = 9, 16
	}
	candidates := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(requests))
	for _, request := range requests {
		candidates[request[0].CustomerUUID] = make(map[uuid.UUID]struct{})
	}
	for i, row := range rows {
		if buyerIDs := candidates[row.CustomerUUID]; buyerIDs != nil {
			buyerIDs[ids[i]] = struct{}{}
		}
	}
	selected := make([][]productionDumpConstraint, 0, len(requests))
	for _, request := range requests {
		size := len(candidates[request[0].CustomerUUID])
		if size >= minimum && size <= maximum {
			selected = append(selected, request)
		}
	}
	require.NotEmpty(b, selected)
	b.Logf("selected %d requests for the %s cohort", len(selected), cohort)
	return selected
}

// Keep each Local live until measurement ends so every request acquires fresh
// storage. On Apple M1 Max, Go 1.27.3, GOMAXPROCS=1, two 1000x samples
// measured 51.89 µs, 3,829 B, and 26 allocs per request.
func BenchmarkProductionDumpFreshLocal100(b *testing.B) {
	benchmarkProductionDumpFreshLocal(b, false)
}

// Keep the search contexts alive over the final heap sample. The 409a51b
// baseline and the candidate measured 4,355 and 3,427 B per Local at
// 1000x on Apple M1 Max, Go 1.27.3, GOMAXPROCS=1.
func BenchmarkProductionDumpLocalRetainedProbe(b *testing.B) {
	benchmarkProductionDumpFreshLocal(b, true)
}

func benchmarkProductionDumpFreshLocal(b *testing.B, retained bool) {
	rows, ids := loadProductionDumpBenchmark(b)
	index, err := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema()).Build(ruleix.Zip(rows, ids))
	require.NoError(b, err)
	requests := productionDumpRequests(rows, 64)
	require.Len(b, requests, 64)
	locals := make([]*ruleix.Local[productionDumpConstraint, uuid.UUID], b.N)
	var before uint64
	if retained {
		runtime.GC()
		runtime.GC()
		before = heapAlloc()
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := range locals {
		local := index.Local()
		searchProductionDumpOffers(local, requests[i%len(requests)], nil)
		locals[i] = local
	}
	b.StopTimer()
	if retained {
		runtime.GC()
		runtime.GC()
		after := heapAlloc()
		runtime.KeepAlive(index)
		runtime.KeepAlive(requests)
		b.ReportMetric(float64(int64(after)-int64(before))/float64(b.N), "retained-B/Local")
	}
	for _, local := range locals {
		local.Close()
	}
}

func loadProductionDumpBenchmark(b *testing.B) ([]productionDumpConstraint, []uuid.UUID) {
	b.Helper()
	path := os.Getenv("RULEIX_PRODUCTION_DUMP")
	if path == "" {
		b.Skip("set RULEIX_PRODUCTION_DUMP to the production JSON dump path")
	}
	input, err := os.Open(path)
	require.NoError(b, err)
	defer input.Close()
	return readProductionDump(b, input)
}

// Apple M1 Max, Go 1.27.3, GOMAXPROCS=1, -benchtime=12x, two samples:
// 152.37 ms and 46,366 allocations/op. See docs/build-and-local-lookup.md.
func BenchmarkProductionDumpBuildProbe(b *testing.B) {
	rows, ids := loadProductionDumpBenchmark(b)
	builder := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema())
	b.ReportAllocs()
	for b.Loop() {
		index, err := builder.Build(ruleix.Zip(rows, ids))
		require.NoError(b, err)
		runtime.KeepAlive(index)
	}
}

// Use a fixed count, such as -benchtime=12x. All indexes remain live until the
// final heap sample. Apple M1 Max, Go 1.27.3, GOMAXPROCS=1, 12x, two
// samples: about 2.58 MB/index. See docs/build-and-local-lookup.md.
func BenchmarkProductionDumpRetainedProbe(b *testing.B) {
	rows, ids := loadProductionDumpBenchmark(b)
	indexes := make([]*ruleix.Index[productionDumpConstraint, uuid.UUID], b.N)
	builder := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema())
	runtime.GC()
	runtime.GC()
	before := heapAlloc()
	b.ReportAllocs()
	b.ResetTimer()
	for i := range indexes {
		index, err := builder.Build(ruleix.Zip(rows, ids))
		require.NoError(b, err)
		indexes[i] = index
	}
	b.StopTimer()
	runtime.GC()
	runtime.GC()
	after := heapAlloc()
	runtime.KeepAlive(rows)
	runtime.KeepAlive(ids)
	runtime.KeepAlive(indexes)
	b.ReportMetric(float64(int64(after)-int64(before))/float64(b.N), "retained-B/index")
}
