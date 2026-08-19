package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildInternsEqualPostingBitmaps(t *testing.T) {
	type constraint struct {
		left  *int
		right *int
	}
	getLeft := GetterFromPointer(func(value constraint) *int { return value.left })
	getRight := GetterFromPointer(func(value constraint) *int { return value.right })
	constraints := make([]constraint, 65)
	ids := make([]int, len(constraints))
	concrete := 7
	for id := range constraints {
		ids[id] = id
		if id != len(constraints)-1 {
			constraints[id] = constraint{left: &concrete, right: &concrete}
		}
	}

	index, err := New[constraint, int](All(Include(getLeft), Include(getRight))).Build(Zip(constraints, ids))
	require.NoError(t, err)
	root := index.root.(*allRule[constraint])
	left := root.children[0].(*unaryEqRule[constraint, int])
	right := root.children[1].(*unaryEqRule[constraint, int])
	require.Same(t, left.wildcard, right.wildcard)
	require.Same(t, left.set.bits, right.set.bits)

	var matches []int
	index.Search(constraint{left: &concrete, right: &concrete}, &matches)
	require.Len(t, matches, len(constraints))
}
