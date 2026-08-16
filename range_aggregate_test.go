package ruleix_test

import (
	"cmp"
	"math/rand"
	"testing"

	"github.com/typomaker/ruleix"
	"github.com/stretchr/testify/require"
)

type differentialComparison struct {
	operator string
	value    int
}

func comparisonMatches(operator string, stored, query int) bool {
	switch operator {
	case "=":
		return query == stored
	case "<":
		return query < stored
	case "<=":
		return query <= stored
	case ">":
		return query > stored
	case ">=":
		return query >= stored
	default:
		panic("invalid test operator")
	}
}

func TestCompareByMatchesScanningReferenceAcrossBlocks(t *testing.T) {
	operators := []string{"=", "<", "<=", ">", ">="}
	rng := rand.New(rand.NewSource(1))
	comparisonSchema := ruleix.CompareBy(
		func(v differentialComparison) string { return v.operator },
		func(v differentialComparison) *int { return &v.value },
		cmp.Compare[int],
	)
	stored := make([]differentialComparison, 2_000)
	ids := make([]int, len(stored))
	for id := range stored {
		stored[id] = differentialComparison{
			operator: operators[id%len(operators)],
			value:    rng.Intn(800) - 400,
		}
		ids[id] = id
	}
	ix := buildZip(t, comparisonSchema, stored, ids)

	for query := -450; query <= 450; query += 7 {
		var want []int
		for id, rule := range stored {
			if comparisonMatches(rule.operator, rule.value, query) {
				want = append(want, id)
			}
		}
		got := search(ix, differentialComparison{value: query})
		require.Equal(t, want, got, "query %d", query)
	}
}

type differentialInterval struct {
	from  int
	until int
}

func TestBetweenMatchesScanningReferenceAcrossBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	intervalSchema := ruleix.Between(
		func(v differentialInterval) *int { return &v.from },
		func(v differentialInterval) *int { return &v.until },
		cmp.Compare[int],
	)
	stored := make([]differentialInterval, 1_500)
	ids := make([]int, len(stored))
	for id := range stored {
		from := rng.Intn(700) - 350
		stored[id] = differentialInterval{from: from, until: from + rng.Intn(200)}
		ids[id] = id
	}
	ix := buildZip(t, intervalSchema, stored, ids)

	for queryFrom := -400; queryFrom <= 400; queryFrom += 31 {
		query := differentialInterval{from: queryFrom, until: queryFrom + 50}
		var want []int
		for id, interval := range stored {
			if interval.from <= query.from && query.until <= interval.until {
				want = append(want, id)
			}
		}
		require.Equal(t, want, search(ix, query), "query %+v", query)
	}
}

func TestBetweenPreservesIndependentBoundsForRepeatedID(t *testing.T) {
	schema := ruleix.Between(
		func(v differentialInterval) *int { return &v.from },
		func(v differentialInterval) *int { return &v.until },
		cmp.Compare[int],
	)
	ix := buildZip(t, schema,
		[]differentialInterval{{from: 0, until: 5}, {from: 15, until: 20}},
		[]string{"repeated", "repeated"})

	require.Equal(t, []string{"repeated"}, search(ix, differentialInterval{from: 10, until: 10}))
}
