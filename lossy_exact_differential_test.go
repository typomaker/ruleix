package ruleix

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type lossyDifferentialConstraint struct {
	name               string
	value, from, until int
	operator           Operator
	namePresent        bool
	valuePresent       bool
	fromPresent        bool
	untilPresent       bool
	operatorPresent    bool
}

func lossyDifferentialData() ([]lossyDifferentialConstraint, []int) {
	const entries = lossyBuildPressureInterval + 1536
	constraints := make([]lossyDifferentialConstraint, entries)
	ids := make([]int, entries)
	for id := range constraints {
		// The prefix is deliberately narrow. Values after the streaming checkpoint
		// expand both edges, exercising build-time rebucketing rather than only the
		// ordinary exact-first compiler.
		value := id%257 - 128
		if id >= lossyBuildPressureInterval {
			value = (id-lossyBuildPressureInterval)*17 - 13_000
		}
		constraints[id] = lossyDifferentialConstraint{
			name:            fmt.Sprintf("customer-%05d", id),
			value:           value,
			from:            value - id%11,
			until:           value + 20 + id%17,
			operator:        Operator(id % 5),
			namePresent:     id%211 != 0,
			valuePresent:    id%197 != 0,
			fromPresent:     id%193 != 0,
			untilPresent:    id%191 != 0,
			operatorPresent: id%197 != 0,
		}
		ids[id] = id
	}
	return constraints, ids
}

func lossyDifferentialQueries() []lossyDifferentialConstraint {
	queries := make([]lossyDifferentialConstraint, 0, 96)
	for _, value := range []int{-13_001, -13_000, -129, -128, -1, 0, 1, 127, 128, 12_000, 13_111} {
		for _, operator := range []Operator{OperatorEQ, OperatorLT, OperatorLTE, OperatorGT, OperatorGTE} {
			queries = append(queries, lossyDifferentialConstraint{
				name: "customer-04096", value: value, from: value, until: value + 7, operator: operator,
				namePresent: true, valuePresent: true, fromPresent: true, untilPresent: true, operatorPresent: true,
			})
		}
	}
	return append(queries,
		lossyDifferentialConstraint{},
		lossyDifferentialConstraint{name: "customer-00001", namePresent: true},
		lossyDifferentialConstraint{value: 0, valuePresent: true},
	)
}

func assertLossyDifferentialSuperset(
	t *testing.T,
	exactRule, approximateRule Rule[lossyDifferentialConstraint],
	limit uint64,
	constraints []lossyDifferentialConstraint,
	ids []int,
	queries []lossyDifferentialConstraint,
) {
	t.Helper()
	exact, err := New[lossyDifferentialConstraint, int](exactRule).Build(Zip(constraints, ids))
	require.NoError(t, err)
	var inspector Inspector
	streamed := false
	approximate, _, err := buildIndexPhysicalAliases(Inspect(
		&inspector,
		Lossy(approximateRule, MemoryLimit(limit)),
	), Zip(constraints, ids), false, nil, buildOptions{
		compilePhysicalAliases: true,
		enableStreaming:        true,
		observeWorkingUsage: func(usage, target uint64) {
			streamed = streamed || usage > target
		},
	})
	require.NoError(t, err)
	require.True(t, streamed, "fixture must cross the streaming pressure threshold")
	require.Equal(t, RuleModeLossy, inspector.Snapshot().Mode(), "fixture must compile a lossy representation")

	exactLocal, approximateLocal := exact.Local(), approximate.Local()
	defer exactLocal.Close()
	defer approximateLocal.Close()
	for queryIndex, query := range queries {
		var exactMatches, approximateMatches []int
		exact.Search(query, &exactMatches)
		approximate.Search(query, &approximateMatches)
		requireSupersetComparable(t, exactMatches, approximateMatches)

		for pass := range 2 {
			var exactLocalMatches, approximateLocalMatches []int
			exactLocal.Search(query, &exactLocalMatches)
			approximateLocal.Search(query, &approximateLocalMatches)
			require.Equalf(t, exactMatches, exactLocalMatches, "exact Local differs at query %d pass %d", queryIndex, pass)
			requireSupersetComparable(t, exactMatches, approximateLocalMatches)
		}
	}
}

