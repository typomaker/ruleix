package ruleix

import (
	"math"
	"math/rand"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

type strictAntonymConstraint struct {
	id   *int
	name *string
}

func TestStrictEqualityAntonymDifferential(t *testing.T) {
	random := rand.New(rand.NewSource(42)) //nolint:gosec // Deterministic property fixture.
	for _, compressed := range []bool{false, true} {
		for iteration := range 25 {
			constraints := make([]strictAntonymConstraint, 96)
			ids := make([]int, len(constraints))
			for id := range constraints {
				ids[id] = id
				if random.Intn(2) == 0 {
					constraints[id].id = antonymPointer(random.Intn(12))
				} else {
					constraints[id].name = antonymPointer(string(rune('a' + random.Intn(12))))
				}
			}
			var schema Rule[strictAntonymConstraint] = strictAntonymSchema()
			if compressed {
				schema = Lossy(schema, MemoryLimit(math.MaxUint64))
			}
			baseline, _, err := buildIndexPhysicalAliases(
				schema, Zip(constraints, ids), false, nil,
				buildOptions{compilePhysicalAliases: true, enableStreaming: true},
			)
			require.NoError(t, err)
			integrated, _, err := buildIndexPhysicalAliases(
				schema, Zip(constraints, ids), false, nil,
				buildOptions{compilePhysicalAliases: true, compileStrictAntonyms: true, enableStreaming: true},
			)
			require.NoError(t, err)
			require.IsType(t, &allRule[strictAntonymConstraint]{}, baseline.root)
			integratedRoot := integrated.root.(*allRule[strictAntonymConstraint])
			require.Len(t, integratedRoot.children, 1)
			require.IsType(t, &strictEqualityAntonymRule[strictAntonymConstraint]{}, integratedRoot.children[0])
			for queryIndex := range 50 {
				query := strictAntonymConstraint{}
				if queryIndex%3 != 0 {
					query.id = antonymPointer(random.Intn(16))
				}
				if queryIndex%5 != 0 {
					query.name = antonymPointer(string(rune('a' + random.Intn(16))))
				}
				var baselineMatches, integratedMatches []int
				baseline.Search(query, &baselineMatches)
				integrated.Search(query, &integratedMatches)
				require.Equal(t, baselineMatches, integratedMatches, "compressed=%v iteration=%d query=%d", compressed, iteration, queryIndex)
			}
		}
	}
}

func strictAntonymSchema() Rule[strictAntonymConstraint] {
	return All(
		Include(func(value strictAntonymConstraint) (int, bool) {
			if value.id == nil {
				return 0, false
			}
			return *value.id, true
		}),
		Include(func(value strictAntonymConstraint) (string, bool) {
			if value.name == nil {
				return "", false
			}
			return *value.name, true
		}),
	)
}

func strictAntonymFixture() ([]strictAntonymConstraint, []int) {
	return []strictAntonymConstraint{
		{id: antonymPointer(10)},
		{id: antonymPointer(11)},
		{name: antonymPointer("foo")},
		{name: antonymPointer("foo")},
		{name: antonymPointer("bar")},
	}, []int{1, 2, 3, 4, 5}
}

func antonymPointer[V any](value V) *V { return &value }

func TestStrictEqualityAntonymSearch(t *testing.T) {
	constraints, ids := strictAntonymFixture()
	tests := []struct {
		name string
		rule Rule[strictAntonymConstraint]
	}{
		{name: "Exact", rule: strictAntonymSchema()},
		{name: "IdentityCompressed", rule: Lossy(strictAntonymSchema(), MemoryLimit(math.MaxUint64))},
	}
	queries := []struct {
		name  string
		value strictAntonymConstraint
		want  []int
	}{
		{name: "right-key-absent", value: strictAntonymConstraint{id: antonymPointer(10), name: antonymPointer("buz")}, want: []int{1}},
		{name: "both-keys-present", value: strictAntonymConstraint{id: antonymPointer(10), name: antonymPointer("foo")}, want: []int{1, 3, 4}},
		{name: "left-value-missing", value: strictAntonymConstraint{name: antonymPointer("foo")}, want: []int{3, 4}},
		{name: "both-keys-absent", value: strictAntonymConstraint{id: antonymPointer(12), name: antonymPointer("buz")}},
	}
	for _, mode := range tests {
		t.Run(mode.name, func(t *testing.T) {
			index, err := New[strictAntonymConstraint, int](mode.rule).Build(Zip(constraints, ids))
			require.NoError(t, err)
			root := index.root.(*allRule[strictAntonymConstraint])
			require.Len(t, root.children, 1)
			require.IsType(t, &strictEqualityAntonymRule[strictAntonymConstraint]{}, root.children[0])
			local := index.Local()
			t.Cleanup(local.Close)
			for _, query := range queries {
				t.Run(query.name, func(t *testing.T) {
					var got []int
					index.Search(query.value, &got)
					require.Equal(t, query.want, got)
					for range 4 {
						got = got[:0]
						local.Search(query.value, &got)
						require.Equal(t, query.want, got)
					}
				})
			}
		})
	}
}

func TestStrictWildcardComplementsRejectOverlapAndGap(t *testing.T) {
	tests := []struct {
		name        string
		constraints []strictAntonymConstraint
	}{
		{name: "overlap", constraints: []strictAntonymConstraint{{}, {id: antonymPointer(1)}, {name: antonymPointer("a")}}},
		{name: "gap", constraints: []strictAntonymConstraint{{id: antonymPointer(1), name: antonymPointer("a")}, {id: antonymPointer(2)}, {name: antonymPointer("b")}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ids := make([]int, len(test.constraints))
			for i := range ids {
				ids[i] = i
			}
			index, err := New[strictAntonymConstraint, int](strictAntonymSchema()).Build(Zip(test.constraints, ids))
			require.NoError(t, err)
			root := index.root.(*allRule[strictAntonymConstraint])
			require.Len(t, root.children, 2)
		})
	}
}

type ambiguousAntonymConstraint struct{ first, second, third *int }

func TestStrictEqualityAntonymChoosesOneDeterministicPartner(t *testing.T) {
	get := func(field func(ambiguousAntonymConstraint) *int) Rule[ambiguousAntonymConstraint] {
		return Include(func(value ambiguousAntonymConstraint) (int, bool) {
			key := field(value)
			if key == nil {
				return 0, false
			}
			return *key, true
		})
	}
	schema := All(
		get(func(value ambiguousAntonymConstraint) *int { return value.first }),
		get(func(value ambiguousAntonymConstraint) *int { return value.second }),
		get(func(value ambiguousAntonymConstraint) *int { return value.third }),
	)
	constraints := []ambiguousAntonymConstraint{
		{first: antonymPointer(1)},
		{first: antonymPointer(2)},
		{second: antonymPointer(3), third: antonymPointer(3)},
		{second: antonymPointer(4), third: antonymPointer(4)},
	}
	index, err := New[ambiguousAntonymConstraint, int](schema).Build(
		Zip(constraints, []int{0, 1, 2, 3}),
	)
	require.NoError(t, err)
	root := index.root.(*allRule[ambiguousAntonymConstraint])
	require.Len(t, root.children, 1)
	pair := root.children[0].(*strictEqualityAntonymRule[ambiguousAntonymConstraint])
	require.Len(t, pair.left, 1)
	require.Len(t, pair.right, 2)
	require.Same(t,
		pair.right[0].sharedWildcard(),
		pair.right[1].sharedWildcard(),
		"one component must retain the interned wildcard shared by its whole side",
	)
	var got []int
	index.Search(ambiguousAntonymConstraint{
		first: antonymPointer(1), second: antonymPointer(3), third: antonymPointer(3),
	}, &got)
	require.Equal(t, []int{0, 2}, got)
}

func TestStrictEqualityAntonymHandlesEmptyWildcardAndMatchAll(t *testing.T) {
	constraints := []strictAntonymConstraint{
		{id: antonymPointer(1)}, {id: antonymPointer(2)}, {id: antonymPointer(3)},
	}
	schema := All(strictAntonymSchema(), All[strictAntonymConstraint]())
	index, err := New[strictAntonymConstraint, int](schema).Build(
		Zip(constraints, []int{1, 2, 3}),
	)
	require.NoError(t, err)
	var got []int
	index.Search(strictAntonymConstraint{id: antonymPointer(2)}, &got)
	require.Equal(t, []int{2}, got)
	got = nil
	index.Search(strictAntonymConstraint{}, &got)
	require.Empty(t, got)
}

func TestStrictEqualityAntonymRuntimeContract(t *testing.T) {
	constraints, ids := strictAntonymFixture()
	index, err := New[strictAntonymConstraint, int](strictAntonymSchema()).Build(Zip(constraints, ids))
	require.NoError(t, err)
	root := index.root.(*allRule[strictAntonymConstraint])
	pair := root.children[0].(*strictEqualityAntonymRule[strictAntonymConstraint])
	query := strictAntonymConstraint{id: antonymPointer(10), name: antonymPointer("foo")}

	require.NoError(t, pair.validate(query))
	require.Same(t, pair, pair.newState(nil, nil))
	require.Equal(t, uint64(3), pair.cardinality(query, nil))
	require.Equal(t, uint64(3), pair.estimateCheapCardinality(query))
	require.False(t, pair.isCheapCardinalityZero(query))
	require.False(t, pair.isCardinalityZero(query))
	require.True(t, pair.matchesID(query, 0))
	require.False(t, pair.matchesID(query, 4))
	pair.insert(query, 0)
	pair.exclude(query, roaring.New(), newBitmapPool())
	pair.collectBuildStatistics(nil)

	pool := newBitmapPool()
	pool.local = make([]localNodeCache, int(pair.nodeID)+1)
	for range 4 {
		bits := roaring.New()
		pair.search(query, bits, pool)
		require.Equal(t, []uint32{0, 2, 3}, bits.ToArray())
	}
	_, found := pair.lookupCachedBitmap(query, pool)
	require.True(t, found)
	keys, retained := pair.captureQueryKeys(query, nil)
	require.NotZero(t, retained)
	require.True(t, pair.queryKeysMatch(query, keys))
	require.False(t, pair.queryKeysMatch(strictAntonymConstraint{}, keys))
	pair.cache(pool).reset(pool)
	_, found = pair.lookupCachedBitmap(query, pool)
	require.False(t, found)
}

type antonymGraphConstraint struct{ values [6]*int }

func antonymGraphSchema(fields int) Rule[antonymGraphConstraint] {
	// Distinct literals deliberately give canonicalization distinct getter PCs.
	rules := []Rule[antonymGraphConstraint]{
		Include(antonymGraphValue0), Include(antonymGraphValue1), Include(antonymGraphValue2),
		Include(antonymGraphValue3), Include(antonymGraphValue4), Include(antonymGraphValue5),
	}
	return All(rules[:fields]...)
}

func antonymGraphValue0(value antonymGraphConstraint) (int, bool) { return antonymGraphValue(value, 0) }
func antonymGraphValue1(value antonymGraphConstraint) (int, bool) { return antonymGraphValue(value, 1) }
func antonymGraphValue2(value antonymGraphConstraint) (int, bool) { return antonymGraphValue(value, 2) }
func antonymGraphValue3(value antonymGraphConstraint) (int, bool) { return antonymGraphValue(value, 3) }
func antonymGraphValue4(value antonymGraphConstraint) (int, bool) { return antonymGraphValue(value, 4) }
func antonymGraphValue5(value antonymGraphConstraint) (int, bool) { return antonymGraphValue(value, 5) }

func antonymGraphValue(value antonymGraphConstraint, field int) (int, bool) {
	key := value.values[field]
	if key == nil {
		return 0, false
	}
	return *key, true
}

func TestStrictEqualityAntonymCompilesThreeByThreeComponent(t *testing.T) {
	constraints := make([]antonymGraphConstraint, 64)
	ids := make([]int, len(constraints))
	for id := range constraints {
		ids[id] = id
		if id < len(constraints)/2 {
			for field := 3; field < 6; field++ {
				constraints[id].values[field] = antonymPointer((id + field) % 7)
			}
		} else {
			for field := 0; field < 3; field++ {
				constraints[id].values[field] = antonymPointer((id + field) % 7)
			}
		}
	}
	for _, compressed := range []bool{false, true} {
		schema := antonymGraphSchema(6)
		if compressed {
			schema = Lossy(schema, MemoryLimit(math.MaxUint64))
		}
		compiled, _, err := buildIndexPhysicalAliases(
			schema, Zip(constraints, ids), false, nil,
			buildOptions{compilePhysicalAliases: true, compileStrictAntonyms: true, enableStreaming: true},
		)
		require.NoError(t, err)
		root := compiled.root.(*allRule[antonymGraphConstraint])
		require.Len(t, root.children, 1)
		component := root.children[0].(*strictEqualityAntonymRule[antonymGraphConstraint])
		require.Len(t, component.left, 3)
		require.Len(t, component.right, 3)

		random := rand.New(rand.NewSource(84)) //nolint:gosec // Deterministic differential fixture.
		for iteration := range 100 {
			query := antonymGraphConstraint{}
			for field := range 6 {
				if random.Intn(4) != 0 {
					query.values[field] = antonymPointer(random.Intn(9))
				}
			}
			want := matchAntonymGraphConstraints(constraints, query, 6)
			var got []int
			compiled.Search(query, &got)
			require.Equal(t, want, got, "compressed=%v iteration=%d query=%v", compressed, iteration, query)
		}
	}
}

func matchAntonymGraphConstraints(
	constraints []antonymGraphConstraint, query antonymGraphConstraint, fields int,
) []int {
	var matches []int
	for id, constraint := range constraints {
		matched := true
		for field := range fields {
			if constraint.values[field] != nil &&
				(query.values[field] == nil || *constraint.values[field] != *query.values[field]) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, id)
		}
	}
	return matches
}

