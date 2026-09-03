package ruleix

import (
	"fmt"
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

const lossyBuildPressureInterval = 4096

func addLossyMemory(total, usage uint64) (uint64, bool) {
	if math.MaxUint64-total < usage {
		return 0, false
	}
	return total + usage, true
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
		usage, available = currentLossyUsage(typed.child)
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

func currentLossyUsage[T any](rule Rule[T]) (uint64, bool) {
	switch typed := rule.(type) {
	case *allRule[T]:
		var total uint64
		for _, child := range typed.children {
			usage, ok := currentLossyUsage(child)
			if !ok || math.MaxUint64-total < usage {
				return 0, false
			}
			total += usage
		}
		return total, true
	case *inspectRule[T]:
		return currentLossyUsage(typed.child)
	case *lossyRule[T]:
		return currentLossyUsage(typed.child)
	default:
		provider, ok := any(rule).(streamingDetailsProvider)
		if !ok {
			return 0, false
		}
		details := provider.refreshedStreamingDetails(inspectionDetails{})
		return details.MemoryUsageBytes, details.MemoryUsageAvailable
	}
}

// streamingLossyAccumulator marks a compiled lossy search representation that
// can also accept the remainder of the one-pass build directly. The marker is
// build-only: the published search method is unchanged.
type streamingLossyAccumulator interface{ streamingLossyAccumulator() }

type streamingDetailsProvider interface {
	refreshedStreamingDetails(inspectionDetails) inspectionDetails
}

func (r *streamingAdaptiveLeaf[T]) nextStreamingUsage() (uint64, bool) {
	if r.nextUsagePrepared {
		return r.nextUsage, r.nextUsageOK
	}
	r.nextUsagePrepared = true
	if !r.approximate {
		if factory, ok := r.child.(streamingFirstGenerationFactory[T]); ok {
			usage, next, available := factory.prepareStreamingFirstGeneration()
			if available {
				r.nextUsage, r.nextUsageOK, r.nextApply = usage, true, func() {
					r.child, r.approximate = next, true
				}
				return usage, true
			}
		}
	}
	if preparer, ok := streamingRuleNextPreparer(r.child); ok {
		r.nextUsage, r.nextApply, r.nextUsageOK = preparer.prepareStreamingNext()
	} else {
		r.nextUsage, r.nextUsageOK = nextStreamingRuleUsage(r.child)
	}
	return r.nextUsage, r.nextUsageOK
}

func (r *streamingAdaptiveLeaf[T]) fitStreamingLimit(limit uint64) {
	r.nextUsagePrepared, r.nextApply = false, nil
	for r.refreshedStreamingDetails(inspectionDetails{}).MemoryUsageBytes > limit {
		if _, ok := r.nextStreamingUsage(); !ok {
			return
		}
		r.fitStreamingNext()
	}
}

func (r *streamingAdaptiveLeaf[T]) fitStreamingNext() {
	if r.nextUsagePrepared && r.nextUsageOK && r.nextApply != nil {
		apply := r.nextApply
		r.nextUsagePrepared, r.nextApply = false, nil
		apply()
		r.approximate = true
		return
	}
	r.nextUsagePrepared = false
	fitStreamingRuleNext(r.child)
	r.approximate = true
}

func streamingRuleNextPreparer[T any](rule Rule[T]) (streamingNextPreparer, bool) {
	switch typed := rule.(type) {
	case *inspectionDetailsRule[T]:
		return streamingRuleNextPreparer(typed.child)
	case *streamingAdaptiveLeaf[T]:
		return streamingRuleNextPreparer(typed.child)
	default:
		preparer, ok := any(rule).(streamingNextPreparer)
		return preparer, ok
	}
}

func refreshedStreamingRuleDetails[T any](rule Rule[T], fallback inspectionDetails) inspectionDetails {
	switch typed := rule.(type) {
	case *inspectionDetailsRule[T]:
		return refreshedStreamingRuleDetails(typed.child, typed.details)
	case *streamingAdaptiveLeaf[T]:
		return typed.refreshedStreamingDetails(fallback)
	default:
		if provider, ok := any(rule).(streamingDetailsProvider); ok {
			return provider.refreshedStreamingDetails(fallback)
		}
		if details := inspectionDetailsOf(rule); details.MemoryUsageAvailable {
			return details
		}
		return fallback
	}
}

func streamingRuleCanFit[T any](rule Rule[T]) bool {
	switch typed := rule.(type) {
	case *inspectionDetailsRule[T]:
		return streamingRuleCanFit(typed.child)
	case *streamingAdaptiveLeaf[T]:
		return typed.canFitStreaming()
	default:
		available, ok := any(rule).(streamingFitAvailability)
		return ok && available.canFitStreaming()
	}
}

func nextStreamingRuleUsage[T any](rule Rule[T]) (uint64, bool) {
	switch typed := rule.(type) {
	case *inspectionDetailsRule[T]:
		return nextStreamingRuleUsage(typed.child)
	case *streamingAdaptiveLeaf[T]:
		return typed.nextStreamingUsage()
	default:
		provider, ok := any(rule).(streamingNextUsageProvider)
		if !ok {
			return 0, false
		}
		return provider.nextStreamingUsage()
	}
}

func fitStreamingRuleNext[T any](rule Rule[T]) {
	switch typed := rule.(type) {
	case *inspectionDetailsRule[T]:
		fitStreamingRuleNext(typed.child)
	case *streamingAdaptiveLeaf[T]:
		typed.fitStreamingNext()
	default:
		if stepper, ok := any(rule).(streamingStepFitter); ok {
			stepper.fitStreamingNext()
		}
	}
}

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
	fitter   streamingLimitFitter
	stepper  streamingStepFitter
	usage    uint64
	released uint64
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
			((details.GranularityAvailable && details.GranularityValue > 1) || streamingRuleCanFit(rule)) {
			next, available := nextStreamingRuleUsage(rule)
			if !available {
				return details.MemoryUsageBytes
			}
			released := uint64(0)
			if next < details.MemoryUsageBytes {
				released = details.MemoryUsageBytes - next
			}
			stepper, _ := any(rule).(streamingStepFitter)
			*candidates = append(*candidates, streamingFitCandidate{
				fitter: fitter, stepper: stepper, usage: details.MemoryUsageBytes,
				released: released,
			})
		}
		return details.MemoryUsageBytes
	}
}

