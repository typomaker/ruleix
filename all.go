package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Scanning up to eight candidate IDs avoids materializing every child result.
// Benchmarks across dense and sparse postings with 2, 4, and 8 children show
// bitmap intersection winning above this shared limit; see
// BenchmarkAllExecutionThreshold.
const allCandidateScanLimit = 8

// Equality matchers remain cheaper than materializing broad unions for
// medium candidate sets, but per-ID map and bitmap lookups lose decisively on
// large sets. Keep the representation-specific shortcut cardinality-gated.
const allCheapDirectIDScanLimit = 512

// Direct exclusion lookups include a getter and a map lookup per exclusion,
// so they stop paying off sooner than ordinary posting-list membership tests.
const allDirectExclusionScanLimit = 16

// All combines rules with logical AND: a stored constraint matches only when
// every child rule matches. All may be nested.
//
// For example, to match both country and customer tier:
//
//	ruleix.All(
//		ruleix.Include(func(c Constraint) (string, bool) { return c.Country, true }),
//		ruleix.Include(func(c Constraint) (string, bool) { return c.Tier, true }),
//	)
func All[T any](rules ...Rule[T]) Rule[T] { return &allRule[T]{children: rules} }

type allRule[T any] struct {
	children                []Rule[T]
	execution               []executionCapability[T]
	queryKeyProviders       []localQueryKeyProvider[T]
	planningProviders       []planningBitmapProvider[T]
	directIDComplete        bool
	equalityClassCount      uint32
	compiledEqualityClasses bool
	// sharedWildcardGroups is allocated only when Build finds equality children
	// whose interned, non-empty wildcard bitmap is identical. Ordinary All
	// searches therefore do not pay for duplicate-result tracking.
	sharedWildcardGroups []int
	duplicateEquality    *allDuplicateEquality[T]
	// Tests may search an internal rule before prepareSearch. Runtime indexes
	// set this flag and can distinguish an exact schema from an unprepared one.
	planningPrepared bool
	// executionCounters is nil in ordinary indexes. The internal A/B harness
	// attaches it to make executor work observable without adding a public mode
	// or API.
	executionCounters *allExecutionCounters
}

type allDuplicateEquality[T any] struct {
	bitmapIDs map[*roaring.Bitmap]uint32
	providers []equalityResultComponents[T]
}

type allExecutionCounters struct {
	materializations          uint64
	intersections             uint64
	containsChecks            uint64
	skippedOperands           uint64
	maskTests                 uint64
	physicalInspectorSearches uint64
	linearEqualityDedupRuns   uint64
}

func (*allRule[T]) rule() {}
func (r *allRule[T]) canonicalDescriptor() canonicalRuleDescriptor {
	return canonicalRuleDescriptor{representation: canonicalAll, schema: r}
}
func (*allRule[T]) inspectionStrategy() string { return "all" }
func (r *allRule[T]) inspectionMode() RuleMode {
	for _, child := range r.children {
		if inspectionModeOf(child) == RuleModeLossy {
			return RuleModeLossy
		}
	}
	return RuleModeExact
}
func (r *allRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	children := make([]Rule[T], 0, len(r.children))
	seen := make(map[Rule[T]]struct{}, len(r.children))
	for _, child := range r.children {
		state := canonicalRuleState(child, ids, hints)
		if _, canonical := child.(canonicalBuildRule); canonical {
			if _, duplicate := seen[state]; duplicate {
				continue
			}
			seen[state] = struct{}{}
		}
		children = append(children, state)
	}
	return &allRule[T]{children: children}
}
func (r *allRule[T]) validate(v T) error {
	for _, child := range r.children {
		if err := child.validate(v); err != nil {
			return err
		}
	}
	return nil
}
func (r *allRule[T]) insert(v T, id uint32) {
	for _, child := range r.children {
		child.insert(v, id)
	}
}
func (r *allRule[T]) exclude(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	for _, child := range r.children {
		child.exclude(v, dst, pool)
	}
}
func (r *allRule[T]) collectBuildStatistics(stats []nodeBuildStatistics) {
	for _, child := range r.children {
		child.collectBuildStatistics(stats)
	}
}
func (r *allRule[T]) prepareSearch() {
	r.queryKeyProviders = r.queryKeyProviders[:0]
	hasQueryKeyGroup := false
	for _, child := range r.children {
		prepareRuleSearch(child)
		if _, ok := child.(localQueryKeyProviderGroup[T]); ok {
			hasQueryKeyGroup = true
		}
	}
	for i, child := range r.children {
		if r.execution == nil {
			r.execution = make([]executionCapability[T], len(r.children))
		}
		r.execution[i] = describeExecutionCapability(child)
		if r.execution[i].posting != nil {
			if r.planningProviders == nil {
				r.planningProviders = make([]planningBitmapProvider[T], len(r.children))
			}
			r.planningProviders[i] = r.execution[i].posting
		}
	}
	if hasQueryKeyGroup {
		r.queryKeyProviders = make([]localQueryKeyProvider[T], 0, len(r.children)+1)
		for childIndex, child := range r.children {
			if group, ok := child.(localQueryKeyProviderGroup[T]); ok {
				r.queryKeyProviders = append(r.queryKeyProviders, group.localQueryKeyProviders()...)
			} else {
				r.queryKeyProviders = append(r.queryKeyProviders, r.execution[childIndex].queryKey)
			}
		}
	}
	r.directIDComplete = true
	for i := range r.children {
		if r.execution[i].directID == nil {
			r.directIDComplete = false
			break
		}
	}
	r.prepareSharedWildcardGroups()
	if !r.compiledEqualityClasses {
		r.prepareDuplicateEqualityResults()
	}
	r.planningPrepared = true
}

