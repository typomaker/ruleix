package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// SearchStrategy describes the execution strategy selected for one search.
// Explain is diagnostic: callers should not depend on a particular strategy.
type SearchStrategy string

const (
	SearchStrategySingle             SearchStrategy = "single"
	SearchStrategyCandidateScan      SearchStrategy = "candidate_scan"
	SearchStrategyBitmapIntersection SearchStrategy = "bitmap_intersection"
)

// PlanChild describes one direct child of the optimized top-level All rule.
// SchemaIndex is its zero-based position after build-time optimization.
type PlanChild struct {
	SchemaIndex       int
	ExecutionOrder    int
	EstimatedMatches  uint64
	EstimateAvailable bool
	ActualMatches     uint64
	// Materialized reports whether an ordinary Search would build this
	// child's result bitmap. Explain measures ActualMatches independently.
	Materialized bool
}

// SearchPlan is an immutable snapshot of the planner decisions for a query.
// Children is empty when the optimized root is not an All rule.
type SearchPlan struct {
	Strategy             SearchStrategy
	Empty                bool
	CandidateCardinality uint64
	ResultCardinality    uint64
	Children             []PlanChild
}

// Explain executes a diagnostic search and reports the estimates, actual
// cardinalities, ordering, and candidate-to-bitmap strategy selected by the
// planner. It does not alter the index or retain instrumentation for later
// searches. Explain is more expensive than Search because it measures every
// top-level All child, including children that candidate scanning would
// normally validate without materializing.
func (ix *Index[C, ID]) Explain(value C) SearchPlan {
	root, ok := ix.root.(*allRule[C])
	if !ok {
		return ix.explainSingle(value)
	}
	return ix.explainAll(root, value)
}

func (ix *Index[C, ID]) explainSingle(value C) SearchPlan {
	bits := ix.pool.get()
	defer ix.pool.put(bits)
	ix.root.search(value, bits, ix.pool)
	if len(ix.exclusions) != 0 {
		excluded := ix.pool.get()
		addExclusions(ix.exclusions, value, excluded, ix.pool)
		bits.AndNot(excluded)
		ix.pool.put(excluded)
	}
	cardinality := bits.GetCardinality()
	return SearchPlan{
		Strategy:          SearchStrategySingle,
		Empty:             cardinality == 0,
		ResultCardinality: cardinality,
	}
}

func (ix *Index[C, ID]) explainAll(root *allRule[C], value C) SearchPlan {
	children := make([]PlanChild, len(root.children))
	order := make([]int, len(root.children))
	for i, child := range root.children {
		children[i].SchemaIndex = i
		order[i] = i
		if estimator, ok := child.(cardinalityEstimator[C]); ok {
			children[i].EstimatedMatches = estimator.estimateCardinality(value)
			children[i].EstimateAvailable = true
		}
	}
	stablePlanOrder(order, children, false)
	for executionOrder, childIndex := range order {
		children[childIndex].ExecutionOrder = executionOrder
	}

	if len(order) == 0 {
		return SearchPlan{Strategy: SearchStrategyBitmapIntersection, Empty: true, Children: children}
	}
	strategy := SearchStrategyBitmapIntersection
	if children[order[0]].EstimateAvailable &&
		children[order[0]].EstimatedMatches <= allCandidateScanLimit {
		strategy = SearchStrategyCandidateScan
	}
	earlyEmpty := false
	for _, child := range root.children {
		if checker, ok := child.(cardinalityZeroChecker[C]); ok && checker.isCardinalityZero(value) {
			earlyEmpty = true
			break
		}
	}

	postings := make([]*roaring.Bitmap, len(root.children))
	for i, child := range root.children {
		bits := ix.pool.get()
		child.search(value, bits, ix.pool)
		postings[i] = bits
		children[i].ActualMatches = bits.GetCardinality()
		children[i].Materialized = strategy == SearchStrategyBitmapIntersection || i == order[0]
	}
	defer func() {
		for _, bits := range postings {
			ix.pool.put(bits)
		}
	}()
	if earlyEmpty {
		for i := range children {
			children[i].Materialized = false
		}
		return SearchPlan{Strategy: strategy, Empty: true, Children: children}
	}

	if strategy == SearchStrategyCandidateScan && children[order[0]].ActualMatches > allCandidateScanLimit {
		strategy = SearchStrategyBitmapIntersection
		for i := range children {
			children[i].Materialized = true
		}
	}
	if strategy == SearchStrategyBitmapIntersection {
		stablePlanOrder(order, children, true)
		for executionOrder, childIndex := range order {
			children[childIndex].ExecutionOrder = executionOrder
		}
	}
	candidateCardinality := children[order[0]].ActualMatches

	result := ix.pool.get()
	defer ix.pool.put(result)
	result.Or(postings[order[0]])
	for _, childIndex := range order[1:] {
		result.And(postings[childIndex])
		if result.IsEmpty() {
			break
		}
	}
	if len(ix.exclusions) != 0 {
		excluded := ix.pool.get()
		addExclusions(ix.exclusions, value, excluded, ix.pool)
		result.AndNot(excluded)
		ix.pool.put(excluded)
	}
	resultCardinality := result.GetCardinality()
	return SearchPlan{
		Strategy:             strategy,
		Empty:                resultCardinality == 0,
		CandidateCardinality: candidateCardinality,
		ResultCardinality:    resultCardinality,
		Children:             children,
	}
}

func stablePlanOrder(order []int, children []PlanChild, actual bool) {
	cardinality := func(index int) uint64 {
		if actual {
			return children[index].ActualMatches
		}
		if children[index].EstimateAvailable {
			return children[index].EstimatedMatches
		}
		return ^uint64(0)
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && cardinality(order[j]) < cardinality(order[j-1]); j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
}
