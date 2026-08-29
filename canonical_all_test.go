package ruleix

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

type canonicalConstraint struct {
	value    int
	until    int
	operator Operator
}

func canonicalEntries() ([]canonicalConstraint, []int) {
	return []canonicalConstraint{
		{value: 1, until: 5, operator: OperatorGTE},
		{value: 2, until: 6, operator: OperatorLTE},
		{value: 3, until: 7, operator: OperatorEQ},
	}, []int{10, 20, 30}
}

func TestBuildCanonicalizesRepeatedExactAllChildren(t *testing.T) {
	constructors := []struct {
		name string
		rule func() Rule[canonicalConstraint]
	}{
		{"Equality", func() Rule[canonicalConstraint] {
			return Include(func(v canonicalConstraint) (int, bool) { return v.value, true })
		}},
		{"Ordered", func() Rule[canonicalConstraint] {
			return GreaterOrEqual(func(v canonicalConstraint) (int, bool) { return v.value, true }, cmp.Compare[int])
		}},
		{"Between", func() Rule[canonicalConstraint] {
			return Between(
				func(v canonicalConstraint) (int, bool) { return v.value, true },
				func(v canonicalConstraint) (int, bool) { return v.until, true },
				cmp.Compare[int],
			)
		}},
		{"CompareBy", func() Rule[canonicalConstraint] {
			return CompareBy(
				func(v canonicalConstraint) (int, bool) { return v.value, true },
				func(v canonicalConstraint) (Operator, bool) { return v.operator, true },
				cmp.Compare[int],
			)
		}},
	}
	constraints, ids := canonicalEntries()
	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			for _, aliases := range []int{2, 4, 8} {
				t.Run(fmt.Sprint(aliases), func(t *testing.T) {
					shared := constructor.rule()
					children := make([]Rule[canonicalConstraint], aliases)
					for i := range children {
						children[i] = shared
					}
					index, err := New[canonicalConstraint, int](All(children...)).Build(Zip(constraints, ids))
					require.NoError(t, err)
					require.Equal(t, 1, index.nodes)
					_, remainsAll := index.root.(*allRule[canonicalConstraint])
					require.False(t, remainsAll)

					var want, got []int
					single, err := New[canonicalConstraint, int](shared).Build(Zip(constraints, ids))
					require.NoError(t, err)
					single.Search(constraints[1], &want)
					index.Search(constraints[1], &got)
					require.Equal(t, want, got)
				})
			}
		})
	}
}

func TestBuildCanonicalizesRepeatedChildrenThroughNestedAll(t *testing.T) {
	shared := Include(func(v canonicalConstraint) (int, bool) { return v.value, true })
	constraints, ids := canonicalEntries()
	index, err := New[canonicalConstraint, int](All(All(shared, shared), shared)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	require.Equal(t, 1, index.nodes)
	_, remainsAll := index.root.(*allRule[canonicalConstraint])
	require.False(t, remainsAll)
}

func TestBuildDoesNotCanonicalizeSimilarIndependentRules(t *testing.T) {
	getter := func(v canonicalConstraint) (int, bool) { return v.value, true }
	constraints, ids := canonicalEntries()
	index, err := New[canonicalConstraint, int](All(Include(getter), Include(getter))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	require.Equal(t, 2, index.nodes)
	require.IsType(t, &allRule[canonicalConstraint]{}, index.root)
}

func TestBuildCanonicalizationPreservesSeparateInspectSites(t *testing.T) {
	shared := Include(func(v canonicalConstraint) (int, bool) { return v.value, true })
	var first, second Inspector
	constraints, ids := canonicalEntries()
	index, err := New[canonicalConstraint, int](All(
		Inspect(&first, shared),
		Inspect(&second, shared),
	)).Build(Zip(constraints, ids))
	require.NoError(t, err)
	require.Equal(t, 1, index.nodes)
	require.Equal(t, uint64(len(constraints)), first.Snapshot().EntryCount())
	require.Equal(t, uint64(len(constraints)), second.Snapshot().EntryCount())

	var matches []int
	index.Search(constraints[0], &matches)
	require.Equal(t, []int{10}, matches)
}
