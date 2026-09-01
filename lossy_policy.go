package ruleix

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

type lossyAllPlanner[T any] interface {
	compile(uint64) (Rule[T], error)
	representationLadder() ([]lossyRepresentation[T], error)
}

type lossyAllCompiler[T any] interface{ newLossyAllPlanner() lossyAllPlanner[T] }

type inspectionDetailsRule[T any] struct {
	child          Rule[T]
	details        inspectionDetails
	canonicalAlias bool
}

func (*inspectionDetailsRule[T]) rule() {}
func (r *inspectionDetailsRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	child, reused := canonicalRuleStateReuse(r.child, ids, hints)
	return &inspectionDetailsRule[T]{child: child, details: r.details, canonicalAlias: reused}
}
func (r *inspectionDetailsRule[T]) validate(v T) error { return r.child.validate(v) }
func (r *inspectionDetailsRule[T]) insert(v T, id uint32) {
	if !r.canonicalAlias {
		r.child.insert(v, id)
	}
}
func (r *inspectionDetailsRule[T]) cardinality(v T, p *bitmapPool) uint64 {
	return r.child.cardinality(v, p)
}
func (r *inspectionDetailsRule[T]) search(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.child.search(v, dst, p)
}
func (r *inspectionDetailsRule[T]) estimateCachedCardinality(v T, p *bitmapPool) (uint64, bool) {
	estimator, ok := r.child.(cachedCardinalityEstimator[T])
	if !ok {
		return 0, false
	}
	return estimator.estimateCachedCardinality(v, p)
}
func (r *inspectionDetailsRule[T]) lookupCachedBitmap(v T, p *bitmapPool) (*roaring.Bitmap, bool) {
	provider, ok := r.child.(cachedBitmapProvider[T])
	if !ok {
		return nil, false
	}
	return provider.lookupCachedBitmap(v, p)
}
func (r *inspectionDetailsRule[T]) exclude(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.child.exclude(v, dst, p)
}
func (r *inspectionDetailsRule[T]) collectBuildStatistics(s []nodeBuildStatistics) {
	r.child.collectBuildStatistics(s)
}
func (r *inspectionDetailsRule[T]) optimize(total uint64) Rule[T] {
	return &inspectionDetailsRule[T]{child: optimizeRule(r.child, total), details: r.details}
}
func (r *inspectionDetailsRule[T]) inspectionStrategy() string           { return inspectionStrategyOf(r.child) }
func (r *inspectionDetailsRule[T]) inspectionMode() RuleMode             { return inspectionModeOf(r.child) }
func (r *inspectionDetailsRule[T]) inspectionDetails() inspectionDetails { return r.details }

// compileLossyRules finds the outermost policy boundaries. Each boundary is
// planned as one tree so nested caps participate in ancestor allocation.
func compileLossyRules[T any](rule Rule[T], identity bool) (Rule[T], error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		return compileLossyPolicyTree(typed, identity)
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			compiled, err := compileLossyRules(child, identity)
			if err != nil {
				return nil, err
			}
			children[i] = compiled
		}
		return &allRule[T]{children: children}, nil
	case *inspectRule[T]:
		child, err := compileLossyRules(typed.child, identity)
		if err != nil {
			return nil, err
		}
		return &inspectRule[T]{dst: typed.dst, child: child}, nil
	default:
		return rule, nil
	}
}

type lossyPolicyPlanKind uint8

const (
	lossyPlanLeaf lossyPolicyPlanKind = iota
	lossyPlanAll
	lossyPlanInspect
	lossyPlanPolicy
)

type lossyPolicyPlan[T any] struct {
	kind       lossyPolicyPlanKind
	original   Rule[T]
	children   []*lossyPolicyPlan[T]
	leaf       int
	first, end int
	limit      uint64
	effective  uint64
	path       string
}

func compileLossyPolicyTree[T any](rule *lossyRule[T], identity bool) (Rule[T], error) {
	leaves := make([]lossyAllLeaf[T], 0, 8)
	root, err := analyzeLossyPolicy(rule, "Lossy", &leaves)
	if err != nil {
		return nil, err
	}
	for i := range leaves {
		leaves[i].compiled = leaves[i].ladder[0].compiled
	}
	if !identity {
		if err := enforceLossyPolicyCaps(root, leaves); err != nil {
			return nil, err
		}
	}
	assignEffectiveLossyPolicyLimits(root, leaves, nil)
	compiled, _, err := materializeLossyPolicy(root, leaves)
	if err == nil && identity {
		compiled = clearIdentityLossyLimits(compiled)
	}
	return compiled, err
}