// fitStreamingAggregate advances one build-time precision level at a time until
// the complete published subtree satisfies its aggregate retained-memory
// limit. It avoids the old emergency path that collapsed every lossy child to
// its minimum representation at once.
func fitStreamingAggregate[T any](rule Rule[T], limit uint64) {
	fitStreamingAggregateTo(rule, limit, false)
}

// fitStreamingAggregateHard is used only by final publication. Unlike a
// pressure checkpoint, it may ask leaves already at the end of their ordinary
// levels for their conservative terminal representation. The caller still
// verifies the resulting retained total and reports an error when it cannot
// fit.
func fitStreamingAggregateHard[T any](rule Rule[T], limit uint64) {
	fitStreamingAggregateTo(rule, limit, true)
}

func fitStreamingAggregateTo[T any](rule Rule[T], limit uint64, allowTerminal bool) {
	for {
		var candidates []streamingFitCandidate
		usage := collectStreamingFitCandidates(rule, &candidates)
		if usage <= limit {
			return
		}
		if len(candidates) == 0 {
			if allowTerminal {
				fitStreamingRule(rule, 0)
				var refreshed []streamingFitCandidate
				if collectStreamingFitCandidates(rule, &refreshed) < usage {
					continue
				}
			}
			return
		}
		// Rank atomic transitions by the smallest release so the planner consumes
		// only as much precision as the current deficit needs. A zero-release step
		// remains eligible because it can expose a useful coarser successor.
		selected := 0
		for i := 1; i < len(candidates); i++ {
			if candidates[i].released < candidates[selected].released ||
				(candidates[i].released == candidates[selected].released &&
					candidates[i].usage > candidates[selected].usage) {
				selected = i
			}
		}
		candidate := candidates[selected]
		if candidate.stepper != nil {
			candidate.stepper.fitStreamingNext()
		} else {
			candidate.fitter.fitStreamingLimit(candidate.usage - 1)
		}
	}
}

// lossyStreamingBuildPressure reads live accounted sizes after the first
// compilation. Policy wrappers retain their effective hard limits, while the
// adaptive leaves beneath them retain both exact and lossy downgrade choices.
func lossyStreamingBuildPressure[T any](rule Rule[T]) (usage, target uint64, available bool) {
	switch typed := rule.(type) {
	case *allRule[T]:
		for _, child := range typed.children {
			childUsage, childTarget, ok := lossyStreamingBuildPressure(child)
			if !ok {
				continue
			}
			if childUsage > childTarget {
				return childUsage, childTarget, true
			}
			if !available || childTarget < target {
				usage, target, available = childUsage, childTarget, true
			}
		}
	case *inspectRule[T]:
		return lossyStreamingBuildPressure(typed.child)
	case *inspectionDetailsRule[T]:
		if typed.details.MemoryLimitAvailable {
			var candidates []streamingFitCandidate
			return collectStreamingFitCandidates(typed.child, &candidates),
				lossyBuildTarget(typed.details.MemoryLimitBytes), true
		}
		return lossyStreamingBuildPressure(typed.child)
	}
	return usage, target, available
}

// fitStreamingPolicies handles a pressure checkpoint, not final hard-limit
// enforcement. Descendant policies are considered before ancestors, and each
// pressured aggregate advances one complete generation at a time only until
// it returns beneath its 125% soft target. Final publication separately checks
// every hard limit in refreshStreamingLossyDetails.
func fitStreamingPolicies[T any](rule Rule[T]) {
	switch typed := rule.(type) {
	case *allRule[T]:
		for _, child := range typed.children {
			fitStreamingPolicies(child)
		}
	case *inspectRule[T]:
		fitStreamingPolicies(typed.child)
	case *inspectionDetailsRule[T]:
		fitStreamingPolicies(typed.child)
		if typed.details.MemoryLimitAvailable {
			target := lossyBuildTarget(typed.details.MemoryLimitBytes)
			var candidates []streamingFitCandidate
			if collectStreamingFitCandidates(typed.child, &candidates) > target {
				fitStreamingAggregate(typed.child, target)
			}
		}
	}
}
