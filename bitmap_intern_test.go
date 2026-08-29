package ruleix

import (
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
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
	require.NotZero(t, left.wildcardSource)
	require.Equal(t, left.wildcardSource, right.wildcardSource)
	require.NotZero(t, left.set.source)
	require.Equal(t, left.set.source, right.set.source)
	require.NotEqual(t, left.wildcardSource, left.set.source)

	var matches []int
	index.Search(constraint{left: &concrete, right: &concrete}, &matches)
	require.Len(t, matches, len(constraints))
}

func TestBitmapInternerAssignsIDsAfterCollisionCheckedEquality(t *testing.T) {
	interner := newBitmapInterner()
	fingerprint := bitmapFingerprint{checksum: 1, cardinality: 2}
	first := roaring.BitmapOf(1, 2)
	equal := roaring.BitmapOf(1, 2)
	collision := roaring.BitmapOf(3, 4)
	equalCollision := roaring.BitmapOf(3, 4)

	firstID := interner.internSourceWithFingerprint(&first, fingerprint)
	equalID := interner.internSourceWithFingerprint(&equal, fingerprint)
	collisionID := interner.internSourceWithFingerprint(&collision, fingerprint)
	equalCollisionID := interner.internSourceWithFingerprint(&equalCollision, fingerprint)

	require.NotZero(t, firstID)
	require.Equal(t, firstID, equalID)
	require.Same(t, first, equal)
	require.NotEqual(t, firstID, collisionID)
	require.Equal(t, collisionID, equalCollisionID)
	require.Same(t, collision, equalCollision)
}

func TestPhysicalSourceIDsAreBuildScoped(t *testing.T) {
	firstInterner := newBitmapInterner()
	secondInterner := newBitmapInterner()
	first, second := roaring.BitmapOf(1), roaring.BitmapOf(2)

	require.Equal(t, physicalSourceID(1), firstInterner.internSource(&first))
	require.Equal(t, physicalSourceID(1), secondInterner.internSource(&second))
}
