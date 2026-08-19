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
	require.IsType(t, &eqRule[constraint, int]{}, index.root)

	var matches []int
	index.Search(constraint{concrete: &two}, &matches)
	require.Equal(t, []int{2}, matches)
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
