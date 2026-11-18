package ruleix_test

import (
	"bytes"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func productionBatchCohort(
	b testing.TB,
	rows []productionDumpConstraint,
	ids []uuid.UUID,
	requests [][]productionDumpConstraint,
	cohort string,
) [][]productionDumpConstraint {
	b.Helper()
	// M1 Max, Go 1.27.3, GOMAXPROCS=1, 3 x 80,000 requests, 100 offers/Local.
	// v0.8.2 -> 086a91b mean: 0 candidates 10.279 -> 11.627 us;
	// 1-3 candidates 38.948 -> 18.887 us. See the production comparison report.
	require.Contains(b, []string{
		"narrow", "broad", "tiny", "medium", "zero", "storezero", "slotzero", "intersectzero", "alternating",
	}, cohort)
	minimum, maximum := 0, 8
	switch cohort {
	case "tiny":
		minimum, maximum = 1, 3
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
	if mutateBatchAbsentKeys(b, rows, requests, cohort) {
		return requests
	}
	if cohort == "intersectzero" {
		mutateBatchEmptyIntersection(b, rows, requests)
		return requests
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

func mutateBatchAbsentKeys(b testing.TB, rows []productionDumpConstraint,
	requests [][]productionDumpConstraint, cohort string,
) bool {
	if cohort != "zero" && cohort != "storezero" && cohort != "slotzero" && cohort != "alternating" {
		return false
	}
	absent := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	for _, row := range rows {
		require.NotEqual(b, absent, row.CustomerUUID)
	}
	for _, request := range requests {
		for i := range request {
			switch cohort {
			case "storezero":
				request[i].StoreUUID = absent
			case "slotzero":
				request[i].SlotType = "__absent_slot_type__"
			case "alternating":
				if i%2 == 0 {
					request[i].CustomerUUID = absent
				}
			default:
				request[i].CustomerUUID = absent
			}
		}
	}
	b.Logf("%s: %d requests with forced absent keys", cohort, len(requests))
	return true
}

func mutateBatchEmptyIntersection(b testing.TB, rows []productionDumpConstraint,
	requests [][]productionDumpConstraint,
) {
	stores := make(map[uuid.UUID]bool)
	for _, row := range rows {
		stores[row.StoreUUID] = true
	}
	for _, request := range requests {
		available := make(map[uuid.UUID]bool)
		for _, row := range rows {
			if row.CustomerUUID == request[0].CustomerUUID {
				available[row.StoreUUID] = true
			}
		}
		var absent uuid.UUID
		for store := range stores {
			if !available[store] && (absent == uuid.Nil || bytes.Compare(store[:], absent[:]) < 0) {
				absent = store
			}
		}
		require.NotEqual(b, uuid.Nil, absent)
		for i := range request {
			request[i].StoreUUID = absent
		}
	}
}
