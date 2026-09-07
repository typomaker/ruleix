package ruleix_test

import (
	"fmt"
	"math/bits"
	"sort"
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

type equalityFlatPartition struct {
	bucketCount uint64
	buckets     map[uint64]*roaring.Bitmap
	bytes       uint64
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
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(v.customerSegment)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (bool, bool) {
			return benchmarkOptional(v.customerFraud)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(v.storeUUID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(v.deliveryAreaID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(v.regionID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) ([16]byte, bool) {
			return benchmarkOptional(v.retailerUUID)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(v.vertical)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (uint8, bool) {
			return benchmarkOptional(v.slotType)
		}),
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) (int, bool) {
			return benchmarkOptional(v.slotDayOfWeek)
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
		newEqualityAntonymTree(constraints, func(v productionBenchmarkConstraint) ([2]string, bool) {
			return benchmarkOptional(v.abTest)
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

func equalityFlatBucketCounts() []uint64 {
	counts := []uint64{1}
	for width := uint(1); width <= 16; width++ {
		upper := uint64(1) << width
		for numerator := uint64(5); numerator <= 8; numerator++ {
			count := upper * numerator / 8
			if count > counts[len(counts)-1] {
				counts = append(counts, count)
			}
		}
	}
	return counts
}

func buildEqualityFlatPartitions(tree *equalityAntonymTree) []equalityFlatPartition {
	partitions := make([]equalityFlatPartition, 0, 65)
	for _, bucketCount := range equalityFlatBucketCounts() {
		partition := equalityFlatPartition{
			bucketCount: bucketCount,
			buckets:     make(map[uint64]*roaring.Bitmap),
			bytes:       tree.wildcard.GetSerializedSizeInBytes(),
		}
		for _, entry := range tree.root.entries {
			bucket, _ := bits.Mul64(entry.hash, bucketCount)
			posting := partition.buckets[bucket]
			if posting == nil {
				posting = roaring.New()
				partition.buckets[bucket] = posting
			}
			posting.Add(entry.id)
		}
		for _, posting := range partition.buckets {
			partition.bytes += 8 + posting.GetSerializedSizeInBytes()
		}
		partitions = append(partitions, partition)
	}
	return partitions
}

func equalityFlatCandidates(
	trees []*equalityAntonymTree,
	partitions [][]equalityFlatPartition,
	selected []int,
	queries []productionBenchmarkConstraint,
) float64 {
	result := roaring.New()
	matches := roaring.New()
	total := uint64(0)
	for _, query := range queries {
		result.AddRange(0, productionBenchmarkEntries)
		for i, tree := range trees {
			matches.Clear()
			matches.Or(tree.wildcard)
			hash, ok := tree.queryHash(query)
			if ok {
				partition := partitions[i][selected[i]]
				bucket, _ := bits.Mul64(hash, partition.bucketCount)
				matches.Or(partition.buckets[bucket])
			}
			result.And(matches)
		}
		total += result.GetCardinality()
		result.Clear()
	}
	return float64(total) / float64(len(queries))
}

func buildEqualityFlatForest(
	trees []*equalityAntonymTree, queries []productionBenchmarkConstraint, budget uint64, seedUniform bool,
) (uint64, float64, []uint64) {
	partitions := make([][]equalityFlatPartition, len(trees))
	selected := make([]int, len(trees))
	used := uint64(0)
	for i, tree := range trees {
		partitions[i] = buildEqualityFlatPartitions(tree)
		used += partitions[i][0].bytes
	}
	if seedUniform {
		for option := 1; option < len(partitions[0]); option++ {
			nextUsed := uint64(0)
			for i := range trees {
				nextUsed += partitions[i][option].bytes
			}
			if nextUsed > budget {
				break
			}
			used = nextUsed
			for i := range selected {
				selected[i] = option
			}
		}
	}
	quality := equalityFlatCandidates(trees, partitions, selected, queries)
	for {
		bestTree := -1
		bestQuality := quality
		bestExtra := uint64(0)
		for i := range trees {
			next := selected[i] + 1
			if next == len(partitions[i]) {
				continue
			}
			currentBytes := partitions[i][selected[i]].bytes
			nextBytes := partitions[i][next].bytes
			if nextBytes < currentBytes || nextBytes-currentBytes > budget-used {
				continue
			}
			selected[i] = next
			candidateQuality := equalityFlatCandidates(trees, partitions, selected, queries)
			selected[i]--
			extra := nextBytes - currentBytes
			benefit := quality - candidateQuality
			bestBenefit := quality - bestQuality
			if benefit > 0 && (bestTree < 0 || benefit*float64(bestExtra) > bestBenefit*float64(extra)) {
				bestTree, bestQuality, bestExtra = i, candidateQuality, extra
			}
		}
		if bestTree < 0 {
			break
		}
		selected[bestTree]++
		used += bestExtra
		quality = bestQuality
	}
	counts := make([]uint64, len(trees))
	for i := range trees {
		counts[i] = partitions[i][selected[i]].bucketCount
	}
	sort.Slice(counts, func(i, j int) bool { return counts[i] < counts[j] })
	return used, quality, counts
}

func buildEqualityUniformFlatForest(
	trees []*equalityAntonymTree, queries []productionBenchmarkConstraint, budget uint64,
) (uint64, float64, uint64) {
	partitions := make([][]equalityFlatPartition, len(trees))
	for i, tree := range trees {
		partitions[i] = buildEqualityFlatPartitions(tree)
	}
	selected := make([]int, len(trees))
	bestUsed := uint64(0)
	bestQuality := float64(productionBenchmarkEntries)
	bestCount := uint64(1)
	for option := range partitions[0] {
		used := uint64(0)
		for i := range trees {
			selected[i] = option
			used += partitions[i][option].bytes
		}
		if used > budget {
			continue
		}
		quality := equalityFlatCandidates(trees, partitions, selected, queries)
		if quality < bestQuality {
			bestUsed, bestQuality = used, quality
			bestCount = partitions[0][option].bucketCount
		}
	}
	return bestUsed, bestQuality, bestCount
}

// BenchmarkProductionEqualityAntonymTree estimates adaptive prefix-tree
// candidate quality at the current equality-only 75% retained checkpoint.
// Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 38,098 entries, all 14 equality
// leaves, 1x: 8/16-byte nodes used 230,760/234,040 bytes and returned 4,975
// candidates/query; 20/24-byte nodes used 234,064/234,056 and returned 5,028.
// Wildcards are accounted separately and never enter tree nodes.
func BenchmarkProductionEqualityAntonymTree(b *testing.B) {
	constraints, _ := productionBenchmarkData()
	queries := []productionBenchmarkConstraint{productionBenchmarkQuery(100), productionBenchmarkQuery(101)}
	for _, overhead := range []uint64{8, 16, 20, 24} {
		trees := productionEqualityAntonymTrees(constraints)
		used := buildEqualityAntonymForest(trees, 234076, overhead)
		candidates := equalityAntonymCandidates(trees, queries)
		b.Run(fmt.Sprintf("NodeBytes%d", overhead), func(b *testing.B) {
			b.ReportMetric(float64(used), "accounted-B")
			b.ReportMetric(candidates, "candidates/query")
		})
	}
}

// BenchmarkProductionEqualityFlatPartition tests whether the antonym-tree
// objective can select a compact flat hash partition. Concrete-key postings
// alone form buckets; wildcard postings are only unioned during lookup.
// Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 38,098 entries, 1x: isolated greedy
// stopped at 209,480 B and 5,135 candidates; joint/uniform 65,536-bucket
// partitions used 233,032/231,976 B and both returned 3,803 candidates.
func BenchmarkProductionEqualityFlatPartition(b *testing.B) {
	constraints, _ := productionBenchmarkData()
	queries := []productionBenchmarkConstraint{productionBenchmarkQuery(100), productionBenchmarkQuery(101)}
	trees := productionEqualityAntonymTrees(constraints)
	used, candidates, counts := buildEqualityFlatForest(trees, queries, 234076, false)
	b.ReportMetric(float64(used), "accounted-B")
	b.ReportMetric(candidates, "candidates/query")
	b.ReportMetric(float64(counts[0]), "min-buckets")
	b.ReportMetric(float64(counts[len(counts)-1]), "max-buckets")
	jointUsed, jointCandidates, jointCounts := buildEqualityFlatForest(trees, queries, 234076, true)
	b.ReportMetric(float64(jointUsed), "joint-B")
	b.ReportMetric(jointCandidates, "joint-candidates/query")
	b.ReportMetric(float64(jointCounts[0]), "joint-min-buckets")
	b.ReportMetric(float64(jointCounts[len(jointCounts)-1]), "joint-max-buckets")
	uniformUsed, uniformCandidates, uniformCount := buildEqualityUniformFlatForest(trees, queries, 234076)
	b.ReportMetric(float64(uniformUsed), "uniform-B")
	b.ReportMetric(uniformCandidates, "uniform-candidates/query")
	b.ReportMetric(float64(uniformCount), "uniform-buckets")
}
