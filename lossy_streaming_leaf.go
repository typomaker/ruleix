package ruleix

import "fmt"

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
		return &inspectionDetailsRule[T]{
			child: wrapStreamingLossyLeaves(typed.child), details: typed.details, mode: typed.mode,
		}
	default:
		if inspectionModeOf(rule) == RuleModeLossy {
			if _, ok := any(rule).(streamingLossyAccumulator); ok {
				return &streamingAdaptiveLeaf[T]{child: rule}
			}
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
		} else if !details.MemoryUsageAvailable {
			details = typed.details
		}
		if typed.details.MemoryLimitAvailable {
			details.MemoryLimitBytes, details.MemoryLimitAvailable = typed.details.MemoryLimitBytes, true
			if details.MemoryUsageBytes > details.MemoryLimitBytes {
				fitStreamingAggregateHard(child, details.MemoryLimitBytes)
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
		return &inspectionDetailsRule[T]{child: child, details: details, mode: lossyPolicyMode(child)}, details, nil
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
			child: unwrapStreamingAdaptiveLeaves(typed.child), details: typed.details, mode: typed.mode,
		}
	case *streamingAdaptiveLeaf[T]:
		return unwrapStreamingAdaptiveLeaves(typed.child)
	default:
		return rule
	}
}
