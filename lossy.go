package ruleix

import (
	"fmt"
	"math"
	"math/bits"
	"reflect"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

// LossyOption configures a memory-bounded representation selected during Build.
// Implementations are sealed so new options can be added without weakening
// validation of a schema.
type LossyOption interface{ lossyOption() }

type memoryLimitOption struct{ bytes uint64 }

func (memoryLimitOption) lossyOption() {}

// MemoryLimit sets the maximum accounted bytes retained by one Lossy rule.
func MemoryLimit(bytes uint64) LossyOption { return memoryLimitOption{bytes: bytes} }

// Lossy permits rule to use a conservative approximation when its exact
// representation does not fit the configured memory limit. Approximate
// searches can return false positives, but never omit an exact match.
func Lossy[T any](rule Rule[T], options ...LossyOption) Rule[T] {
	if rule == nil {
		panic("ruleix: nil lossy rule")
	}
	return &lossyRule[T]{child: rule, options: options}
}

type lossyRule[T any] struct {
	child   Rule[T]
	options []LossyOption
	limit   uint64
}

func (*lossyRule[T]) rule() {}
func (r *lossyRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &lossyRule[T]{child: r.child.newState(ids, hints), options: r.options, limit: r.limit}
}
func (r *lossyRule[T]) validate(v T) error {
	if err := r.validatePolicy(); err != nil {
		return err
	}
	return r.child.validate(v)
}

// validateLossyPolicies reports malformed policies with the same stable schema
// paths used by planning errors. It runs once before entry validation so nested
// policy structure does not add per-entry build work.
func validateLossyPolicies[T any](rule Rule[T], path string) error {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		if err := typed.validatePolicy(); err != nil {
			return fmt.Errorf("ruleix: %s: %w", path, err)
		}
		return validateLossyPolicies(typed.child, path+"/child")
	case *allRule[T]:
		for i, child := range typed.children {
			if err := validateLossyPolicies(child, fmt.Sprintf("%s/All[%d]", path, i)); err != nil {
				return err
			}
		}
	case *inspectRule[T]:
		return validateLossyPolicies(typed.child, path+"/Inspect")
	}
	return nil
}
func (r *lossyRule[T]) validatePolicy() error {
	if r.limit != 0 {
		return nil
	}
	for _, option := range r.options {
		value, ok := option.(memoryLimitOption)
		if !ok {
			return fmt.Errorf("ruleix: invalid Lossy option")
		}
		if r.limit != 0 {
			return fmt.Errorf("ruleix: Lossy requires exactly one MemoryLimit")
		}
		r.limit = value.bytes
	}
	if r.limit == 0 {
		return fmt.Errorf("ruleix: Lossy requires one non-zero MemoryLimit")
	}
	return nil
}
func (r *lossyRule[T]) insert(v T, id uint32)                           { r.child.insert(v, id) }
func (r *lossyRule[T]) cardinality(v T, p *bitmapPool) uint64           { return r.child.cardinality(v, p) }
func (r *lossyRule[T]) search(v T, dst *roaring.Bitmap, p *bitmapPool)  { r.child.search(v, dst, p) }
func (r *lossyRule[T]) exclude(v T, dst *roaring.Bitmap, p *bitmapPool) { r.child.exclude(v, dst, p) }
func (r *lossyRule[T]) collectBuildStatistics(s []nodeBuildStatistics) {
	r.child.collectBuildStatistics(s)
}

type lossyCompiler[T any] interface{ compileLossy(uint64) (Rule[T], error) }

type lossyRepresentation[T any] struct {
	compiled Rule[T]
	details  inspectionDetails
}

const lossyBuildPressureInterval = 4096

// lossyBuildTarget is deliberately private: MemoryLimit remains the only
// public and hard retained-memory contract. Saturation keeps MaxUint64 limits
// useful for disabling pressure without wrapping the soft target.
func lossyBuildTarget(limit uint64) uint64 {
	headroom := limit / 4
	if math.MaxUint64-limit < headroom {
		return math.MaxUint64
	}
	return limit + headroom
}

// lossyBuildPressure returns deterministic Ruleix accounting for exact state
// beneath the outermost policy and the smallest soft target that owns it.
// Nested caps are already enforced by final policy compilation; choosing the
// smallest target here ensures an ancestor cannot hide child pressure.
func lossyBuildPressure[T any](rule Rule[T]) (usage, target uint64, available bool, err error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		var leaves []lossyAllLeaf[T]
		_, err = analyzeLossyPolicy(typed, "Lossy", &leaves)
		if err != nil {
			return 0, 0, false, err
		}
		usage, available = lossyLeafRangeUsage(leaves, 0, len(leaves))
		if !available {
			return 0, 0, false, fmt.Errorf("ruleix: Lossy build working memory accounting overflow")
		}
		return usage, lossyBuildTarget(typed.limit), true, nil
	case *allRule[T]:
		for _, child := range typed.children {
			childUsage, childTarget, ok, childErr := lossyBuildPressure(child)
			if childErr != nil {
				return 0, 0, false, childErr
			}
			if !ok {
				continue
			}
			if childUsage > childTarget {
				return childUsage, childTarget, true, nil
			}
			if !available || childTarget < target {
				usage, target, available = childUsage, childTarget, true
			}
		}
	case *inspectRule[T]:
		return lossyBuildPressure(typed.child)
	}
	return usage, target, available, nil
}

// streamingLossyLeaf is a conservative safety net for a future lossy
// representation that does not yet implement operator-specific insertion.
// Every built-in representation implements streamingLossyAccumulator, so this
// wrapper is not present in production indexes built from the public rules.
type streamingLossyLeaf[T any] struct {
	child Rule[T]
	tail  *roaring.Bitmap
}

type streamingUniversalProvider interface {
	streamingUniversal() (nodeID, *roaring.Bitmap, string)
}

// streamingLossyAccumulator marks a compiled lossy search representation that
// can also accept the remainder of the one-pass build directly. The marker is
// build-only: the published search method is unchanged.
type streamingLossyAccumulator interface{ streamingLossyAccumulator() }

type streamingDetailsProvider interface {
	refreshedStreamingDetails(inspectionDetails) inspectionDetails
}

type streamingLimitFitter interface{ fitStreamingLimit(uint64) }

func fitStreamingRule[T any](rule Rule[T], limit uint64) {
	switch typed := rule.(type) {
	case *allRule[T]:
		for _, child := range typed.children {
			fitStreamingRule(child, limit)
		}
	case *inspectRule[T]:
		fitStreamingRule(typed.child, limit)
	case *inspectionDetailsRule[T]:
		fitStreamingRule(typed.child, limit)
	default:
		if fitter, ok := any(rule).(streamingLimitFitter); ok {
			fitter.fitStreamingLimit(limit)
		}
	}
}

type streamingFitCandidate struct {
	fitter      streamingLimitFitter
	usage       uint64
	granularity uint64
}

