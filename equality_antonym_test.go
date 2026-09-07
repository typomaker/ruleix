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
	require.Len(t, root.children, 2)
	pair := root.children[0].(*strictEqualityAntonymRule[ambiguousAntonymConstraint])
	require.Same(t,
		pair.right.sharedWildcard(),
		strictEqualityOperandOf(root.children[1]).sharedWildcard(),
		"the unpaired sibling must retain the interned wildcard shared with the chosen partner",
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
	key, retained := pair.localQueryKey(query)
	require.NotZero(t, retained)
	require.True(t, pair.localQueryKeyMatches(query, key))
	require.False(t, pair.localQueryKeyMatches(strictAntonymConstraint{}, key))
	pair.cache(pool).reset(pool)
	_, found = pair.lookupCachedBitmap(query, pool)
	require.False(t, found)
}
