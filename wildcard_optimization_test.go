//nolint:lll // Migration coverage keeps legacy pointer getters inline.
package ruleix

import (
	"cmp"
	"testing"

	"github.com/stretchr/testify/require"
)

func optimizationPointer[T any](value T) *T { return &value }

func TestBuildOptimizesWildcardRoot(t *testing.T) {
	type constraint struct{ value *int }
	index, err := New[constraint, int](Include(GetterFromPointer(func(value constraint) *int { return value.value }))).Build(
		Zip([]constraint{{}, {}}, []int{1, 2}),
	)
	require.NoError(t, err)
	require.IsType(t, &matchAllRule[constraint]{}, index.root)

	query := constraint{value: optimizationPointer(42)}
	var matches []int
	index.Search(query, &matches)
	require.Equal(t, []int{1, 2}, matches)
}

func TestBuildRemovesWildcardChildrenFromAll(t *testing.T) {
	type constraint struct{ wildcard, concrete *int }
	schema := All(
		Include(GetterFromPointer(func(value constraint) *int { return value.wildcard })),
		Include(GetterFromPointer(func(value constraint) *int { return value.concrete })),
	)
	one, two := 1, 2
	index, err := New[constraint, int](schema).Build(Zip(
		[]constraint{{concrete: &one}, {concrete: &two}},
		[]int{1, 2},
	))
	require.NoError(t, err)
	require.IsType(t, &binaryEqRule[constraint, int]{}, index.root)

	var matches []int
	index.Search(constraint{concrete: &two}, &matches)
	require.Equal(t, []int{2}, matches)
}

func TestBuildSpecializesUnaryEquality(t *testing.T) {
	type constraint struct{ value *bool }
	falseValue, trueValue := false, true
	index, err := New[constraint, int](Include(GetterFromPointer(func(value constraint) *bool { return value.value }))).Build(Zip(
		[]constraint{{value: &falseValue}, {}, {value: &falseValue}},
		[]int{1, 2, 3},
	))
	require.NoError(t, err)
	require.IsType(t, &unaryEqRule[constraint, bool]{}, index.root)

	for _, tt := range []struct {
		query constraint
		want  []int
	}{
		{constraint{value: &falseValue}, []int{1, 2, 3}},
		{constraint{value: &trueValue}, []int{2}},
		{constraint{}, []int{2}},
	} {
		var matches []int
		index.Search(tt.query, &matches)
		require.Equal(t, tt.want, matches)
		matches = nil
		index.Local().Search(tt.query, &matches)
		require.Equal(t, tt.want, matches)
	}
}

func TestBuildSpecializesBinaryEquality(t *testing.T) {
	type constraint struct{ value *bool }
	falseValue, trueValue := false, true
	index, err := New[constraint, int](Include(GetterFromPointer(func(value constraint) *bool { return value.value }))).Build(Zip(
		[]constraint{{value: &falseValue}, {value: &trueValue}, {}},
		[]int{1, 2, 3},
	))
	require.NoError(t, err)
	require.IsType(t, &binaryEqRule[constraint, bool]{}, index.root)

	for _, tt := range []struct {
		query constraint
		want  []int
	}{
		{constraint{value: &falseValue}, []int{1, 3}},
		{constraint{value: &trueValue}, []int{2, 3}},
		{constraint{}, []int{3}},
	} {
		var matches []int
		index.Search(tt.query, &matches)
		require.Equal(t, tt.want, matches)
	}
}

func TestBuildOptimizesWildcardBetween(t *testing.T) {
	type constraint struct{ from, until *int }
	index, err := New[constraint, int](Between(GetterFromPointer(func(value constraint) *int { return value.from }), GetterFromPointer(func(value constraint) *int { return value.until }), cmp.Compare[int])).Build(Zip([]constraint{{}, {}}, []int{1, 2}))
	require.NoError(t, err)
	require.IsType(t, &matchAllRule[constraint]{}, index.root)

	var matches []int
	index.Search(constraint{from: optimizationPointer(10), until: optimizationPointer(20)}, &matches)
	require.Equal(t, []int{1, 2}, matches)
}

func TestBuildKeepsWildcardExcludeWithConcreteExclusions(t *testing.T) {
	type constraint struct{ excluded *int }
	schema := Exclude(GetterFromPointer(func(value constraint) *int { return value.excluded }))
	excluded := 42
	index, err := New[constraint, int](schema).Build(Zip(
		[]constraint{{}, {excluded: &excluded}, {}},
		[]int{1, 1, 2},
	))
	require.NoError(t, err)
	require.IsType(t, &allRule[constraint]{}, index.root)
	root := index.root.(*allRule[constraint])
	require.Len(t, root.children, 1)
	require.IsType(t, &matchAllRule[constraint]{}, root.children[0])

	var matches []int
	index.Search(constraint{excluded: &excluded}, &matches)
	require.Equal(t, []int{2}, matches)
}

func TestBuildCollectsStatisticsBeforeWildcardOptimization(t *testing.T) {
	type constraint struct{ value *int }
	builder := New[constraint, int](Include(GetterFromPointer(func(value constraint) *int { return value.value })))
	_, err := builder.Build(Zip([]constraint{{}, {}}, []int{1, 2}))
	require.NoError(t, err)
	require.Len(t, builder.hints.nodes, 1)
	require.Equal(t, 2, builder.hints.uniqueIDs)
}