func collectStreamingFitCandidates[T any](rule Rule[T], candidates *[]streamingFitCandidate) uint64 {
	switch typed := rule.(type) {
	case *allRule[T]:
		var usage uint64
		for _, child := range typed.children {
			usage = saturatingAdd(usage, collectStreamingFitCandidates(child, candidates))
		}
		return usage
	case *inspectRule[T]:
		return collectStreamingFitCandidates(typed.child, candidates)
	case *inspectionDetailsRule[T]:
		return collectStreamingFitCandidates(typed.child, candidates)
	default:
		provider, hasDetails := any(rule).(streamingDetailsProvider)
		if !hasDetails {
			return inspectionDetailsOf(rule).MemoryUsageBytes
		}
		details := provider.refreshedStreamingDetails(inspectionDetailsOf(rule))
		if fitter, ok := any(rule).(streamingLimitFitter); ok &&
			details.GranularityAvailable && details.GranularityValue > 1 {
			*candidates = append(*candidates, streamingFitCandidate{
				fitter: fitter, usage: details.MemoryUsageBytes, granularity: details.GranularityValue,
			})
		}
		return details.MemoryUsageBytes
	}
}

// fitStreamingAggregate releases one build-time bucket level at a time until
// the complete published subtree satisfies its aggregate retained-memory
// limit. It avoids the old emergency path that collapsed every lossy child to
// its minimum representation at once.
func fitStreamingAggregate[T any](rule Rule[T], limit uint64) {
	for {
		var candidates []streamingFitCandidate
		usage := collectStreamingFitCandidates(rule, &candidates)
		if usage <= limit || len(candidates) == 0 {
			return
		}
		selected := 0
		for i := 1; i < len(candidates); i++ {
			if candidates[i].usage > candidates[selected].usage ||
				(candidates[i].usage == candidates[selected].usage &&
					candidates[i].granularity > candidates[selected].granularity) {
				selected = i
			}
		}
		candidate := candidates[selected]
		candidate.fitter.fitStreamingLimit(candidate.usage - 1)
	}
}

