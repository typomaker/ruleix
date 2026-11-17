package ruleix

import "github.com/RoaringBitmap/roaring/v2"

//nolint:gocognit // Ranking intentionally keeps the hot-path decisions in one pass.
func (r *allRule[T]) rankChildren(
	v T,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	refreshLocalPlan := false
	// Before any group creates a plan, both reuse lookups can only miss.
	if pool.local != nil && pool.allPlans != nil {
		if result, reused := r.reuseLocalPlan(v, pool, rankedChildren); reused {
			return result
		}
		refreshLocalPlan = r.localPlan(pool) != nil
	}
	// Rank constant-time equality bounds first. If one is already small enough
	// for direct ID validation, ordered cardinalities cannot improve the chosen
	// execution mode and would only repeat their boundary and posting scans.
	cheapBound := ^uint64(0)
	order := r.emptyOrder
	for position := range r.children {
		i := position
		if order != 0 {
			i = int(order&15) - 1
			order >>= 4
		}
		child := r.children[i]
		estimate := ^uint64(0)
		var planningBits *roaring.Bitmap
		var planningFound bool
		if len(r.planningProviders) != 0 || !r.planningPrepared {
			planningBits, planningFound = r.lookupPlanningBitmap(i, child, v)
		}
		if planningFound {
			bits := planningBits
			estimate = bits.GetCardinality()
			rankedChildren[i].bits = bits
			if estimate == 0 {
				rememberEmptyRank(pool, i)
				return false
			}
			cheapBound = min(cheapBound, estimate)
		} else if cheapEstimate, ok := cheapCardinality(child, v); ok {
			estimate = cheapEstimate
			if estimate == 0 {
				rememberEmptyRank(pool, i)
				return false
			}
			cheapBound = min(cheapBound, estimate)
		} else if cheapCardinalityIsZero(child, v) {
			rememberEmptyRank(pool, i)
			return false
		}
		rankedChildren[i].card = estimate
		rankedChildren[i].childIdx = i
	}
	//nolint:nestif // Cache-aware ranking keeps the hot path allocation-free.
	if cheapBound > allCandidateScanLimit {
		for i, child := range r.children {
			if rankedChildren[i].card != ^uint64(0) {
				continue
			}
			if provider := r.childExecution(i).cached; provider != nil {
				if bits, found := provider.lookupCachedBitmap(v, pool); found {
					rankedChildren[i].bits = bits
					rankedChildren[i].card = bits.GetCardinality()
					if rankedChildren[i].card == 0 {
						return false
					}
					continue
				}
			}
			if estimate, ok := estimateCardinalityForPlan(child, v, pool); ok {
				if estimate == 0 {
					return false
				}
				rankedChildren[i].card = estimate
			} else if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
				return false
			}
		}
	}
	// Filter groups are normally small; insertion sort avoids reflection and a
	// closure allocation while preserving schema order for equal estimates.
	for i := 1; i < len(rankedChildren); i++ {
		for j := i; j > 0 && rankedChildren[j].card < rankedChildren[j-1].card; j-- {
			rankedChildren[j], rankedChildren[j-1] = rankedChildren[j-1], rankedChildren[j]
		}
	}
	if pool.local != nil {
		r.rememberLocalPlan(pool, rankedChildren)
		if refreshLocalPlan {
			if result, reused := r.reuseLocalPlan(v, pool, rankedChildren); reused {
				return result
			}
		}
	}
	return true
}
