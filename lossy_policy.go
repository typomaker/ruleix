package ruleix

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

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

func compileLossyRules[T any](rule Rule[T], identity bool) (Rule[T], error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		return compileStreamingLossyTree(typed, identity, "Lossy")
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

// compileStreamingLossyTree publishes one current generation; later levels
// are derived lazily from it by the common streaming rebuild primitive.
func compileStreamingLossyTree[T any](rule Rule[T], identity bool, path string) (Rule[T], error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		if err := typed.validatePolicy(); err != nil {
			return nil, fmt.Errorf("ruleix: %s: %w", path, err)
		}
		child, err := compileStreamingLossyTree(typed.child, identity, path+"/child")
		if err != nil {
			return nil, err
		}
		if !identity {
			fitStreamingAggregateHard(child, typed.limit)
		}
		child, details, err := refreshStreamingLossyDetails(child)
		if err != nil {
			return nil, err
		}
		if !identity && details.MemoryUsageBytes > typed.limit {
			return nil, fmt.Errorf("ruleix: %s cannot fit the memory limit", path)
		}
		if !identity {
			details.MemoryLimitBytes, details.MemoryLimitAvailable = typed.limit, true
			child = clampLossyDiagnosticLimits(child, typed.limit)
		}
		return &inspectionDetailsRule[T]{child: child, details: details}, nil
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			compiled, err := compileStreamingLossyTree(child, identity, fmt.Sprintf("%s/All[%d]", path, i))
			if err != nil {
				return nil, err
			}
			children[i] = compiled
		}
		return &allRule[T]{children: children}, nil
	case *inspectRule[T]:
		child, err := compileStreamingLossyTree(typed.child, identity, path+"/Inspect")
		if err != nil {
			return nil, err
		}
		details := refreshedStreamingRuleDetails(child, inspectionDetailsOf(child))
		return &inspectRule[T]{dst: typed.dst, child: &inspectionDetailsRule[T]{child: child, details: details}}, nil
	default:
		provider, accounted := any(rule).(streamingDetailsProvider)
		if _, ok := any(rule).(streamingFirstGenerationFactory[T]); !ok || !accounted {
			return nil, fmt.Errorf("ruleix: %s: Lossy does not support this rule representation", path)
		}
		if validator, ok := any(rule).(interface{ validateStreamingLossy() error }); ok {
			if err := validator.validateStreamingLossy(); err != nil {
				return nil, fmt.Errorf("ruleix: %s: %w", path, err)
			}
		}
		details := provider.refreshedStreamingDetails(inspectionDetails{})
		return &inspectionDetailsRule[T]{child: &streamingAdaptiveLeaf[T]{child: rule}, details: details}, nil
	}
}

func clampLossyDiagnosticLimits[T any](rule Rule[T], limit uint64) Rule[T] {
	switch typed := rule.(type) {
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			children[i] = clampLossyDiagnosticLimits(child, limit)
		}
		return &allRule[T]{children: children}
	case *inspectRule[T]:
		return &inspectRule[T]{dst: typed.dst, child: clampLossyDiagnosticLimits(typed.child, limit)}
	case *inspectionDetailsRule[T]:
		details := typed.details
		if details.MemoryLimitAvailable {
			limit = min(details.MemoryLimitBytes, limit)
		}
		details.MemoryLimitBytes, details.MemoryLimitAvailable = limit, true
		return &inspectionDetailsRule[T]{child: clampLossyDiagnosticLimits(typed.child, limit), details: details}
	default:
		return rule
	}
}