func (*streamingLossyLeaf[T]) rule()                                                 {}
func (r *streamingLossyLeaf[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (r *streamingLossyLeaf[T]) validate(v T) error                                  { return r.child.validate(v) }
func (r *streamingLossyLeaf[T]) insert(_ T, id uint32)                               { r.tail.Add(id) }
func (r *streamingLossyLeaf[T]) cardinality(v T, p *bitmapPool) uint64 {
	return r.child.cardinality(v, p) + r.tail.GetCardinality()
}
func (r *streamingLossyLeaf[T]) search(v T, dst *roaring.Bitmap, p *bitmapPool) {
	r.child.search(v, dst, p)
	dst.Or(r.tail)
}
func (r *streamingLossyLeaf[T]) exclude(T, *roaring.Bitmap, *bitmapPool) {}
func (r *streamingLossyLeaf[T]) collectBuildStatistics(s []nodeBuildStatistics) {
	r.child.collectBuildStatistics(s)
}
func (*streamingLossyLeaf[T]) inspectionMode() RuleMode { return RuleModeLossy }
func (r *streamingLossyLeaf[T]) inspectionStrategy() string {
	return inspectionStrategyOf(r.child)
}
func (r *streamingLossyLeaf[T]) prepareSearch() {
	prepareRuleSearch(r.child)
	prepareBitmapForSearch(r.tail)
}
func (r *streamingLossyLeaf[T]) internBitmaps(interner *bitmapInterner) {
	if child, ok := r.child.(bitmapInternable); ok {
		child.internBitmaps(interner)
	}
	interner.intern(&r.tail)
}

func wrapStreamingLossyLeaves[T any](rule Rule[T]) Rule[T] {
	switch typed := rule.(type) {
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			children[i] = wrapStreamingLossyLeaves(child)
		}
		return &allRule[T]{children: children}
	case *inspectRule[T]:
		return &inspectRule[T]{dst: typed.dst, child: wrapStreamingLossyLeaves(typed.child)}
	case *inspectionDetailsRule[T]:
		return &inspectionDetailsRule[T]{child: wrapStreamingLossyLeaves(typed.child), details: typed.details}
	default:
		if inspectionModeOf(rule) == RuleModeLossy {
			if _, ok := any(rule).(streamingLossyAccumulator); ok {
				return rule
			}
			if provider, ok := any(rule).(streamingUniversalProvider); ok {
				node, bits, name := provider.streamingUniversal()
				return &lossyUniversalRule[T]{nodeID: node, bits: bits, name: name}
			}
			return &streamingLossyLeaf[T]{child: rule, tail: roaring.New()}
		}
		return rule
	}
}

func refreshStreamingLossyDetails[T any](rule Rule[T]) (Rule[T], inspectionDetails, error) {
	switch typed := rule.(type) {
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		var aggregate inspectionDetails
		for i, child := range typed.children {
			refreshed, details, err := refreshStreamingLossyDetails(child)
			if err != nil {
				return nil, inspectionDetails{}, err
			}
			children[i] = refreshed
			aggregateLossyDetails(&aggregate, details)
		}
		return &allRule[T]{children: children}, aggregate, nil
	case *inspectRule[T]:
		child, details, err := refreshStreamingLossyDetails(typed.child)
		return &inspectRule[T]{dst: typed.dst, child: child}, details, err
	case *inspectionDetailsRule[T]:
		if typed.details.MemoryLimitAvailable {
			fitStreamingRule(typed.child, typed.details.MemoryLimitBytes)
		}
		child, details, err := refreshStreamingLossyDetails(typed.child)
		if err != nil {
			return nil, inspectionDetails{}, err
		}
		if typed.details.MemoryLimitAvailable {
			if fitter, ok := any(child).(streamingLimitFitter); ok {
				fitter.fitStreamingLimit(typed.details.MemoryLimitBytes)
			}
		}
		if provider, ok := any(child).(streamingDetailsProvider); ok {
			details = provider.refreshedStreamingDetails(typed.details)
		} else if streaming, ok := child.(*streamingLossyLeaf[T]); ok {
			details = typed.details
			details.MemoryUsageBytes += bitmapBytes(streaming.tail)
			details.Items += streaming.tail.GetCardinality()
			details.MemoryUsageAvailable, details.ItemsAvailable = true, true
		} else if !details.MemoryUsageAvailable {
			details = typed.details
		}
		if typed.details.MemoryLimitAvailable {
			details.MemoryLimitBytes, details.MemoryLimitAvailable = typed.details.MemoryLimitBytes, true
			if details.MemoryUsageBytes > details.MemoryLimitBytes {
				fitStreamingAggregate(child, details.MemoryLimitBytes)
				child, details, err = refreshStreamingLossyDetails(child)
				if err != nil {
					return nil, inspectionDetails{}, err
				}
				details.MemoryLimitBytes, details.MemoryLimitAvailable = typed.details.MemoryLimitBytes, true
				if details.MemoryUsageBytes > details.MemoryLimitBytes {
					return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy streaming state cannot fit the memory limit")
				}
			}
		}
		return &inspectionDetailsRule[T]{child: child, details: details}, details, nil
	case *streamingLossyLeaf[T]:
		details := inspectionDetailsOf(typed.child)
		details.MemoryUsageBytes += bitmapBytes(typed.tail)
		details.Items += typed.tail.GetCardinality()
		details.MemoryUsageAvailable, details.ItemsAvailable = true, true
		return typed, details, nil
	default:
		return rule, inspectionDetailsOf(rule), nil
	}
}

// lossyAllPlanner exposes a finite exact-to-minimum representation ladder.
// compile keeps the existing single-leaf limit behavior; aggregate planning
// consumes the ladder directly instead of probing arbitrary byte limits.
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
func compileLossyRules[T any](rule Rule[T]) (Rule[T], error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		return compileLossyPolicyTree(typed)
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			compiled, err := compileLossyRules(child)
			if err != nil {
				return nil, err
			}
			children[i] = compiled
		}
		return &allRule[T]{children: children}, nil
	case *inspectRule[T]:
		child, err := compileLossyRules(typed.child)
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

func compileLossyPolicyTree[T any](rule *lossyRule[T]) (Rule[T], error) {
	leaves := make([]lossyAllLeaf[T], 0, 8)
	root, err := analyzeLossyPolicy(rule, "Lossy", &leaves)
	if err != nil {
		return nil, err
	}
	for i := range leaves {
		leaves[i].compiled = leaves[i].ladder[0].compiled
	}
	if err := enforceLossyPolicyCaps(root, leaves); err != nil {
		return nil, err
	}
	assignEffectiveLossyPolicyLimits(root, leaves, nil)
	compiled, _, err := materializeLossyPolicy(root, leaves)
	return compiled, err
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

func aggregateLossyDetails(dst *inspectionDetails, value inspectionDetails) {
	dst.MemoryUsageBytes += value.MemoryUsageBytes
	dst.Items += value.Items
	dst.DistinctValues += value.DistinctValues
	dst.GranularityValue += value.GranularityValue
	dst.MemoryUsageAvailable = dst.MemoryUsageAvailable || value.MemoryUsageAvailable
	dst.ItemsAvailable = dst.ItemsAvailable || value.ItemsAvailable
	dst.DistinctValuesAvailable = dst.DistinctValuesAvailable || value.DistinctValuesAvailable
	dst.GranularityAvailable = dst.GranularityAvailable || value.GranularityAvailable
}

type lossyAllLeaf[T any] struct {
	planner  lossyAllPlanner[T]
	ladder   []lossyRepresentation[T]
	exact    uint64
	selected int
	compiled Rule[T]
}

// compileLossyAll starts with every leaf exact and applies one discrete
// downgrade at a time until the composite fits. The selector prefers the step
// that releases the most bytes, then the larger current leaf, then schema
// order. Keeping that policy isolated makes it possible to replace the score
// without changing the aggregate budget semantics.
func compileLossyAll[T any](rule *allRule[T], limit uint64) (*allRule[T], inspectionDetails, error) {
	var leaves []lossyAllLeaf[T]
	if err := collectLossyAllLeaves[T](rule, &leaves); err != nil {
		return nil, inspectionDetails{}, err
	}
	var total uint64
	for _, leaf := range leaves {
		var ok bool
		total, ok = addLossyMemory(total, leaf.exact)
		if !ok {
			return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All memory accounting overflow")
		}
	}
	for i := range leaves {
		leaves[i].compiled = leaves[i].ladder[0].compiled
	}
	if total > limit {
		var minimumTotal uint64
		for i := range leaves {
			minimum := leaves[i].ladder[len(leaves[i].ladder)-1].details.MemoryUsageBytes
			var ok bool
			minimumTotal, ok = addLossyMemory(minimumTotal, minimum)
			if !ok {
				return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All memory accounting overflow")
			}
		}
		if minimumTotal > limit {
			return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All cannot fit the memory limit")
		}
		for total > limit {
			best := selectLossyAllDowngrade(leaves)
			if best < 0 {
				return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All cannot fit the memory limit")
			}
			current := leaves[best].ladder[leaves[best].selected].details.MemoryUsageBytes
			leaves[best].selected++
			next := leaves[best].ladder[leaves[best].selected]
			total -= current - next.details.MemoryUsageBytes
			leaves[best].compiled = next.compiled
		}
	}
	index := 0
	compiled, details, err := materializeLossyAll[T](rule, leaves, &index)
	if err != nil {
		return nil, inspectionDetails{}, err
	}
	details.MemoryLimitBytes, details.MemoryLimitAvailable = limit, true
	return compiled.(*allRule[T]), details, nil
}

func selectLossyAllDowngrade[T any](leaves []lossyAllLeaf[T]) int {
	best := -1
	var bestReleased, bestCurrent uint64
	for i := range leaves {
		selected := leaves[i].selected
		if selected+1 >= len(leaves[i].ladder) {
			continue
		}
		current := leaves[i].ladder[selected].details.MemoryUsageBytes
		next := leaves[i].ladder[selected+1].details.MemoryUsageBytes
		released := current - next
		if best < 0 || released > bestReleased ||
			(released == bestReleased && current > bestCurrent) {
			best, bestReleased, bestCurrent = i, released, current
		}
	}
	return best
}

func addLossyMemory(total, usage uint64) (uint64, bool) {
	if math.MaxUint64-total < usage {
		return 0, false
	}
	return total + usage, true
}

func collectLossyAllLeaves[T any](rule Rule[T], leaves *[]lossyAllLeaf[T]) error {
	switch typed := rule.(type) {
	case *allRule[T]:
		for _, child := range typed.children {
			if err := collectLossyAllLeaves(child, leaves); err != nil {
				return err
			}
		}
		return nil
	case *inspectRule[T]:
		return collectLossyAllLeaves(typed.child, leaves)
	case *lossyRule[T]:
		return fmt.Errorf("ruleix: nested Lossy policies are not supported")
	default:
		_, ok := rule.(lossyCompiler[T])
		if !ok {
			return fmt.Errorf("ruleix: Lossy All does not support a child rule representation")
		}
		factory, ok := rule.(lossyAllCompiler[T])
		if !ok {
			return fmt.Errorf("ruleix: Lossy All child does not expose representation candidates")
		}
		planner := factory.newLossyAllPlanner()
		ladder, err := planner.representationLadder()
		if err != nil {
			return err
		}
		if len(ladder) == 0 {
			return fmt.Errorf("ruleix: Lossy All child has no viable representation")
		}
		details := ladder[0].details
		if !details.MemoryUsageAvailable {
			return fmt.Errorf("ruleix: Lossy All child has no memory accounting")
		}
		*leaves = append(*leaves, lossyAllLeaf[T]{planner: planner, ladder: ladder, exact: details.MemoryUsageBytes})
		return nil
	}
}

func materializeLossyAll[T any](
	rule Rule[T],
	leaves []lossyAllLeaf[T],
	index *int,
) (Rule[T], inspectionDetails, error) {
	switch typed := rule.(type) {
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		var aggregate inspectionDetails
		for i, child := range typed.children {
			compiled, details, err := materializeLossyAll(child, leaves, index)
			if err != nil {
				return nil, inspectionDetails{}, err
			}
			children[i] = compiled
			aggregate.MemoryUsageBytes += details.MemoryUsageBytes
			aggregate.Items += details.Items
			aggregate.DistinctValues += details.DistinctValues
			aggregate.GranularityValue += details.GranularityValue
			aggregate.MemoryUsageAvailable = aggregate.MemoryUsageAvailable || details.MemoryUsageAvailable
			aggregate.ItemsAvailable = aggregate.ItemsAvailable || details.ItemsAvailable
			aggregate.DistinctValuesAvailable = aggregate.DistinctValuesAvailable || details.DistinctValuesAvailable
			aggregate.GranularityAvailable = aggregate.GranularityAvailable || details.GranularityAvailable
		}
		return &allRule[T]{children: children}, aggregate, nil
	case *inspectRule[T]:
		child, details, err := materializeLossyAll(typed.child, leaves, index)
		if err != nil {
			return nil, inspectionDetails{}, err
		}
		return &inspectRule[T]{dst: typed.dst, child: &inspectionDetailsRule[T]{child: child, details: details}}, details, nil
	default:
		leaf := leaves[*index]
		*index++
		return leaf.compiled, inspectionDetailsOf(leaf.compiled), nil
	}
}

func minimumLossyAllLimit[T any](planner lossyAllPlanner[T], exact uint64) (uint64, Rule[T], error) {
	ladder, err := planner.representationLadder()
	if err != nil {
		return 0, nil, err
	}
	if len(ladder) == 0 || ladder[0].details.MemoryUsageBytes != exact {
		return 0, nil, fmt.Errorf("ruleix: invalid Lossy representation ladder")
	}
	minimum := ladder[len(ladder)-1]
	return minimum.details.MemoryUsageBytes, minimum.compiled, nil
}

func selectLossyRepresentation[T any](ladder []lossyRepresentation[T], limit uint64, failure string) (Rule[T], error) {
	for _, candidate := range ladder {
		if candidate.details.MemoryUsageBytes <= limit {
			return candidate.compiled, nil
		}
	}
	return nil, fmt.Errorf("%s", failure)
}

// buildLossyRepresentationLadder converts the leaf builders' natural
// coarse-to-fine order into the aggregate planner's exact-to-minimum order.
// Adjacent candidates are deduplicated only when both accounted size and the
// exposed precision metadata describe the same representation behavior.
func buildLossyRepresentationLadder[T any](exact Rule[T], coarseToFine []Rule[T]) []lossyRepresentation[T] {
	result := make([]lossyRepresentation[T], 0, len(coarseToFine)+1)
	appendCandidate := func(compiled Rule[T]) {
		details := inspectionDetailsOf(compiled)
		if len(result) != 0 {
			previous := result[len(result)-1]
			if previous.details.MemoryUsageBytes == details.MemoryUsageBytes &&
				previous.details.GranularityAvailable == details.GranularityAvailable &&
				previous.details.GranularityValue == details.GranularityValue &&
				inspectionModeOf(previous.compiled) == inspectionModeOf(compiled) &&
				inspectionStrategyOf(previous.compiled) == inspectionStrategyOf(compiled) {
				return
			}
		}
		result = append(result, lossyRepresentation[T]{compiled: compiled, details: details})
	}
	appendCandidate(exact)
	for i := len(coarseToFine) - 1; i >= 0; i-- {
		candidate := coarseToFine[i]
		if inspectionDetailsOf(candidate).MemoryUsageBytes >= result[len(result)-1].details.MemoryUsageBytes {
			continue
		}
		appendCandidate(candidate)
	}
	return result
}

func lossyinspectionDetails[T any](rule Rule[T], limit uint64) inspectionDetails {
	d := inspectionDetailsOf(rule)
	d.MemoryLimitBytes, d.MemoryLimitAvailable = limit, true
	return d
}

const lossyMaxBucketBits = 16

func bitmapBytes(bits *roaring.Bitmap) uint64 {
	if bits == nil {
		return 0
	}
	return bits.GetSerializedSizeInBytes()
}

func hashScalar(value any) (uint64, bool) {
	switch value := value.(type) {
	case bool:
		encoded := byte(0)
		if value {
			encoded = 1
		}
		return fnvHashByte(fnvHashByte(fnvOffset64, canonicalBool), encoded), true
	case string:
		hash := fnvHashUint64(fnvHashByte(fnvOffset64, canonicalString), uint64(len(value)))
		for index := range len(value) {
			hash = fnvHashByte(hash, value[index])
		}
		return hash, true
	case int:
		return avalancheTaggedEqualityHash(canonicalInt, uint64(int64(value))), true
	case int8:
		return avalancheTaggedEqualityHash(canonicalInt8, uint64(value)), true
	case int16:
		return avalancheTaggedEqualityHash(canonicalInt16, uint64(value)), true
	case int32:
		return avalancheTaggedEqualityHash(canonicalInt32, uint64(value)), true
	case int64:
		return avalancheTaggedEqualityHash(canonicalInt64, uint64(value)), true
	case uint:
		return avalancheTaggedEqualityHash(canonicalUint, uint64(value)), true
	case uint8:
		return avalancheTaggedEqualityHash(canonicalUint8, uint64(value)), true
	case uint16:
		return avalancheTaggedEqualityHash(canonicalUint16, uint64(value)), true
	case uint32:
		return avalancheTaggedEqualityHash(canonicalUint32, uint64(value)), true
	case uint64:
		return avalancheTaggedEqualityHash(canonicalUint64, value), true
	case uintptr:
		return avalancheTaggedEqualityHash(canonicalUintptr, uint64(value)), true
	case float32:
		return avalancheTaggedEqualityHash(canonicalFloat32, uint64(canonicalFloat32Bits(value))), true
	case float64:
		return avalancheTaggedEqualityHash(canonicalFloat64, canonicalFloat64Bits(value)), true
	case [16]byte:
		hash := fnvHashByte(fnvOffset64, 0xf0)
		for _, item := range value {
			hash = fnvHashByte(hash, item)
		}
		return avalancheEqualityHash(hash), true
	case [2]string:
		hash := fnvHashByte(fnvOffset64, 0xf1)
		for _, item := range value {
			hash = fnvHashUint64(hash, uint64(len(item)))
			for i := range len(item) {
				hash = fnvHashByte(hash, item[i])
			}
		}
		return avalancheEqualityHash(hash), true
	default:
		return 0, false
	}
}

func comparableValueBytes(value any) uint64 {
	if encoded, ok := canonicalScalar(nil, value); ok {
		return uint64(len(encoded))
	}
	var size func(reflect.Value) uint64
	size = func(current reflect.Value) uint64 {
		if !current.IsValid() {
			return 0
		}
		switch current.Kind() {
		case reflect.String:
			return uint64(len(current.String())) + 9
		case reflect.Bool:
			return 2
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
			reflect.Uintptr, reflect.Float32, reflect.Float64,
			reflect.Chan, reflect.Pointer, reflect.UnsafePointer:
			return 9
		case reflect.Complex64, reflect.Complex128:
			return 17
		case reflect.Array:
			total := uint64(1)
			for i := range current.Len() {
				total += size(current.Index(i))
			}
			return total
		case reflect.Struct:
			total := uint64(1)
			for i := range current.NumField() {
				total += size(current.Field(i))
			}
			return total
		case reflect.Interface:
			if current.IsNil() {
				return 1
			}
			return 9 + uint64(len(current.Elem().Type().String())) + size(current.Elem())
		default:
			return 0
		}
	}
	return size(reflect.ValueOf(value))
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func fnvHashByte(hash uint64, value byte) uint64 {
	return (hash ^ uint64(value)) * fnvPrime64
}

func fnvHashUint64(hash, value uint64) uint64 {
	for shift := 56; shift >= 0; shift -= 8 {
		hash = fnvHashByte(hash, byte(value>>shift))
	}
	return hash
}

func fnvHashTaggedUint64(tag byte, value uint64) uint64 {
	return fnvHashUint64(fnvHashByte(fnvOffset64, tag), value)
}

func avalancheTaggedEqualityHash(tag byte, value uint64) uint64 {
	return avalancheEqualityHash(fnvHashTaggedUint64(tag, value))
}

func avalancheEqualityHash(hash uint64) uint64 {
	hash ^= hash >> 30
	hash *= 0xbf58476d1ce4e5b9
	hash ^= hash >> 27
	hash *= 0x94d049bb133111eb
	return hash ^ hash>>31
}

// lossyUniversalRule is the terminal conservative representation for an
// operator whose comparator cannot be projected onto an order-preserving key.
// It returns every ID stored in that leaf, so it may lose all selectivity but
// can never remove an exact match.
type lossyUniversalRule[T any] struct {
	nodeID nodeID
	bits   *roaring.Bitmap
	name   string
}

func (r *lossyUniversalRule[T]) runtimeNodeID() nodeID                               { return r.nodeID }
func (*lossyUniversalRule[T]) rule()                                                 {}
func (r *lossyUniversalRule[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyUniversalRule[T]) validate(T) error                                      { return nil }
func (r *lossyUniversalRule[T]) insert(_ T, id uint32)                               { r.bits.Add(id) }
func (r *lossyUniversalRule[T]) cardinality(T, *bitmapPool) uint64                   { return r.bits.GetCardinality() }
func (r *lossyUniversalRule[T]) estimateCardinality(T) uint64                        { return r.bits.GetCardinality() }
func (r *lossyUniversalRule[T]) isCardinalityZero(T) bool                            { return r.bits.IsEmpty() }
func (r *lossyUniversalRule[T]) lookupPlanningBitmap(T) (*roaring.Bitmap, bool)      { return r.bits, true }
func (r *lossyUniversalRule[T]) matchesID(_ T, id uint32) bool                       { return r.bits.Contains(id) }
func (r *lossyUniversalRule[T]) search(_ T, dst *roaring.Bitmap, _ *bitmapPool)      { dst.Or(r.bits) }
func (*lossyUniversalRule[T]) exclude(T, *roaring.Bitmap, *bitmapPool)               {}
func (*lossyUniversalRule[T]) collectBuildStatistics([]nodeBuildStatistics)          {}
func (r *lossyUniversalRule[T]) inspectionStrategy() string                          { return r.name }
func (*lossyUniversalRule[T]) inspectionMode() RuleMode                              { return RuleModeLossy }
func (r *lossyUniversalRule[T]) inspectionDetails() inspectionDetails {
	return representationDetails(uint64(24)+bitmapBytes(r.bits), r.bits.GetCardinality(), 1, 1, true)
}
func (*lossyUniversalRule[T]) streamingLossyAccumulator() {}
func (r *lossyUniversalRule[T]) refreshedStreamingDetails(inspectionDetails) inspectionDetails {
	return r.inspectionDetails()
}
func (r *lossyUniversalRule[T]) prepareSearch()                         { prepareBitmapForSearch(r.bits) }
func (r *lossyUniversalRule[T]) internBitmaps(interner *bitmapInterner) { interner.intern(&r.bits) }

type fixedLossyAllPlanner[T any] struct{ ladder []lossyRepresentation[T] }

func (p fixedLossyAllPlanner[T]) compile(limit uint64) (Rule[T], error) {
	return selectLossyRepresentation(p.ladder, limit, "ruleix: Lossy rule cannot fit the memory limit")
}

func (p fixedLossyAllPlanner[T]) representationLadder() ([]lossyRepresentation[T], error) {
	return p.ladder, nil
}

func newUniversalLossyPlanner[T any](exact Rule[T], node nodeID, name string, all *roaring.Bitmap) lossyAllPlanner[T] {
	usage := uint64(24) + bitmapBytes(all)
	fallback := Rule[T](&inspectionDetailsRule[T]{
		child:   &lossyUniversalRule[T]{nodeID: node, bits: all, name: name},
		details: representationDetails(usage, all.GetCardinality(), 1, 1, true),
	})
	return fixedLossyAllPlanner[T]{ladder: buildLossyRepresentationLadder(exact, []Rule[T]{fallback})}
}

type lossyEqualityRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	bucketCount    uint64
	codec          equalityCodec[V]
	buckets        map[uint64]lossyEqualityPosting
}

type lossyEqualityPosting struct {
	bits   *roaring.Bitmap
	source physicalSourceID
	class  uint32
}

func (r *lossyEqualityRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func (r *lossyEqualityRule[T, V]) streamingUniversal() (nodeID, *roaring.Bitmap, string) {
	bits := r.wildcard.Clone()
	for _, posting := range r.buckets {
		bits.Or(posting.bits)
	}
	return r.nodeID, bits, "lossy-streaming-universal"
}

func (*lossyEqualityRule[T, V]) streamingLossyAccumulator() {}
func (r *lossyEqualityRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard)
	items := r.wildcard.GetCardinality()
	for _, posting := range r.buckets {
		usage += 24 + bitmapBytes(posting.bits)
		items += posting.bits.GetCardinality()
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.buckets)), true
	return details
}
func (r *lossyEqualityRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && r.bucketCount > 1 {
		r.rebucket(max(r.bucketCount/2, 1))
	}
}

// rebucket maps each old hash interval to every overlapping interval in a
// coarser multiply-high grid. The original hash values are not retained.
func (r *lossyEqualityRule[T, V]) rebucket(count uint64) {
	count = min(max(count, 1), r.bucketCount)
	if count == r.bucketCount {
		return
	}
	next := make(map[uint64]lossyEqualityPosting, min(len(r.buckets), int(count)))
	for old, posting := range r.buckets {
		first := old * count / r.bucketCount
		last := ((old+1)*count - 1) / r.bucketCount
		for bucket := first; bucket <= last; bucket++ {
			merged := next[bucket]
			if merged.bits == nil {
				merged.bits = roaring.New()
			}
			merged.bits.Or(posting.bits)
			next[bucket] = merged
		}
	}
	r.bucketCount, r.buckets = count, next
}

func (r *lossyEqualityRule[T, V]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
	// A wildcard requires a union with the concrete bucket, so it cannot expose
	// one of its owned bitmaps as the complete child result.
	if !r.wildcard.IsEmpty() {
		return nil, false
	}
	value, ok := r.get(v)
	if !ok {
		return r.wildcard, true
	}
	hash := r.codec.hash(value)
	bits := r.buckets[reduceEqualityHash(hash, r.bucketCount)].bits
	if bits == nil {
		return r.wildcard, true
	}
	return bits, true
}

func (*lossyEqualityRule[T, V]) rule()                                                 {}
func (r *lossyEqualityRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyEqualityRule[T, V]) validate(T) error                                      { return nil }
func (r *lossyEqualityRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	bucket := reduceEqualityHash(r.codec.hash(value), r.bucketCount)
	posting := r.buckets[bucket]
	if posting.bits == nil {
		posting.bits = roaring.New()
	}
	posting.bits.Add(id)
	r.buckets[bucket] = posting
}
func (r *lossyEqualityRule[T, V]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	value := getOptional(r.get, v)
	if pool.local != nil {
		cache := equalityCache[V](pool, r.nodeID)
		if bits, found := comparableValueCacheLookup(cache, value); found {
			dst.Or(bits)
			return
		}
		if comparableValueCacheAdmit(cache, value) {
			bits := cache.replace(value, pool)
			r.addMatches(value, bits)
			dst.Or(bits)
			cache.commit(bits, pool)
			return
		}
	}
	r.addMatches(value, dst)
}
func (r *lossyEqualityRule[T, V]) addMatches(value optionalValue[V], dst *roaring.Bitmap) {
	dst.Or(r.wildcard)
	if !value.ok {
		return
	}
	hash := r.codec.hash(value.value)
	if bits := r.buckets[reduceEqualityHash(hash, r.bucketCount)].bits; bits != nil {
		dst.Or(bits)
	}
}
func (r *lossyEqualityRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	hash := r.codec.hash(value)
	if bits := r.buckets[reduceEqualityHash(hash, r.bucketCount)].bits; bits != nil {
		n += bits.GetCardinality()
	}
	return n
}
func (r *lossyEqualityRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *lossyEqualityRule[T, V]) lookupEqualityClass(v T) uint32 {
	value, ok := r.get(v)
	if !ok {
		return r.wildcardClass
	}
	hash := r.codec.hash(value)
	return r.buckets[reduceEqualityHash(hash, r.bucketCount)].class
}
func (r *lossyEqualityRule[T, V]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	bits, found := r.lookupCachedBitmap(v, pool)
	if !found {
		return 0, false
	}
	return bits.GetCardinality(), true
}
func (r *lossyEqualityRule[T, V]) lookupCachedBitmap(v T, pool *bitmapPool) (*roaring.Bitmap, bool) {
	return lookupEqualityCachedBitmap(pool, r.nodeID, getOptional(r.get, v))
}
func (r *lossyEqualityRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyEqualityRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	if !ok {
		return false
	}
	hash := r.codec.hash(value)
	bits := r.buckets[reduceEqualityHash(hash, r.bucketCount)].bits
	return bits != nil && bits.Contains(id)
}
func (*lossyEqualityRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }

// reduceEqualityHash maps the complete codec hash onto any immutable bucket
// count without modulo bias. bits.Mul64 returns the high half of hash*count.
func reduceEqualityHash(hash, bucketCount uint64) uint64 {
	high, _ := bits.Mul64(hash, bucketCount)
	return high
}
func (r *lossyEqualityRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*lossyEqualityRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyEqualityRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyEqualityRule[T, V]) inspectionStrategy() string                   { return "lossy-grouped-hash" }
func (*lossyEqualityRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyEqualityRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, posting := range r.buckets {
		prepareBitmapForSearch(posting.bits)
	}
}
func (r *lossyEqualityRule[T, V]) internBitmaps(i *bitmapInterner) {
	r.wildcardSource = i.internSource(&r.wildcard)
	for k, posting := range r.buckets {
		posting.source = i.internSource(&posting.bits)
		r.buckets[k] = posting
	}
}
func (r *lossyEqualityRule[T, V]) equalitySourceCount() int { return 1 + len(r.buckets) }
func (r *lossyEqualityRule[T, V]) visitEqualitySources(visit func(equalitySourcePair)) {
	visit(equalitySourcePair{wildcard: r.wildcardSource})
	for _, posting := range r.buckets {
		if posting.source == 0 {
			continue
		}
		visit(equalitySourcePair{wildcard: r.wildcardSource, posting: posting.source})
	}
}
func (r *lossyEqualityRule[T, V]) assignEqualityClasses(classes map[equalitySourcePair]uint32) {
	r.wildcardClass = classes[equalitySourcePair{wildcard: r.wildcardSource}]
	for key, posting := range r.buckets {
		if posting.source != 0 {
			posting.class = classes[equalitySourcePair{wildcard: r.wildcardSource, posting: posting.source}]
			r.buckets[key] = posting
		}
	}
}

type lossyOrderedRule[T any, V any] struct {
	nodeID          nodeID
	get             Getter[T, V]
	dir             direction
	inclusive       bool
	wildcard        *roaring.Bitmap
	min, max, width uint64
	buckets         []*roaring.Bitmap
}

type lossyComparedOrderedRule[T any, V any] struct {
	nodeID     nodeID
	get        Getter[T, V]
	compare    Compare[V]
	dir        direction
	inclusive  bool
	wildcard   *roaring.Bitmap
	minimum    V
	maximum    V
	capacity   int
	boundaries []V
	buckets    []*roaring.Bitmap
}

type lossyComparedBuckets[V any] struct {
	compare    Compare[V]
	minimum    V
	maximum    V
	capacity   int
	boundaries []V
	buckets    []*roaring.Bitmap
}

func (r *lossyComparedBuckets[V]) insert(value V, id uint32) {
	if len(r.buckets) == 0 {
		r.minimum, r.maximum = value, value
		r.capacity = max(r.capacity, 1)
		r.boundaries = []V{value}
		r.buckets = []*roaring.Bitmap{roaring.BitmapOf(id)}
		return
	}
	if r.compare(value, r.minimum) < 0 {
		r.minimum = value
		r.boundaries = append([]V{value}, r.boundaries...)
		r.buckets = append([]*roaring.Bitmap{roaring.BitmapOf(id)}, r.buckets...)
		r.fitCapacity()
		return
	}
	if r.compare(value, r.maximum) > 0 {
		r.maximum = value
		r.boundaries = append(r.boundaries, value)
		r.buckets = append(r.buckets, roaring.BitmapOf(id))
		r.fitCapacity()
		return
	}
	bucket := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	r.buckets[bucket].Add(id)
}

func (r *lossyComparedBuckets[V]) fitCapacity() {
	for len(r.buckets) > max(r.capacity, 1) {
		r.coarsenOne()
	}
}

// coarsenOne merges the least-populated adjacent pair. Comparator-backed
// values have no arithmetic distance, so cardinality is the only stable
// build-time signal available without retaining the original exact values.
func (r *lossyComparedBuckets[V]) coarsenOne() {
	if len(r.buckets) <= 1 {
		return
	}
	merge := 0
	best := r.buckets[0].GetCardinality() + r.buckets[1].GetCardinality()
	for i := 1; i+1 < len(r.buckets); i++ {
		cardinality := r.buckets[i].GetCardinality() + r.buckets[i+1].GetCardinality()
		if cardinality < best {
			merge, best = i, cardinality
		}
	}
	r.buckets[merge].Or(r.buckets[merge+1])
	r.boundaries[merge] = r.boundaries[merge+1]
	copy(r.buckets[merge+1:], r.buckets[merge+2:])
	copy(r.boundaries[merge+1:], r.boundaries[merge+2:])
	r.buckets = r.buckets[:len(r.buckets)-1]
	r.boundaries = r.boundaries[:len(r.boundaries)-1]
}

func buildLossyComparedBuckets[V any](index *orderedIndex[V], wanted int) lossyComparedBuckets[V] {
	result := lossyComparedBuckets[V]{compare: index.compare, capacity: max(wanted, 1)}
	values := make([]*orderedItem[V], 0, index.buildStatistics().uniqueValues)
	for block := range index.blocks {
		values = append(values, index.blocks[block].items...)
	}
	if len(values) == 0 {
		return result
	}
	result.minimum, result.maximum = values[0].value, values[len(values)-1].value
	count := min(max(wanted, 1), len(values))
	width := (len(values) + count - 1) / count
	result.boundaries = make([]V, 0, count)
	result.buckets = make([]*roaring.Bitmap, 0, count)
	for first := 0; first < len(values); first += width {
		last := min(first+width, len(values))
		bits := roaring.New()
		for _, value := range values[first:last] {
			bits.Or(value.bits)
		}
		result.boundaries = append(result.boundaries, values[last-1].value)
		result.buckets = append(result.buckets, bits)
	}
	return result
}

func (r *lossyComparedBuckets[V]) matchingRange(value V, dir direction, inclusive bool) (int, int, bool) {
	if len(r.buckets) == 0 {
		return 0, 0, false
	}
	last := len(r.buckets) - 1
	if dir == greaterThan {
		minimum := r.compare(value, r.minimum)
		if minimum < 0 || (!inclusive && minimum == 0) {
			return 0, 0, false
		}
		if r.compare(value, r.maximum) >= 0 {
			return 0, last, true
		}
		end := sort.Search(len(r.boundaries), func(i int) bool {
			return r.compare(r.boundaries[i], value) >= 0
		})
		return 0, end, true
	}
	maximum := r.compare(value, r.maximum)
	if maximum > 0 || (!inclusive && maximum == 0) {
		return 0, 0, false
	}
	if r.compare(value, r.minimum) <= 0 {
		return 0, last, true
	}
	first := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	return first, last, true
}

func (r *lossyComparedBuckets[V]) exactRange(value V) (int, int, bool) {
	if len(r.buckets) == 0 || r.compare(value, r.minimum) < 0 || r.compare(value, r.maximum) > 0 {
		return 0, 0, false
	}
	bucket := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	return bucket, bucket, true
}

func (r *lossyComparedBuckets[V]) addRange(first, last int, dst *roaring.Bitmap) {
	for i := first; i <= last; i++ {
		dst.Or(r.buckets[i])
	}
}

func (r *lossyComparedBuckets[V]) rangeCardinality(first, last int) uint64 {
	var result uint64
	for i := first; i <= last; i++ {
		result += r.buckets[i].GetCardinality()
	}
	return result
}

func (r *lossyComparedBuckets[V]) rangeContains(first, last int, id uint32) bool {
	for i := first; i <= last; i++ {
		if r.buckets[i].Contains(id) {
			return true
		}
	}
	return false
}

func (r *lossyComparedBuckets[V]) memoryUsage() uint64 {
	usage := uint64(16 * len(r.buckets))
	for i, bits := range r.buckets {
		usage += comparableValueBytes(any(r.boundaries[i])) + bitmapBytes(bits)
	}
	return usage
}

func (r *lossyComparedBuckets[V]) prepareSearch() {
	for _, bits := range r.buckets {
		prepareBitmapForSearch(bits)
	}
}

func (r *lossyComparedOrderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }
func (*lossyComparedOrderedRule[T, V]) rule()                   {}
func (r *lossyComparedOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] {
	return r
}
func (*lossyComparedOrderedRule[T, V]) validate(T) error           { return nil }
func (*lossyComparedOrderedRule[T, V]) streamingLossyAccumulator() {}
func (r *lossyComparedOrderedRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(40) + bitmapBytes(r.wildcard) + uint64(len(r.buckets))*16
	items := r.wildcard.GetCardinality()
	for i, bucket := range r.buckets {
		usage += bitmapBytes(bucket) + comparableValueBytes(any(r.boundaries[i]))
		items += bucket.GetCardinality()
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.buckets)), true
	return details
}
func (r *lossyComparedOrderedRule[T, V]) fitStreamingLimit(limit uint64) {
	buckets := lossyComparedBuckets[V]{
		compare: r.compare, minimum: r.minimum, maximum: r.maximum,
		capacity: r.capacity, boundaries: r.boundaries, buckets: r.buckets,
	}
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && len(buckets.buckets) > 1 {
		buckets.coarsenOne()
		r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
	}
	r.capacity = min(max(r.capacity, 1), len(buckets.buckets))
	r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
}
func (r *lossyComparedOrderedRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	buckets := lossyComparedBuckets[V]{
		compare: r.compare, minimum: r.minimum, maximum: r.maximum,
		capacity: r.capacity, boundaries: r.boundaries, buckets: r.buckets,
	}
	buckets.insert(value, id)
	r.minimum, r.maximum = buckets.minimum, buckets.maximum
	r.capacity = buckets.capacity
	r.boundaries, r.buckets = buckets.boundaries, buckets.buckets
}
func (r *lossyComparedOrderedRule[T, V]) matchingBucketRange(v T) (int, int, bool) {
	value, ok := r.get(v)
	if !ok || len(r.buckets) == 0 {
		return 0, 0, false
	}
	last := len(r.buckets) - 1
	if r.dir == greaterThan {
		minimum := r.compare(value, r.minimum)
		if minimum < 0 || (!r.inclusive && minimum == 0) {
			return 0, 0, false
		}
		if r.compare(value, r.maximum) >= 0 {
			return 0, last, true
		}
		end := sort.Search(len(r.boundaries), func(i int) bool {
			return r.compare(r.boundaries[i], value) >= 0
		})
		return 0, end, true
	}
	maximum := r.compare(value, r.maximum)
	if maximum > 0 || (!r.inclusive && maximum == 0) {
		return 0, 0, false
	}
	if r.compare(value, r.minimum) <= 0 {
		return 0, last, true
	}
	first := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	return first, last, true
}
func (r *lossyComparedOrderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return
	}
	for i := first; i <= last; i++ {
		dst.Or(r.buckets[i])
	}
}
func (r *lossyComparedOrderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return n
	}
	for i := first; i <= last; i++ {
		n += r.buckets[i].GetCardinality()
	}
	return n
}
func (r *lossyComparedOrderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (r *lossyComparedOrderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyComparedOrderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return false
	}
	for i := first; i <= last; i++ {
		if r.buckets[i].Contains(id) {
			return true
		}
	}
	return false
}
func (*lossyComparedOrderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyComparedOrderedRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyComparedOrderedRule[T, V]) inspectionStrategy() string                   { return "lossy-ordered" }
func (*lossyComparedOrderedRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyComparedOrderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, bits := range r.buckets {
		prepareBitmapForSearch(bits)
	}
}