func TestAllSharesInternedPartialEqualityWildcards(t *testing.T) {
	type constraint struct{ left, right *int }
	schema := All(
		Include(GetterFromPointer(func(value constraint) *int { return value.left })),
		Include(GetterFromPointer(func(value constraint) *int { return value.right })),
	)
	one, two, three := 1, 2, 3
	constraints := []constraint{
		{}, {}, {}, {}, {}, {},
		{left: &one, right: &one},
		{left: &one, right: &two},
		{left: &two, right: &one},
		{left: &three, right: &three},
	}
	index, err := New[constraint, int](schema).Build(Zip(constraints, []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}))
	require.NoError(t, err)
	root := index.root.(*allRule[constraint])
	left := root.children[0].(*eqRule[constraint, int])
	right := root.children[1].(*eqRule[constraint, int])
	require.Same(t, left.wildcard, right.wildcard)
	require.Equal(t, []int{1, 1}, root.sharedWildcardGroups)

	ranked := []rankedBitmap{{childIdx: 0}, {childIdx: 1}}
	ranked = root.collectSharedWildcards(constraint{left: &one, right: &one}, index.pool, ranked)
	require.Len(t, ranked, 1)
	require.Equal(t, uint64(7), ranked[0].card)
	root.releaseRanked(index.pool, ranked)

	for _, search := range []func(constraint, *[]int) bool{index.Search, index.Local().Search} {
		var matches []int
		search(constraint{left: &one, right: &one}, &matches)
		require.Equal(t, []int{0, 1, 2, 3, 4, 5, 6}, matches)
	}
}

func TestAllStoresIDsOnlyForDuplicateEqualityBitmaps(t *testing.T) {
	type constraint struct{ left, right *int }
	schema := All(
		Include(GetterFromPointer(func(value constraint) *int { return value.left })),
		Include(GetterFromPointer(func(value constraint) *int { return value.right })),
	)
	one, two := 1, 2
	index, err := New[constraint, int](schema).Build(Zip(
		[]constraint{{left: &one}, {right: &two}, {left: &two, right: &one}},
		[]int{0, 1, 2},
	))
	require.NoError(t, err)
	root := index.root.(*allRule[constraint])
	require.Nil(t, root.sharedWildcardGroups)
	// Neither the wildcard sets nor the small unique postings are shared, so no
	// duplicate IDs or retained per-posting metadata are created.
	require.Nil(t, root.duplicateBitmapIDs)
}

func TestAllChecksInternedEqualityPostingOnce(t *testing.T) {
	type constraint struct{ left, right *int }
	schema := All(
		Include(GetterFromPointer(func(value constraint) *int { return value.left })),
		Include(GetterFromPointer(func(value constraint) *int { return value.right })),
	)
	one := 1
	constraints := make([]constraint, equalityArrayLimit+2)
	ids := make([]int, len(constraints))
	for i := range constraints {
		constraints[i] = constraint{left: &one, right: &one}
		ids[i] = i
	}
	index, err := New[constraint, int](schema).Build(Zip(constraints, ids))
	require.NoError(t, err)
	root := index.root.(*allRule[constraint])
	require.Equal(t, uint32(2), root.equalityClassCount) // empty wildcard and concrete posting
	require.Nil(t, root.duplicateBitmapIDs)

	ranked := []rankedBitmap{{childIdx: 0}, {childIdx: 1}}
	var checked [1]uint64
	ranked = root.deduplicateEqualityClasses(constraint{left: &one, right: &one}, ranked, checked[:])
	require.Len(t, ranked, 1)

	var matches []int
	index.Search(constraint{left: &one, right: &one}, &matches)
	require.Equal(t, ids, matches)
}

func TestAllDeduplicatesOnlyMatchingWildcardPostingPair(t *testing.T) {
	type constraint struct{ left, right *int }
	schema := All(
		Include(GetterFromPointer(func(value constraint) *int { return value.left })),
		Include(GetterFromPointer(func(value constraint) *int { return value.right })),
	)
	one, two := 1, 2
	constraints := make([]constraint, 0, 80)
	ids := make([]int, 0, 80)
	for i := range 40 {
		constraints = append(constraints, constraint{left: &one, right: &one})
		ids = append(ids, i)
	}
	for i := 40; i < 80; i++ {
		constraints = append(constraints, constraint{left: &one, right: &two})
		ids = append(ids, i)
	}
	index, err := New[constraint, int](schema).Build(Zip(constraints, ids))
	require.NoError(t, err)
	root := index.root.(*allRule[constraint])

	ranked := []rankedBitmap{{childIdx: 0}, {childIdx: 1}}
	var checked [1]uint64
	got := root.deduplicateEqualityClasses(constraint{left: &one, right: &one}, append([]rankedBitmap(nil), ranked...), checked[:])
	require.Len(t, got, 2, "different concrete postings must remain distinct")

	// Querying an absent value selects the same [empty wildcard, no posting]
	// pair in both children, so the second rule is redundant.
	missing := 3
	checked = [1]uint64{}
	got = root.deduplicateEqualityClasses(constraint{left: &missing, right: &missing}, ranked, checked[:])
	require.Len(t, got, 1)
}
