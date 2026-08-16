package ruleix_test

import (
	"cmp"
	"testing"

	"github.com/albertsultanov/ruleix"
	"github.com/stretchr/testify/require"
)

type wildcardValue struct {
	operator string
	value    *int
}

func TestOrderedRulesSupportWildcard(t *testing.T) {
	tests := []struct {
		name   string
		schema ruleix.Rule[wildcardValue]
		query  int
		want   []string
	}{
		{
			name:   "GreaterOrEqual",
			schema: ruleix.GreaterOrEqual(func(v wildcardValue) *int { return v.value }, cmp.Compare[int]),
			query:  10,
			want:   []string{"wildcard", "match"},
		},
		{
			name:   "LessOrEqual",
			schema: ruleix.LessOrEqual(func(v wildcardValue) *int { return v.value }, cmp.Compare[int]),
			query:  10,
			want:   []string{"wildcard", "different"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := buildZip(t, tt.schema,
				[]wildcardValue{{}, {value: ptr(5)}, {value: ptr(15)}},
				[]string{"wildcard", "match", "different"})
			require.Equal(t, tt.want, search(ix, wildcardValue{value: &tt.query}))
			require.Equal(t, []string{"wildcard"}, search(ix, wildcardValue{}))
		})
	}
}

func TestOrderedRulesHonorStrictness(t *testing.T) {
	tests := []struct {
		name   string
		schema ruleix.Rule[wildcardValue]
		want   []string
	}{
		{
			name:   "GreaterOrEqual",
			schema: ruleix.GreaterOrEqual(func(v wildcardValue) *int { return v.value }, cmp.Compare[int]),
			want:   []string{"wildcard", "less", "equal"},
		},
		{
			name:   "Greater",
			schema: ruleix.Greater(func(v wildcardValue) *int { return v.value }, cmp.Compare[int]),
			want:   []string{"wildcard", "less"},
		},
		{
			name:   "LessOrEqual",
			schema: ruleix.LessOrEqual(func(v wildcardValue) *int { return v.value }, cmp.Compare[int]),
			want:   []string{"wildcard", "equal", "greater"},
		},
		{
			name:   "Less",
			schema: ruleix.Less(func(v wildcardValue) *int { return v.value }, cmp.Compare[int]),
			want:   []string{"wildcard", "greater"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ix := buildZip(t, tt.schema,
				[]wildcardValue{{}, {value: ptr(5)}, {value: ptr(10)}, {value: ptr(15)}},
				[]string{"wildcard", "less", "equal", "greater"})
			require.Equal(t, tt.want, search(ix, wildcardValue{value: ptr(10)}))
			require.Equal(t, []string{"wildcard"}, search(ix, wildcardValue{}))
		})
	}
}

func TestCompareBySupportsWildcard(t *testing.T) {
	schema := ruleix.CompareBy(
		func(v wildcardValue) string { return v.operator },
		func(v wildcardValue) *int { return v.value },
		cmp.Compare[int],
	)
	ix := buildZip(t, schema,
		[]wildcardValue{{operator: "invalid"}, {operator: ">=", value: ptr(5)}, {operator: "<=", value: ptr(5)}},
		[]string{"wildcard", "match", "different"})

	require.Equal(t, []string{"wildcard", "match"}, search(ix, wildcardValue{value: ptr(10)}))
	require.Equal(t, []string{"wildcard"}, search(ix, wildcardValue{}))
}

type wildcardInterval struct {
	from  *int
	until *int
}

func TestBetweenUsesWildcardBoundsFromChildren(t *testing.T) {
	schema := ruleix.Between(
		func(v wildcardInterval) *int { return v.from },
		func(v wildcardInterval) *int { return v.until },
		cmp.Compare[int],
	)
	ix := buildZip(t, schema,
		[]wildcardInterval{
			{},
			{from: ptr(5)},
			{until: ptr(20)},
			{from: ptr(5), until: ptr(20)},
			{from: ptr(15), until: ptr(20)},
		},
		[]string{"wildcard", "open-until", "open-from", "covering", "different"})

	require.Equal(t,
		[]string{"wildcard", "open-until", "open-from", "covering"},
		search(ix, wildcardInterval{from: ptr(10), until: ptr(15)}))
	require.Equal(t, []string{"wildcard"}, search(ix, wildcardInterval{}))
}

func TestNotWildcardDoesNotOverrideConcreteExclusion(t *testing.T) {
	schema := ruleix.Exclude(func(v wildcardValue) *int { return v.value })
	ix := buildZip(t, schema,
		[]wildcardValue{{value: ptr(1)}, {value: ptr(2)}, {value: ptr(3)}, {}},
		[]int{1, 1, 1, 1})

	require.Equal(t, []int{1}, search(ix, wildcardValue{}))
	require.Empty(t, search(ix, wildcardValue{value: ptr(1)}))
	require.Equal(t, []int{1}, search(ix, wildcardValue{value: ptr(4)}))
}