// clearIdentityLossyLimits prevents final streaming refresh from enforcing a
// cap that the internal identity control intentionally does not represent.
func clearIdentityLossyLimits[T any](rule Rule[T]) Rule[T] {
	switch typed := rule.(type) {
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			children[i] = clearIdentityLossyLimits(child)
		}
		return &allRule[T]{children: children}
	case *inspectRule[T]:
		return &inspectRule[T]{dst: typed.dst, child: clearIdentityLossyLimits(typed.child)}
	case *inspectionDetailsRule[T]:
		details := typed.details
		details.MemoryLimitBytes, details.MemoryLimitAvailable = 0, false
		return &inspectionDetailsRule[T]{child: clearIdentityLossyLimits(typed.child), details: details}
	default:
		return rule
	}
}

type lossyAncestorLimit struct {
	first, end int
	limit      uint64
}

// assignEffectiveLossyPolicyLimits derives the maximum retained usage each
// policy subtree could have under the selected sibling representations while
// still satisfying every ancestor cap. It records diagnostics only; planning
// remains complete and immutable before this pass runs.
func assignEffectiveLossyPolicyLimits[T any](
	plan *lossyPolicyPlan[T],
	leaves []lossyAllLeaf[T],
	ancestors []lossyAncestorLimit,
) {
	if plan.kind == lossyPlanPolicy {
		plan.effective = plan.limit
		subtreeUsage, _ := lossyLeafRangeUsage(leaves, plan.first, plan.end)
		for _, ancestor := range ancestors {
			ancestorUsage, _ := lossyLeafRangeUsage(leaves, ancestor.first, ancestor.end)
			outsideUsage := ancestorUsage - subtreeUsage
			available := ancestor.limit - outsideUsage
			plan.effective = min(plan.effective, available)
		}
		ancestors = append(ancestors, lossyAncestorLimit{first: plan.first, end: plan.end, limit: plan.effective})
	}
	for _, child := range plan.children {
		assignEffectiveLossyPolicyLimits(child, leaves, ancestors)
	}
}

// analyzeLossyPolicy preserves policy and inspection boundaries. Ordinary All
// nodes only contribute structure; their leaves share the nearest policy.
func analyzeLossyPolicy[T any](rule Rule[T], path string, leaves *[]lossyAllLeaf[T]) (*lossyPolicyPlan[T], error) {
	plan := &lossyPolicyPlan[T]{original: rule, first: len(*leaves), leaf: -1, path: path}
	switch typed := rule.(type) {
	case *lossyRule[T]:
		if err := typed.validatePolicy(); err != nil {
			return nil, fmt.Errorf("ruleix: %s: %w", path, err)
		}
		plan.kind, plan.limit = lossyPlanPolicy, typed.limit
		child, err := analyzeLossyPolicy(typed.child, path+"/child", leaves)
		if err != nil {
			return nil, err
		}
		plan.children = []*lossyPolicyPlan[T]{child}
	case *allRule[T]:
		plan.kind = lossyPlanAll
		plan.children = make([]*lossyPolicyPlan[T], len(typed.children))
		for i, child := range typed.children {
			compiled, err := analyzeLossyPolicy(child, fmt.Sprintf("%s/All[%d]", path, i), leaves)
			if err != nil {
				return nil, err
			}
			plan.children[i] = compiled
		}
	case *inspectRule[T]:
		plan.kind = lossyPlanInspect
		child, err := analyzeLossyPolicy(typed.child, path+"/Inspect", leaves)
		if err != nil {
			return nil, err
		}
		plan.children = []*lossyPolicyPlan[T]{child}
	default:
		factory, ok := rule.(lossyAllCompiler[T])
		if !ok {
			return nil, fmt.Errorf("ruleix: %s: Lossy does not support this rule representation", path)
		}
		planner := factory.newLossyAllPlanner()
		ladder, err := planner.representationLadder()
		if err != nil {
			return nil, fmt.Errorf("ruleix: %s: %w", path, err)
		}
		if len(ladder) == 0 || !ladder[0].details.MemoryUsageAvailable {
			return nil, fmt.Errorf("ruleix: %s: Lossy rule has no viable accounted representation", path)
		}
		plan.kind, plan.leaf = lossyPlanLeaf, len(*leaves)
		*leaves = append(*leaves, lossyAllLeaf[T]{
			planner: planner,
			ladder:  ladder,
			exact:   ladder[0].details.MemoryUsageBytes,
		})
	}
	plan.end = len(*leaves)
	return plan, nil
}