// TestLossyExactDifferentialEverySupportedRule is the central public-rule
// correctness matrix. Lossy may add candidates but must never omit an Exact
// match, including after streaming grids expand beyond their prefix range.
func TestLossyExactDifferentialEverySupportedRule(t *testing.T) {
	constraints, ids := lossyDifferentialData()
	queries := lossyDifferentialQueries()
	name := func(v lossyDifferentialConstraint) (string, bool) { return v.name, v.namePresent }
	value := func(v lossyDifferentialConstraint) (int, bool) { return v.value, v.valuePresent }
	from := func(v lossyDifferentialConstraint) (int, bool) { return v.from, v.fromPresent }
	until := func(v lossyDifferentialConstraint) (int, bool) { return v.until, v.untilPresent }
	operator := func(v lossyDifferentialConstraint) (Operator, bool) {
		return v.operator, v.operatorPresent
	}

	tests := []struct {
		name  string
		limit uint64
		build func() Rule[lossyDifferentialConstraint]
	}{
		{"Include", 16 << 10, func() Rule[lossyDifferentialConstraint] { return Include(name) }},
		{"Greater", 16 << 10, func() Rule[lossyDifferentialConstraint] { return Greater(value, cmp.Compare[int]) }},
		{"GreaterOrEqual", 16 << 10, func() Rule[lossyDifferentialConstraint] { return GreaterOrEqual(value, cmp.Compare[int]) }},
		{"Less", 16 << 10, func() Rule[lossyDifferentialConstraint] { return Less(value, cmp.Compare[int]) }},
		{"LessOrEqual", 16 << 10, func() Rule[lossyDifferentialConstraint] { return LessOrEqual(value, cmp.Compare[int]) }},
		{"Between", 32 << 10, func() Rule[lossyDifferentialConstraint] {
			return Between(from, until, cmp.Compare[int])
		}},
		{"CompareBy", 32 << 10, func() Rule[lossyDifferentialConstraint] {
			return CompareBy(value, operator, cmp.Compare[int])
		}},
		{"All", 160 << 10, func() Rule[lossyDifferentialConstraint] {
			return All(Include(name), GreaterOrEqual(value, cmp.Compare[int]), Between(from, until, cmp.Compare[int]))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertLossyDifferentialSuperset(
				t, test.build(), test.build(), test.limit, constraints, ids, queries,
			)
		})
	}
}

// TestIdentityLossyDifferentialEverySupportedRule proves that the control
// quantizer traverses Lossy policy analysis while retaining exact keys.
func TestIdentityLossyDifferentialEverySupportedRule(t *testing.T) {
	constraints, ids := lossyDifferentialData()
	for i := range ids {
		ids[i] /= 2 // constraints deliberately share external IDs
	}
	queries := lossyDifferentialQueries()
	name := func(v lossyDifferentialConstraint) (string, bool) { return v.name, v.namePresent }
	value := func(v lossyDifferentialConstraint) (int, bool) { return v.value, v.valuePresent }
	from := func(v lossyDifferentialConstraint) (int, bool) { return v.from, v.fromPresent }
	until := func(v lossyDifferentialConstraint) (int, bool) { return v.until, v.untilPresent }
	operator := func(v lossyDifferentialConstraint) (Operator, bool) {
		return v.operator, v.operatorPresent
	}
	tests := []struct {
		name  string
		build func() Rule[lossyDifferentialConstraint]
	}{
		{"Include", func() Rule[lossyDifferentialConstraint] { return Include(name) }},
		{"Greater", func() Rule[lossyDifferentialConstraint] { return Greater(value, cmp.Compare[int]) }},
		{"GreaterOrEqual", func() Rule[lossyDifferentialConstraint] { return GreaterOrEqual(value, cmp.Compare[int]) }},
		{"Less", func() Rule[lossyDifferentialConstraint] { return Less(value, cmp.Compare[int]) }},
		{"LessOrEqual", func() Rule[lossyDifferentialConstraint] { return LessOrEqual(value, cmp.Compare[int]) }},
		{"Between", func() Rule[lossyDifferentialConstraint] { return Between(from, until, cmp.Compare[int]) }},
		{"CompareBy", func() Rule[lossyDifferentialConstraint] {
			return CompareBy(value, operator, cmp.Compare[int])
		}},
		{"All", func() Rule[lossyDifferentialConstraint] {
			var inspector Inspector
			return All(Inspect(&inspector, Include(name)), GreaterOrEqual(value, cmp.Compare[int]), Between(from, until, cmp.Compare[int]))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			exact, err := New[lossyDifferentialConstraint, int](test.build()).Build(Zip(constraints, ids))
			require.NoError(t, err)
			var inspector Inspector
			identity, _, err := buildIndexPhysicalAliases(
				Inspect(&inspector, Lossy(test.build(), MemoryLimit(1))), Zip(constraints, ids), false, nil,
				buildOptions{compilePhysicalAliases: true, enableStreaming: true, identityLossy: true},
			)
			require.NoError(t, err)
			exactLocal, identityLocal := exact.Local(), identity.Local()
			defer exactLocal.Close()
			defer identityLocal.Close()
			for queryIndex, query := range queries {
				var want, got []int
				exact.Search(query, &want)
				identity.Search(query, &got)
				require.Equalf(t, want, got, "Index.Search query %d", queryIndex)
				for pass := range 3 {
					want, got = want[:0], got[:0]
					exactLocal.Search(query, &want)
					identityLocal.Search(query, &got)
					require.Equalf(t, want, got, "Local.Search query %d pass %d", queryIndex, pass)
				}
			}
		})
	}
}
