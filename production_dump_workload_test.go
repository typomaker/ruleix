package ruleix_test

import (
	"bytes"
	"cmp"
	"os"
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

// Apple M1 Max, macOS 26.6.2, Go 1.27.3, GOMAXPROCS=1, -benchtime=2s,
// -count=3, 96,630 constraints: median 87,703 ns/request, 877.0 ns/offer,
// 2,392 B/request and 63 allocs/request. See docs/performance-history.md.
func BenchmarkProductionDumpLocalRequest100(b *testing.B) {
	path := os.Getenv("RULEIX_PRODUCTION_DUMP")
	if path == "" {
		b.Skip("set RULEIX_PRODUCTION_DUMP to the production JSON dump path")
	}
	input, err := os.Open(path)
	require.NoError(b, err)
	defer input.Close()

	rows, ids := readProductionDump(b, input)
	index, err := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema()).Build(ruleix.Zip(rows, ids))
	require.NoError(b, err)
	const requestCount = 64
	requests := productionDumpRequests(rows, requestCount)
	require.Len(b, requests, requestCount)
	for _, request := range requests {
		checkProductionDumpRequest(b, request)
		require.Len(b, request, productionDumpOffersPerRequest)
	}
	b.Logf("loaded %d constraints and generated %d requests", len(rows), len(requests))

	b.ReportAllocs()
	run := 0
	for b.Loop() {
		searchProductionDumpRequest(index, requests[run%len(requests)], nil)
		run++
	}
	b.ReportMetric(productionDumpOffersPerRequest, "offers/op")
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*productionDumpOffersPerRequest), "ns/offer")
}
