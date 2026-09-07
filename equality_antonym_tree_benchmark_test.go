package ruleix_test

import (
	"fmt"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/typomaker/ruleix"
)

type equalityAntonymEntry struct {
	hash uint64
	id   uint32
}

type equalityAntonymNode struct {
	entries     []equalityAntonymEntry
	bits        *roaring.Bitmap
	left, right *equalityAntonymNode
	depth       uint8
}

type equalityAntonymTree struct {
	root      *equalityAntonymNode
	wildcard  *roaring.Bitmap
	queryHash func(productionBenchmarkConstraint) (uint64, bool)
}

type equalityAntonymSplit struct {
	tree        *equalityAntonymTree
	node        *equalityAntonymNode
	left, right *equalityAntonymNode
	bytes       uint64
	benefit     uint64
}

func newEqualityAntonymTree[V comparable](
	constraints []productionBenchmarkConstraint,
	get func(productionBenchmarkConstraint) (V, bool),
) *equalityAntonymTree {
	root := &equalityAntonymNode{bits: roaring.New()}
	wildcard := roaring.New()
	for id, constraint := range constraints {
		value, ok := get(constraint)
		if !ok {
			wildcard.Add(uint32(id))
			continue
		}
		hash := ruleix.EqualityHashForTest(value)
		root.entries = append(root.entries, equalityAntonymEntry{hash: hash, id: uint32(id)})
		root.bits.Add(uint32(id))
	}
	return &equalityAntonymTree{
		root: root, wildcard: wildcard,
		queryHash: func(query productionBenchmarkConstraint) (uint64, bool) {
			value, ok := get(query)
			if !ok {
				return 0, false
			}
			return ruleix.EqualityHashForTest(value), true
		},
	}
}

func productionEqualityAntonymTrees(constraints []productionBenchmarkConstraint) []*equalityAntonymTree {
	return []*equalityAntonymTree{
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(v.customerUUID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(v.storeUUID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(v.regionID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(v.slotType)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (string, bool) {
			if v.platform == nil {
				return "", false
			}
			return v.platform.name, true
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (bool, bool) {
			return benchmarkOptional(v.dbs)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(v.marketType)
		}),
	}
}

func equalityAntonymNodeBytes(node *equalityAntonymNode, overhead uint64) uint64 {
	return overhead + node.bits.GetSerializedSizeInBytes()
}

func prepareEqualityAntonymSplit(
	tree *equalityAntonymTree, node *equalityAntonymNode, overhead uint64,
) (equalityAntonymSplit, bool) {
	if node.depth == 64 || len(node.entries) < 2 || node.left != nil {
		return equalityAntonymSplit{}, false
	}
	left := &equalityAntonymNode{depth: node.depth + 1, bits: roaring.New()}
	right := &equalityAntonymNode{depth: node.depth + 1, bits: roaring.New()}
	shift := 63 - node.depth
	for _, entry := range node.entries {
		dst := left
		if entry.hash&(uint64(1)<<shift) != 0 {
			dst = right
		}
		dst.entries = append(dst.entries, entry)
		dst.bits.Add(entry.id)
	}
	if len(left.entries) == 0 || len(right.entries) == 0 {
		child := left
		if len(child.entries) == 0 {
			child = right
		}
		child.depth = node.depth + 1
		return equalityAntonymSplit{tree: tree, node: node, left: child}, true
	}
	before := equalityAntonymNodeBytes(node, overhead)
	after := overhead + equalityAntonymNodeBytes(left, overhead) + equalityAntonymNodeBytes(right, overhead)
	leftCount, rightCount := uint64(len(left.entries)), uint64(len(right.entries))
	return equalityAntonymSplit{
		tree: tree, node: node, left: left, right: right,
		bytes: after - before, benefit: 2 * leftCount * rightCount,
	}, true
}

func applyEqualityAntonymSplit(split equalityAntonymSplit) {
	if split.right == nil {
		split.node.depth = split.left.depth
		return
	}
	split.node.left, split.node.right = split.left, split.right
	split.node.entries = nil
	split.node.bits = nil
}

func buildEqualityAntonymForest(trees []*equalityAntonymTree, budget, overhead uint64) uint64 {
	used := uint64(0)
	var splits []equalityAntonymSplit
	for _, tree := range trees {
		used += tree.wildcard.GetSerializedSizeInBytes() + equalityAntonymNodeBytes(tree.root, overhead)
		if split, ok := prepareEqualityAntonymSplit(tree, tree.root, overhead); ok {
			splits = append(splits, split)
		}
	}
	for len(splits) != 0 {
		best := -1
		for i, split := range splits {
			if used > budget || split.bytes > budget-used {
				continue
			}
			if best < 0 || split.benefit*splits[best].bytes > splits[best].benefit*split.bytes {
				best = i
			}
		}
		if best < 0 {
			return used
		}
		selected := splits[best]
		splits[best] = splits[len(splits)-1]
		splits = splits[:len(splits)-1]
		used += selected.bytes
		applyEqualityAntonymSplit(selected)
		next := []*equalityAntonymNode{selected.left, selected.right}
		if selected.right == nil {
			next = []*equalityAntonymNode{selected.node}
		}
		for _, node := range next {
			if node != nil && node.left == nil {
				if split, ok := prepareEqualityAntonymSplit(selected.tree, node, overhead); ok {
					splits = append(splits, split)
				}
			}
		}
	}
	return used
}

func equalityAntonymCandidates(
	trees []*equalityAntonymTree, queries []productionBenchmarkConstraint,
) float64 {
	result := roaring.New()
	matches := roaring.New()
	total := uint64(0)
	for _, query := range queries {
		result.AddRange(0, productionBenchmarkEntries)
		for _, tree := range trees {
			hash, ok := tree.queryHash(query)
			matches.Clear()
			matches.Or(tree.wildcard)
			if ok {
				node := tree.root
				for node.left != nil {
					if hash&(uint64(1)<<(63-node.depth)) == 0 {
						node = node.left
					} else {
						node = node.right
					}
				}
				matches.Or(node.bits)
			}
			result.And(matches)
		}
		total += result.GetCardinality()
		result.Clear()
	}
	return float64(total) / float64(len(queries))
}

// BenchmarkProductionEqualityAntonymTree estimates adaptive prefix-tree
// candidate quality at the current equality-only 75% retained checkpoint.
// Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 38,098 entries, 1x: 8/16-byte
// nodes used 230,554/232,276 bytes and returned the Exact-equivalent 150
// candidates/query; 24-byte nodes used 233,402 bytes and returned 1,485.
func BenchmarkProductionEqualityAntonymTree(b *testing.B) {
	constraints, _ := productionBenchmarkData()
	queries := []productionBenchmarkConstraint{productionBenchmarkQuery(100), productionBenchmarkQuery(101)}
	for _, overhead := range []uint64{8, 16, 24} {
		trees := productionEqualityAntonymTrees(constraints)
		used := buildEqualityAntonymForest(trees, 234076, overhead)
		candidates := equalityAntonymCandidates(trees, queries)
		b.Run(fmt.Sprintf("NodeBytes%d", overhead), func(b *testing.B) {
			b.ReportMetric(float64(used), "accounted-B")
			b.ReportMetric(candidates, "candidates/query")
		})
	}
}
