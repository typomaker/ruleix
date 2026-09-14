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
		if _, ok := any(rule).(streamingLossyAccumulator); ok {
			return &streamingAdaptiveLeaf[T]{child: rule, approximate: true}
		}
		return rule
	}
}

func refreshStreamingLossyDetails[T any](rule Rule[T]) (Rule[T], inspectionDetails, error) {
	switch typed := rule.(type) {
	case *allRule[T]:
		return refreshStreamingLossyAll(typed)
	case *inspectRule[T]:
		child, details, err := refreshStreamingLossyDetails(typed.child)
		return &inspectRule[T]{dst: typed.dst, child: child}, details, err
	case *inspectionDetailsRule[T]:
		return refreshStreamingInspectionDetails(typed)
	case *streamingAdaptiveLeaf[T]:
		return typed, typed.refreshedStreamingDetails(inspectionDetails{}), nil
	default:
		return rule, inspectionDetailsOf(rule), nil
	}
}

func refreshStreamingLossyAll[T any](typed *allRule[T]) (Rule[T], inspectionDetails, error) {
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
}

func refreshStreamingInspectionDetails[T any](
	typed *inspectionDetailsRule[T],
) (Rule[T], inspectionDetails, error) {
	limitAvailable := typed.details.MemoryLimitAvailable
	limit := typed.details.MemoryLimitBytes
	if limitAvailable {
		fitStreamingRule(typed.child, limit)
	}
	child, details, err := refreshStreamingLossyDetails(typed.child)
	if err != nil {
		return nil, inspectionDetails{}, err
	}
	if limitAvailable {
		if fitter, ok := any(child).(streamingLimitFitter); ok {
			fitter.fitStreamingLimit(limit)
		}
	}
	if provider, ok := any(child).(streamingDetailsProvider); ok {
		details = provider.refreshedStreamingDetails(typed.details)
	} else if !details.MemoryUsageAvailable {
		details = typed.details
	}
	if limitAvailable {
		child, details, err = enforceRefreshedStreamingLimit(child, details, limit)
		if err != nil {
			return nil, inspectionDetails{}, err
		}
	}
	wrapped := &inspectionDetailsRule[T]{child: child, details: details, mode: lossyPolicyMode(child)}
	return wrapped, details, nil
}

func enforceRefreshedStreamingLimit[T any](
	child Rule[T], details inspectionDetails, limit uint64,
) (Rule[T], inspectionDetails, error) {
	details.MemoryLimitBytes, details.MemoryLimitAvailable = limit, true
	if details.MemoryUsageBytes <= limit {
		return child, details, nil
	}
	fitStreamingAggregateHard(child, limit)
	refreshed, details, err := refreshStreamingLossyDetails(child)
	if err != nil {
		return nil, inspectionDetails{}, err
	}
	details.MemoryLimitBytes, details.MemoryLimitAvailable = limit, true
	if details.MemoryUsageBytes > limit {
		return nil, inspectionDetails{}, fmt.Errorf(
			"ruleix: Lossy streaming state cannot fit the memory limit: %d > %d",
			details.MemoryUsageBytes, details.MemoryLimitBytes,
		)
	}
	return refreshed, details, nil
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
		mode := typed.mode
		if lossyPolicyMode(typed.child) == RuleModeLossy {
			mode = RuleModeLossy
		}
		return &inspectionDetailsRule[T]{
			child: unwrapStreamingAdaptiveLeaves(typed.child), details: typed.details, mode: mode,
		}
	case *streamingAdaptiveLeaf[T]:
		return unwrapStreamingAdaptiveLeaves(typed.child)
	default:
		return rule
	}
}
