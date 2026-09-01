package ruleix

import (
	"fmt"
	"math"
)

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
