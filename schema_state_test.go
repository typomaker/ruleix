package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

type schemaStateConstraint struct {
	name  *string
	value *int
}

func TestSchemaStateUsesStableSequentialNodeIDs(t *testing.T) {
	schema := All(
		Include(func(v schemaStateConstraint) *string { return v.name }),
		All(
			GreaterOrEqual(func(v schemaStateConstraint) *int { return v.value }, func(a, b int) int { return a - b }),
			Exclude(func(v schemaStateConstraint) *string { return v.name }),
		),
	)

	first := schema.newState(&nodeIDAllocator{}).(*allRule[schemaStateConstraint])
	second := schema.newState(&nodeIDAllocator{}).(*allRule[schemaStateConstraint])

	firstEq := first.children[0].(*eqRule[schemaStateConstraint, string])
	firstNested := first.children[1].(*allRule[schemaStateConstraint])
	firstOrdered := firstNested.children[0].(*orderedRule[schemaStateConstraint, int])
	firstNot := firstNested.children[1].(*notRule[schemaStateConstraint, string])

	secondEq := second.children[0].(*eqRule[schemaStateConstraint, string])
	secondNested := second.children[1].(*allRule[schemaStateConstraint])
	secondOrdered := secondNested.children[0].(*orderedRule[schemaStateConstraint, int])
	secondNot := secondNested.children[1].(*notRule[schemaStateConstraint, string])

	require.Equal(t, []nodeID{0, 1, 2}, []nodeID{firstEq.nodeID, firstOrdered.nodeID, firstNot.nodeID})
	require.Equal(t, firstEq.nodeID, secondEq.nodeID)
	require.Equal(t, firstOrdered.nodeID, secondOrdered.nodeID)
	require.Equal(t, firstNot.nodeID, secondNot.nodeID)
	require.NotSame(t, firstEq.wildcard, secondEq.wildcard)
	require.NotSame(t, firstOrdered.wildcard, secondOrdered.wildcard)
	require.NotSame(t, firstNot.values, secondNot.values)
}