func (r *lossyOrderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func lossyOrderedBucket(key, minimum, width, count uint64) uint64 {
	bucket := (key - minimum) / width
	if bucket >= count {
		return count - 1
	}
	return bucket
}

func lossyOrderedGrid(minimum, maximum uint64, wanted int) (uint64, int) {
	count := uint64(max(wanted, 1))
	span := maximum - minimum
	width := span/count + 1
	if width == 0 {
		width = math.MaxUint64
	}
	used := min(span/width+1, count)
	return width, int(used)
}

// regrid rebuilds a coarser numeric grid using only the interval represented
// by each old bucket. An old posting is copied to every overlapping new bucket,
// preserving the no-false-negative contract without retaining exact values.
func (r *lossyOrderedRule[T, V]) regrid(minimum, maximum uint64, wanted int) {
	width, count := lossyOrderedGrid(minimum, maximum, wanted)
	next := make([]*roaring.Bitmap, count)
	oldMinimum, oldMaximum, oldWidth := r.min, r.max, r.width
	for i, bits := range r.buckets {
		if bits == nil {
			continue
		}
		start := oldMinimum
		if i != 0 {
			if oldWidth != 0 && uint64(i) > math.MaxUint64/oldWidth {
				start = math.MaxUint64
			} else {
				offset := uint64(i) * oldWidth
				if offset > math.MaxUint64-oldMinimum {
					start = math.MaxUint64
				} else {
					start += offset
				}
			}
		}
		end := oldMaximum
		if oldWidth != math.MaxUint64 && oldWidth-1 <= math.MaxUint64-start {
			end = min(end, start+oldWidth-1)
		}
		first := lossyOrderedBucket(start, minimum, width, uint64(count))
		last := lossyOrderedBucket(end, minimum, width, uint64(count))
		for bucket := first; bucket <= last; bucket++ {
			if next[bucket] == nil {
				next[bucket] = roaring.New()
			}
			next[bucket].Or(bits)
		}
	}
	r.min, r.max, r.width, r.buckets = minimum, maximum, width, next
}

func (*lossyOrderedRule[T, V]) rule()                                                 {}
func (r *lossyOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyOrderedRule[T, V]) validate(T) error                                      { return nil }
func (*lossyOrderedRule[T, V]) streamingLossyAccumulator()                            {}
func (r *lossyOrderedRule[T, V]) refreshedStreamingDetails(details inspectionDetails) inspectionDetails {
	usage := uint64(32) + bitmapBytes(r.wildcard) + uint64(len(r.buckets))*8
	items := r.wildcard.GetCardinality()
	for _, bucket := range r.buckets {
		if bucket != nil {
			usage += bitmapBytes(bucket)
			items += bucket.GetCardinality()
		}
	}
	details.MemoryUsageBytes, details.MemoryUsageAvailable = usage, true
	details.Items, details.ItemsAvailable = items, true
	details.GranularityValue, details.GranularityAvailable = uint64(len(r.buckets)), true
	return details
}
func (r *lossyOrderedRule[T, V]) fitStreamingLimit(limit uint64) {
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit && len(r.buckets) > 1 {
		r.regrid(r.min, r.max, len(r.buckets)-1)
	}
}
func (r *lossyOrderedRule[T, V]) insert(v T, id uint32) {
	value, ok := r.get(v)
	if !ok {
		r.wildcard.Add(id)
		return
	}
	key, ok := orderedScalarKey(any(value))
	if !ok {
		return
	}
	if len(r.buckets) == 0 {
		r.min, r.max, r.width = key, key, math.MaxUint64
		r.buckets = []*roaring.Bitmap{roaring.BitmapOf(id)}
		return
	}
	if key < r.min || key > r.max {
		r.regrid(min(r.min, key), max(r.max, key), len(r.buckets))
	}
	bucket := lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
	if r.buckets[bucket] == nil {
		r.buckets[bucket] = roaring.New()
	}
	r.buckets[bucket].Add(id)
}
func (r *lossyOrderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return
	}
	for n := first; n <= last; n++ {
		if b := r.buckets[n]; b != nil {
			dst.Or(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) matchingBucketRange(v T) (uint64, uint64, bool) {
	value, ok := r.get(v)
	if !ok || len(r.buckets) == 0 {
		return 0, 0, false
	}
	key, ok := orderedScalarKey(any(value))
	if !ok {
		return 0, 0, false
	}
	last := uint64(len(r.buckets) - 1)
	if r.dir == greaterThan {
		if key < r.min || (!r.inclusive && key == r.min) {
			return 0, 0, false
		}
		if key < r.max {
			last = lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
		}
		return 0, last, true
	}
	if key > r.max || (!r.inclusive && key == r.max) {
		return 0, 0, false
	}
	first := uint64(0)
	if key > r.min {
		first = lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
	}
	return first, last, true
}
func (r *lossyOrderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return n
	}
	for bucket := first; bucket <= last; bucket++ {
		if bits := r.buckets[bucket]; bits != nil {
			n += bits.GetCardinality()
		}
	}
	return n
}
func (r *lossyOrderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyOrderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return false
	}
	for bucket := first; bucket <= last; bucket++ {
		if bits := r.buckets[bucket]; bits != nil && bits.Contains(id) {
			return true
		}
	}
	return false
}
func (r *lossyOrderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*lossyOrderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyOrderedRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyOrderedRule[T, V]) inspectionStrategy() string                   { return "lossy-ordered-buckets" }
func (*lossyOrderedRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyOrderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, b := range r.buckets {
		if b != nil {
			prepareBitmapForSearch(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) internBitmaps(i *bitmapInterner) {
	i.intern(&r.wildcard)
	for n, b := range r.buckets {
		if b != nil {
			i.intern(&b)
			r.buckets[n] = b
		}
	}
}

func addAccounting(total, add uint64) (uint64, bool) {
	if math.MaxUint64-total < add {
		return 0, false
	}
	return total + add, true
}
