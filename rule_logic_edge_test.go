package ruleix_test

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/typomaker/ruleix"
)

type optionalNumber struct {
	value *int
}

func optionalNumberGetter(v optionalNumber) (int, bool) {
	if v.value == nil {
		return 0, false
	}
	return *v.value, true
}

func TestGetterRulesDistinguishPresentZeroFromAbsent(t *testing.T) {
	zero, one := 0, 1
	constraints := []optionalNumber{{}, {value: &zero}, {value: &one}}
	ids := []string{"absent", "zero", "one"}
	tests := []struct {
		name       string
		rule       ruleix.Rule[optionalNumber]
		zeroWant   []string
		absentWant []string
	}{
		{"Include", ruleix.Include(optionalNumberGetter), []string{"absent", "zero"}, []string{"absent"}},
		{"Greater", ruleix.Greater(optionalNumberGetter, cmp.Compare[int]), []string{"absent"}, []string{"absent"}},
		{"GreaterOrEqual", ruleix.GreaterOrEqual(optionalNumberGetter, cmp.Compare[int]), []string{"absent", "zero"}, []string{"absent"}},
		{"Less", ruleix.Less(optionalNumberGetter, cmp.Compare[int]), []string{"absent", "one"}, []string{"absent"}},
		{"LessOrEqual", ruleix.LessOrEqual(optionalNumberGetter, cmp.Compare[int]), []string{"absent", "zero", "one"}, []string{"absent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := buildZip(t, tt.rule, constraints, ids)
			require.Equal(t, tt.zeroWant, search(ix, optionalNumber{value: &zero}))
			require.Equal(t, tt.absentWant, search(ix, optionalNumber{}))
		})
	}
}

func TestExcludeDistinguishesPresentZeroFromAbsent(t *testing.T) {
	zero, one := 0, 1
	ix := buildZip(t, ruleix.Exclude(optionalNumberGetter),
		[]optionalNumber{{}, {value: &zero}, {value: &one}},
		[]string{"absent", "zero", "one"})

	require.Equal(t, []string{"absent", "one"}, search(ix, optionalNumber{value: &zero}))
	require.Equal(t, []string{"absent", "zero", "one"}, search(ix, optionalNumber{}))
}

func TestEmptyAllMatchesEveryStoredID(t *testing.T) {
	ix := buildZip(t, ruleix.All[optionalNumber](),
		[]optionalNumber{{}, {}, {}}, []string{"first", "second", "first"})
	require.Equal(t, []string{"first", "second"}, search(ix, optionalNumber{}))
}

func TestExcludeMatchesScanningReference(t *testing.T) {
	constraints := make([]optionalNumber, 257)
	ids := make([]int, len(constraints))
	values := make([]int, len(constraints))
	for i := range constraints {
		values[i] = i%17 - 8
		if i%19 != 0 {
			constraints[i].value = &values[i]
		}
		ids[i] = i
	}
	ix := buildZip(t, ruleix.Exclude(optionalNumberGetter), constraints, ids)
	for query := -10; query <= 10; query++ {
		var want []int
		for id, constraint := range constraints {
			if constraint.value == nil || *constraint.value != query {
				want = append(want, id)
			}
		}
		require.Equal(t, want, search(ix, optionalNumber{value: &query}), "query %d", query)
	}
}
