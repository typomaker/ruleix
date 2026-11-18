package ruleix_test

import (
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

// The production workload uses Local throughout. Index.SearchOne provides the
// independent, non-Local result for each offer in this opt-in differential test.
func TestProductionDumpLocalMatchesIndex(t *testing.T) {
	path := os.Getenv("RULEIX_PRODUCTION_DUMP")
	if path == "" {
		t.Skip("set RULEIX_PRODUCTION_DUMP to the production JSON dump path")
	}
	input, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, input.Close()) }()
	rows, ids := readProductionDump(t, input)

	index, err := ruleix.New[productionDumpConstraint, uuid.UUID](productionDumpSchema()).Build(ruleix.Zip(rows, ids))
	require.NoError(t, err)
	requests := productionDumpRequests(rows, 64)
	require.Len(t, requests, 64)
	for _, rotating := range []bool{false, true} {
		label := "Grouped"
		if rotating {
			label = "Rotating"
		}
		t.Run(label, func(t *testing.T) {
			var want []uuid.UUID
			for requestIndex, request := range requests {
				require.Len(t, request, productionDumpOffersPerRequest)
				if rotating {
					original := request
					request = slices.Clone(original)
					for i := range original {
						request[i] = original[(i*37)%len(original)]
					}
				}
				searchProductionDumpRequest(index, request, func(offerIndex int, got []uuid.UUID) {
					want = want[:0]
					index.SearchOne(request[offerIndex], &want)
					require.ElementsMatch(t, want, got, "request %d, offer %d", requestIndex, offerIndex)
				})
			}
		})
	}
}