func TestStrictEqualityAntonymCompilesIndependentComponents(t *testing.T) {
	constraints := make([]antonymGraphConstraint, 16)
	ids := make([]int, len(constraints))
	for id := range constraints {
		ids[id] = id
		for field := range 4 {
			present := id%2 == 0
			if field >= 2 {
				present = id%4 < 2
			}
			if field%2 == 1 {
				present = !present
			}
			if present {
				constraints[id].values[field] = antonymPointer(id % 3)
			}
		}
	}
	index, err := New[antonymGraphConstraint, int](antonymGraphSchema(4)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	root := index.root.(*allRule[antonymGraphConstraint])
	require.Len(t, root.children, 2)
	for _, child := range root.children {
		component := child.(*strictEqualityAntonymRule[antonymGraphConstraint])
		require.Len(t, component.left, 1)
		require.Len(t, component.right, 1)
	}
}

func TestStrictEqualityAntonymLargeComponentPaths(t *testing.T) {
	const sideSize = 600
	constraints := make([]antonymGraphConstraint, sideSize*2)
	ids := make([]int, len(constraints))
	for id := range constraints {
		ids[id] = id
		from, until := 0, 3
		if id < sideSize {
			from, until = 3, 6
		}
		for field := from; field < until; field++ {
			constraints[id].values[field] = antonymPointer(7)
		}
	}
	index, err := New[antonymGraphConstraint, int](antonymGraphSchema(6)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	component := index.root.(*allRule[antonymGraphConstraint]).children[0].(*strictEqualityAntonymRule[antonymGraphConstraint])
	query := antonymGraphConstraint{}
	for field := range 6 {
		query.values[field] = antonymPointer(7)
	}

	var got []int
	index.Search(query, &got)
	require.Len(t, got, len(constraints))
	require.True(t, component.matchesID(query, 0))
	require.Equal(t, uint64(6*allEqualityDirectIDWork), component.directIDWork())
	require.Nil(t, component.left[0].concreteMatchSet(antonymGraphConstraint{}))
	_, found := component.lookupPlanningBitmap(query)
	require.False(t, found)
	set := component.left[0].concreteMatchSet(query)
	require.True(t, strictEqualitySetAllMatches(set, component.left, 0, query))

	query.values[1] = antonymPointer(8)
	got = got[:0]
	index.Search(query, &got)
	require.Len(t, got, sideSize)
	filtered := roaring.New()
	strictEqualityFilterSet(set, component.left, 0, query, filtered)
	require.True(t, filtered.IsEmpty())
}
