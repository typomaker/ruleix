//nolint:lll // Differential schemas keep their getters inline.
package ruleix_test

import (
	"cmp"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

type differentialComparison struct {
	operator *ruleix.Operator
	value    int
}

func comparisonMatches(operator ruleix.Operator, stored, query int) bool {
	switch operator {
	case ruleix.OperatorEQ:
		return query == stored
	case ruleix.OperatorLT:
		return query < stored
	case ruleix.OperatorLTE:
		return query <= stored
	case ruleix.OperatorGT:
		return query > stored
	case ruleix.OperatorGTE:
		return query >= stored
	default:
		panic("invalid test operator")
	}
}

func TestCompareByMatchesScanningReferenceAcrossBlocks(t *testing.T) {
	operators := []ruleix.Operator{
		ruleix.OperatorEQ,
		ruleix.OperatorLT,
		ruleix.OperatorLTE,
		ruleix.OperatorGT,
		ruleix.OperatorGTE,
	}
	rng := rand.New(rand.NewSource(1))
	comparisonSchema := ruleix.CompareBy(ruleix.GetterFromPointer(func(v differentialComparison) *int { return &v.value }), ruleix.GetterFromPointer(func(v differentialComparison) *ruleix.Operator { return v.operator }), cmp.Compare[int])
	stored := make([]differentialComparison, 2_000)
	ids := make([]int, len(stored))
	for id := range stored {
		stored[id] = differentialComparison{operator: &operators[rng.Intn(len(operators))], value: rng.Intn(800) - 400}
		ids[id] = id
	}
	ix := buildZip(t, comparisonSchema, stored, ids)

	for query := -450; query <= 450; query += 7 {
		var want []int
		for id, rule := range stored {
			if comparisonMatches(*rule.operator, rule.value, query) {
				want = append(want, id)
			}
		}
		got := search(ix, differentialComparison{operator: ptr(ruleix.OperatorEQ), value: query})
		require.Equal(t, want, got, "query %d", query)
	}
}

type differentialInterval struct {
	from  int
	until int
}

func TestBetweenMatchesScanningReferenceAcrossBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	intervalSchema := ruleix.Between(ruleix.GetterFromPointer(func(v differentialInterval) *int { return &v.from }), ruleix.GetterFromPointer(func(v differentialInterval) *int { return &v.until }), cmp.Compare[int])
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
	schema := ruleix.Between(ruleix.GetterFromPointer(func(v differentialInterval) *int { return &v.from }), ruleix.GetterFromPointer(func(v differentialInterval) *int { return &v.until }), cmp.Compare[int])
	ix := buildZip(t, schema,
		[]differentialInterval{{from: 0, until: 5}, {from: 15, until: 20}},
		[]string{"repeated", "repeated"})

	require.Equal(t, []string{"repeated"}, search(ix, differentialInterval{from: 10, until: 10}))
}

func TestBetweenSelectiveBoundPreservesWildcards(t *testing.T) {
	type optionalInterval struct {
		from  *int
		until *int
	}
	schema := ruleix.Between(
		ruleix.GetterFromPointer(func(v optionalInterval) *int { return v.from }),
		ruleix.GetterFromPointer(func(v optionalInterval) *int { return v.until }),
		cmp.Compare[int],
	)
	ix := buildZip(t, schema,
		[]optionalInterval{
			{from: ptr(0), until: ptr(20)},
			{from: ptr(10), until: ptr(30)},
			{from: nil, until: ptr(25)},
			{from: ptr(5), until: nil},
		},
		[]string{"covering", "late", "open-from", "open-until"},
	)

	require.Equal(t, []string{"covering", "open-from", "open-until"},
		search(ix, optionalInterval{from: ptr(5), until: ptr(15)}))
}