func enforceLossyPolicyCaps[T any](plan *lossyPolicyPlan[T], leaves []lossyAllLeaf[T]) error {
	for _, child := range plan.children {
		if err := enforceLossyPolicyCaps(child, leaves); err != nil {
			return err
		}
	}
	if plan.kind != lossyPlanPolicy {
		return nil
	}
	minimum, ok := lossyLeafRangeMinimum(leaves, plan.first, plan.end)
	if !ok {
		return fmt.Errorf("ruleix: %s: memory accounting overflow", plan.path)
	}
	if minimum > plan.limit {
		return fmt.Errorf("ruleix: %s cannot fit the memory limit", plan.path)
	}
	usage, ok := lossyLeafRangeUsage(leaves, plan.first, plan.end)
	if !ok {
		return fmt.Errorf("ruleix: %s: memory accounting overflow", plan.path)
	}
	for usage > plan.limit {
		best := selectLossyAllDowngrade(leaves[plan.first:plan.end])
		if best < 0 {
			return fmt.Errorf("ruleix: %s cannot fit the memory limit", plan.path)
		}
		best += plan.first
		current := leaves[best].ladder[leaves[best].selected].details.MemoryUsageBytes
		leaves[best].selected++
		next := leaves[best].ladder[leaves[best].selected]
		leaves[best].compiled = next.compiled
		usage -= current - next.details.MemoryUsageBytes
	}
	return nil
}

func lossyLeafRangeUsage[T any](leaves []lossyAllLeaf[T], first, end int) (uint64, bool) {
	var total uint64
	for i := first; i < end; i++ {
		usage := leaves[i].ladder[leaves[i].selected].details.MemoryUsageBytes
		var ok bool
		total, ok = addLossyMemory(total, usage)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func lossyLeafRangeMinimum[T any](leaves []lossyAllLeaf[T], first, end int) (uint64, bool) {
	var total uint64
	for i := first; i < end; i++ {
		ladder := leaves[i].ladder
		var ok bool
		total, ok = addLossyMemory(total, ladder[len(ladder)-1].details.MemoryUsageBytes)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func materializeLossyPolicy[T any](
	plan *lossyPolicyPlan[T],
	leaves []lossyAllLeaf[T],
) (Rule[T], inspectionDetails, error) {
	switch plan.kind {
	case lossyPlanLeaf:
		leaf := leaves[plan.leaf]
		return leaf.compiled, inspectionDetailsOf(leaf.compiled), nil
	case lossyPlanAll:
		children := make([]Rule[T], len(plan.children))
		var aggregate inspectionDetails
		for i, childPlan := range plan.children {
			child, details, err := materializeLossyPolicy(childPlan, leaves)
			if err != nil {
				return nil, inspectionDetails{}, err
			}
			children[i] = child
			aggregateLossyDetails(&aggregate, details)
		}
		return &allRule[T]{children: children}, aggregate, nil
	case lossyPlanInspect:
		child, details, err := materializeLossyPolicy(plan.children[0], leaves)
		if err != nil {
			return nil, inspectionDetails{}, err
		}
		return &inspectRule[T]{
			dst:   plan.original.(*inspectRule[T]).dst,
			child: &inspectionDetailsRule[T]{child: child, details: details},
		}, details, nil
	case lossyPlanPolicy:
		child, details, err := materializeLossyPolicy(plan.children[0], leaves)
		if err != nil {
			return nil, inspectionDetails{}, err
		}
		details.MemoryLimitBytes, details.MemoryLimitAvailable = plan.effective, true
		if rootInspectorBelongsToPolicy(plan.children[0]) {
			child = applyLossyPolicyDetailsToRootInspector(child, details)
		}
		return &inspectionDetailsRule[T]{child: child, details: details}, details, nil
	default:
		return nil, inspectionDetails{}, fmt.Errorf("ruleix: invalid Lossy policy plan")
	}
}

func rootInspectorBelongsToPolicy[T any](plan *lossyPolicyPlan[T]) bool {
	for plan.kind == lossyPlanInspect {
		plan = plan.children[0]
	}
	return plan.kind != lossyPlanPolicy
}

// Inspect directly inside Lossy owns the same policy view as Inspect outside
// it. Stop at the first non-inspection node so nested policy ownership remains
// intact.
func applyLossyPolicyDetailsToRootInspector[T any](rule Rule[T], details inspectionDetails) Rule[T] {
	inspected, ok := rule.(*inspectRule[T])
	if !ok {
		return rule
	}
	child := inspected.child
	if wrapped, ok := child.(*inspectionDetailsRule[T]); ok {
		child = &inspectionDetailsRule[T]{child: wrapped.child, details: details}
	}
	return &inspectRule[T]{dst: inspected.dst, child: child}
}
