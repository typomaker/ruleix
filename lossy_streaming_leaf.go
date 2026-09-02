package ruleix

import (
	"fmt"

	"github.com/RoaringBitmap/roaring/v2"
)

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
		if _, ok := any(rule).(lossyAllCompiler[T]); ok {
			return &streamingAdaptiveLeaf[T]{child: rule}
		}
		if inspectionModeOf(rule) == RuleModeLossy {
			if _, ok := any(rule).(streamingLossyAccumulator); ok {
				return &streamingAdaptiveLeaf[T]{child: rule}
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
					return nil, inspectionDetails{}, fmt.Errorf(
						"ruleix: Lossy streaming state cannot fit the memory limit: %d > %d",
						details.MemoryUsageBytes, details.MemoryLimitBytes,
					)
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
	case *streamingAdaptiveLeaf[T]:
		return typed, typed.refreshedStreamingDetails(inspectionDetails{}), nil
	default:
		return rule, inspectionDetailsOf(rule), nil
	}
}

func unwrapStreamingAdaptiveLeaves[T any](rule Rule[T]) Rule[T] {
	switch typed := rule.(type) {
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			children[i] = unwrapStreamingAdaptiveLeaves(child)
		}
		return &allRule[T]{children: children}
	case *inspectRule[T]:
		return &inspectRule[T]{dst: typed.dst, child: unwrapStreamingAdaptiveLeaves(typed.child)}
	case *inspectionDetailsRule[T]:
		return &inspectionDetailsRule[T]{
			child: unwrapStreamingAdaptiveLeaves(typed.child), details: typed.details,
		}
	case *streamingAdaptiveLeaf[T]:
		return unwrapStreamingAdaptiveLeaves(typed.child)
	default:
		return rule
	}
}

// lossyAllPlanner exposes a finite exact-to-minimum representation ladder.
// compile keeps the existing single-leaf limit behavior; aggregate planning
// consumes the ladder directly instead of probing arbitrary byte limits.