func (r *allRule[T]) supportsDirectIDMatch(index int) bool {
	return r.executionCapability(index).directID != nil
}

func (r *allRule[T]) prepareSharedWildcardGroups() {
	if r.sharedWildcardGroups != nil {
		return
	}
	firstByWildcard := make(map[*roaring.Bitmap]int)
	nextGroup := 1
	for i, child := range r.children {
		equality, ok := sharedWildcardOf(child)
		if !ok || equality.sharedWildcard().IsEmpty() {
			continue
		}
		wildcard := equality.sharedWildcard()
		first, found := firstByWildcard[wildcard]
		if !found {
			firstByWildcard[wildcard] = i
			continue
		}
		if r.sharedWildcardGroups == nil {
			r.sharedWildcardGroups = make([]int, len(r.children))
		}
		group := r.sharedWildcardGroups[first]
		if group == 0 {
			group = nextGroup
			nextGroup++
			r.sharedWildcardGroups[first] = group
		}
		r.sharedWildcardGroups[i] = group
	}
}

func (r *allRule[T]) prepareDuplicateEqualityResults() {
	owners := make(map[*roaring.Bitmap]int)
	providers := make([]equalityResultComponents[T], len(r.children))
	nextID := uint32(1)
	for i, child := range r.children {
		provider := resolveEqualityResultComponents(child)
		if provider == nil {
			continue
		}
		providers[i] = provider
		provider.visitEqualityResultBitmaps(func(bits *roaring.Bitmap) {
			if r.duplicateEquality != nil && r.duplicateEquality.bitmapIDs[bits] != 0 {
				r.duplicateEquality.providers[i] = provider
				return
			}
			if owner, found := owners[bits]; found && owner != i {
				if r.duplicateEquality == nil {
					r.duplicateEquality = &allDuplicateEquality[T]{
						bitmapIDs: make(map[*roaring.Bitmap]uint32),
						providers: make([]equalityResultComponents[T], len(r.children)),
					}
				}
				r.duplicateEquality.bitmapIDs[bits] = nextID
				nextID++
				r.duplicateEquality.providers[owner] = providers[owner]
				r.duplicateEquality.providers[i] = provider
				return
			}
			owners[bits] = i
		})
	}
}
func (r *allRule[T]) optimize(total uint64) Rule[T] {
	if len(r.children) == 0 {
		universe := roaring.New()
		universe.AddRange(0, total)
		return newMatchAllRule[T](universe)
	}
	children := make([]Rule[T], 0, len(r.children))
	seen := make(map[canonicalRuleDescriptor]struct{}, len(r.children))
	var universal *matchAllRule[T]
	appendChild := func(child Rule[T]) {
		if canonical, ok := child.(canonicalBuildRule); ok {
			descriptor := canonical.canonicalDescriptor()
			if _, duplicate := seen[descriptor]; duplicate {
				return
			}
			seen[descriptor] = struct{}{}
		}
		children = append(children, child)
	}
	for _, child := range r.children {
		optimized := optimizeRule(child, total)
		if matchAll, ok := optimized.(*matchAllRule[T]); ok {
			universal = matchAll
			continue
		}
		if nested, ok := optimized.(*allRule[T]); ok {
			for _, nestedChild := range nested.children {
				appendChild(nestedChild)
			}
			continue
		}
		appendChild(optimized)
	}
	if len(children) == 0 {
		return universal
	}
	if len(children) == 1 {
		if observed, ok := children[0].(*inspectedRuntimeRule[T]); ok && len(observed.aliases) != 0 {
			return &allRule[T]{children: children}
		}
		return children[0]
	}
	return &allRule[T]{children: children}
}
func (r *allRule[T]) cardinality(v T, pool *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, pool)
}
func (r *allRule[T]) estimateCardinality(v T) uint64 {
	estimate := ^uint64(0)
	for _, child := range r.children {
		if estimator, ok := child.(cardinalityEstimator[T]); ok {
			childEstimate := estimator.estimateCardinality(v)
			if childEstimate == 0 {
				return 0
			}
			estimate = min(estimate, childEstimate)
		} else if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
			return 0
		}
	}
	return estimate
}
func (r *allRule[T]) estimateCachedCardinality(v T, pool *bitmapPool) (uint64, bool) {
	if pool.local == nil {
		return 0, false
	}
	estimate := ^uint64(0)
	usedCache := false
	for _, child := range r.children {
		childEstimate, cached := cachedCardinality(child, v, pool)
		if !cached {
			estimator, ok := child.(cardinalityEstimator[T])
			if !ok {
				continue
			}
			childEstimate = estimator.estimateCardinality(v)
		} else {
			usedCache = true
		}
		if childEstimate == 0 {
			return 0, usedCache
		}
		estimate = min(estimate, childEstimate)
	}
	return estimate, usedCache
}
func (r *allRule[T]) estimateCheapCardinality(v T) uint64 {
	estimate := ^uint64(0)
	for _, child := range r.children {
		if childEstimate, ok := cheapCardinality(child, v); ok {
			if childEstimate == 0 {
				return 0
			}
			estimate = min(estimate, childEstimate)
		} else if cheapCardinalityIsZero(child, v) {
			return 0
		}
	}
	return estimate
}
func (r *allRule[T]) isCheapCardinalityZero(v T) bool {
	return r.estimateCheapCardinality(v) == 0
}
func (r *allRule[T]) isCardinalityZero(v T) bool {
	for _, child := range r.children {
		if checker, ok := child.(cardinalityZeroChecker[T]); ok && checker.isCardinalityZero(v) {
			return true
		}
	}
	return false
}
func (r *allRule[T]) matchesID(v T, id uint32) bool {
	for i := range r.children {
		matcher := r.executionCapability(i).directID
		if matcher == nil || !matcher.matchesID(v, id) {
			return false
		}
	}
	return true
}
func (r *allRule[T]) search(v T, dst *roaring.Bitmap, pool *bitmapPool) {
	r.searchObserved(v, dst, pool, nil)
}

