package ruleix

import (
	"fmt"
	"hash/maphash"
	"math"

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
	compiled, _, err := materializeLossyPolicy(root, leaves)
	return compiled, err
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
		*leaves = append(*leaves, lossyAllLeaf[T]{planner: planner, ladder: ladder, exact: ladder[0].details.MemoryUsageBytes})
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

func materializeLossyPolicy[T any](plan *lossyPolicyPlan[T], leaves []lossyAllLeaf[T]) (Rule[T], inspectionDetails, error) {
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
		return &inspectRule[T]{dst: plan.original.(*inspectRule[T]).dst, child: &inspectionDetailsRule[T]{child: child, details: details}}, details, nil
	case lossyPlanPolicy:
		child, details, err := materializeLossyPolicy(plan.children[0], leaves)
		if err != nil {
			return nil, inspectionDetails{}, err
		}
		details.MemoryLimitBytes, details.MemoryLimitAvailable = plan.limit, true
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
		return fnvHashTaggedUint64(canonicalInt, uint64(int64(value))), true
	case int8:
		return fnvHashTaggedUint64(canonicalInt8, uint64(value)), true
	case int16:
		return fnvHashTaggedUint64(canonicalInt16, uint64(value)), true
	case int32:
		return fnvHashTaggedUint64(canonicalInt32, uint64(value)), true
	case int64:
		return fnvHashTaggedUint64(canonicalInt64, uint64(value)), true
	case uint:
		return fnvHashTaggedUint64(canonicalUint, uint64(value)), true
	case uint8:
		return fnvHashTaggedUint64(canonicalUint8, uint64(value)), true
	case uint16:
		return fnvHashTaggedUint64(canonicalUint16, uint64(value)), true
	case uint32:
		return fnvHashTaggedUint64(canonicalUint32, uint64(value)), true
	case uint64:
		return fnvHashTaggedUint64(canonicalUint64, value), true
	case uintptr:
		return fnvHashTaggedUint64(canonicalUintptr, uint64(value)), true
	case float32:
		return fnvHashTaggedUint64(canonicalFloat32, uint64(canonicalFloat32Bits(value))), true
	case float64:
		return fnvHashTaggedUint64(canonicalFloat64, canonicalFloat64Bits(value)), true
	default:
		return 0, false
	}
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

type lossyEqualityRule[T any, V comparable] struct {
	nodeID         nodeID
	get            Getter[T, V]
	wildcard       *roaring.Bitmap
	wildcardSource physicalSourceID
	wildcardClass  uint32
	shift          uint
	hasher         scalarHasher[V]
	buckets        map[uint64]lossyEqualityPosting
}

type lossyEqualityPosting struct {
	bits   *roaring.Bitmap
	source physicalSourceID
	class  uint32
}

func (r *lossyEqualityRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

type scalarHasher[V comparable] struct {
	stringSeed maphash.Seed
	stringHash bool
}

func newScalarHasher[V comparable]() scalarHasher[V] {
	var zero V
	_, stringHash := any(zero).(string)
	if !stringHash {
		return scalarHasher[V]{}
	}
	return scalarHasher[V]{stringSeed: maphash.MakeSeed(), stringHash: true}
}

func (h scalarHasher[V]) hash(value V) (uint64, bool) {
	if h.stringHash {
		return maphash.String(h.stringSeed, any(value).(string)), true
	}
	return hashScalar(any(value))
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
	hash, ok := r.hasher.hash(value)
	if !ok {
		return r.wildcard, true
	}
	bits := r.buckets[hash>>r.shift].bits
	if bits == nil {
		return r.wildcard, true
	}
	return bits, true
}

func (*lossyEqualityRule[T, V]) rule()                                                 {}
func (r *lossyEqualityRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyEqualityRule[T, V]) validate(T) error                                      { return nil }
func (*lossyEqualityRule[T, V]) insert(T, uint32)                                      {}
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
	hash, ok := r.hasher.hash(value.value)
	if !ok {
		return
	}
	if bits := r.buckets[hash>>r.shift].bits; bits != nil {
		dst.Or(bits)
	}
}
func (r *lossyEqualityRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	hash, ok := r.hasher.hash(value)
	if !ok {
		return n
	}
	if bits := r.buckets[hash>>r.shift].bits; bits != nil {
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
	hash, ok := r.hasher.hash(value)
	if !ok {
		return r.wildcardClass
	}
	return r.buckets[hash>>r.shift].class
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
	hash, ok := r.hasher.hash(value)
	if !ok {
		return false
	}
	bits := r.buckets[hash>>r.shift].bits
	return bits != nil && bits.Contains(id)
}
func (*lossyEqualityRule[T, V]) directIDWork() uint64 { return allEqualityDirectIDWork }
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

func (r *lossyOrderedRule[T, V]) runtimeNodeID() nodeID { return r.nodeID }

func lossyOrderedBucket(key, minimum, width, count uint64) uint64 {
	bucket := (key - minimum) / width
	if bucket >= count {
		return count - 1
	}
	return bucket
}

func (*lossyOrderedRule[T, V]) rule()                                                 {}
func (r *lossyOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyOrderedRule[T, V]) validate(T) error                                      { return nil }
func (*lossyOrderedRule[T, V]) insert(T, uint32)                                      {}
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