func (r *allRule[T]) searchObserved(v T, dst *roaring.Bitmap, pool *bitmapPool, metrics *inspectorRuntime) {
	r.searchObservedAliases(v, dst, pool, metrics, nil)
}

func (r *allRule[T]) searchObservedAliases(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	metrics *inspectorRuntime,
	aliases []*inspectorRuntime,
) {
	// Most All groups are small. Keeping their ranking storage on the stack
	// avoids a service allocation without adding shared mutable state.
	var inline [8]rankedBitmap
	if len(r.children) <= len(inline) && r.equalityClassCount <= 64 {
		var checked [1]uint64
		r.searchRanked(v, dst, pool, inline[:len(r.children)], metrics, aliases, checked[:])
		return
	}
	buffer := pool.getRanked(len(r.children))
	words := int((r.equalityClassCount + 63) / 64)
	if words > 0 {
		if cap(buffer.mask) < words {
			buffer.mask = make([]uint64, words)
		} else {
			buffer.mask = buffer.mask[:words]
			clear(buffer.mask)
		}
	}
	r.searchRanked(v, dst, pool, buffer.items, metrics, aliases, buffer.mask)
	pool.putRanked(buffer)
}

func (r *allRule[T]) searchRanked(
	v T,
	dst *roaring.Bitmap,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
	metrics *inspectorRuntime,
	metricAliases []*inspectorRuntime,
	checkedClasses []uint64,
) {
	if !r.rankChildren(v, pool, rankedChildren) {
		return
	}
	if len(rankedChildren) == 0 {
		return
	}
	if r.duplicateEquality != nil {
		if r.executionCounters != nil {
			r.executionCounters.linearEqualityDedupRuns++
		}
		rankedChildren = r.deduplicateEqualityResults(v, rankedChildren)
	} else if r.equalityClassCount != 0 {
		rankedChildren = r.deduplicateEqualityClasses(v, rankedChildren, checkedClasses)
	}
	// Local plans prioritize reuse and must keep their zero-allocation hot path.
	// For uncached Index searches, exact postings can be compared by complete
	// operation cost without speculative representation work.
	if pool.local == nil && len(rankedChildren) > 1 {
		first := r.selectInitialBitmapSource(rankedChildren)
		rankedChildren[0], rankedChildren[first] = rankedChildren[first], rankedChildren[0]
	}
	if !r.shouldValidateCandidates(rankedChildren) {
		if !r.intersectRankedInOrderObserved(v, dst, pool, rankedChildren, metrics, metricAliases) {
			r.releaseRanked(pool, rankedChildren)
			return
		}
		r.releaseRanked(pool, rankedChildren)
		return
	}
	first := rankedChildren[0].bits
	owned := false
	if first == nil {
		first = pool.get()
		r.children[rankedChildren[0].childIdx].search(v, first, pool)
		owned = true
	}
	dst.Or(first)
	r.validateCandidateBitmap(v, dst, pool, rankedChildren[1:])
	if owned {
		pool.put(first)
	}
	r.releaseRanked(pool, rankedChildren[1:])
}

func resolveEqualityClassProvider[T any](rule Rule[T]) equalityClassProvider[T] {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		return resolveEqualityClassProvider(observed.child)
	}
	provider, _ := rule.(equalityClassProvider[T])
	return provider
}
