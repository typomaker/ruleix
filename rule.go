// Package ruleix implements a strongly typed in-memory rule index.
package ruleix

import "github.com/RoaringBitmap/roaring/v2"

// Compare defines an ordering for values used by ordered filters. It returns a
// negative number when a < b, zero when a == b, and a positive number when
// a > b. The standard library's cmp.Compare is suitable for ordered types.
type Compare[V any] func(a, b V) int

// Getter returns a value and reports whether it is present. A missing value is
// interpreted as a wildcard by filters. The boolean keeps zero values distinct
// from missing values without requiring pointers or heap allocations.
type Getter[T, V any] func(T) (V, bool)

// GetterFromPointer adapts an old pointer getter during migration. New schemas
// should return (value, ok) directly to avoid pointers to temporary values.
func GetterFromPointer[T, V any](get func(T) *V) Getter[T, V] {
	return func(value T) (V, bool) {
		result := get(value)
		if result == nil {
			var zero V
			return zero, false
		}
		return *result, true
	}
}

type optionalValue[V any] struct {
	value V
	ok    bool
}

func getOptional[T, V any](get Getter[T, V], value T) optionalValue[V] {
	v, ok := get(value)
	return optionalValue[V]{value: v, ok: ok}
}

// Rule describes how constraints and query values of T are matched. Construct
// rules with Include, Exclude, the ordered filters, Between, CompareBy, and All.
// Its implementation is sealed so an index can rely on all rule invariants.
type Rule[T any] interface {
	rule()
	newState(*nodeIDAllocator, *buildStatistics) Rule[T]
	validate(T) error
	insert(T, uint32)
	cardinality(T, *bitmapPool) uint64
	search(T, *roaring.Bitmap, *bitmapPool)
	exclude(T, *roaring.Bitmap, *bitmapPool)
	collectBuildStatistics([]nodeBuildStatistics)
}

type ruleOptimizer[T any] interface {
	optimize(uint64) Rule[T]
}

type ruleSearchPreparer interface {
	prepareSearch()
}

// cardinalityZeroChecker is implemented by rules that can determine an empty
// result without materializing their posting lists.
type cardinalityZeroChecker[T any] interface {
	isCardinalityZero(T) bool
}

// cardinalityEstimator is implemented by rules whose output size can be
// estimated without doing work comparable to materializing their result.
type cardinalityEstimator[T any] interface {
	estimateCardinality(T) uint64
}

// cachedCardinalityEstimator exposes a result that has already been
// materialized by this Local. The lookup must be observational: planning must
// not create a cache, affect admission, or update replacement state.
type cachedCardinalityEstimator[T any] interface {
	estimateCachedCardinality(T, *bitmapPool) (uint64, bool)
}

// cachedBitmapProvider lets All consume an immutable bitmap already owned by a
// Local cache instead of copying it through a temporary child result.
type cachedBitmapProvider[T any] interface {
	lookupCachedBitmap(T, *bitmapPool) (*roaring.Bitmap, bool)
}

// planningBitmapProvider exposes an immutable posting list found while All is
// ranking its children. The bitmap remains owned by the rule and may only be
// read by the search path.
type planningBitmapProvider[T any] interface {
	lookupPlanningBitmap(T) (*roaring.Bitmap, bool)
}

// cheapCardinalityEstimator is implemented by rules whose cardinality is a
// constant-time lookup. All uses these estimates before ordered estimates so a
// sufficiently small equality result can become the candidate set without a
// boundary lookup or posting-cardinality scan.
type cheapCardinalityEstimator[T any] interface {
	estimateCheapCardinality(T) uint64
}

// cheapCardinalityZeroChecker marks emptiness checks that do not traverse
// ordered postings. It lets nested All nodes preserve an exact empty result in
// the equality-first pass.
type cheapCardinalityZeroChecker[T any] interface {
	isCheapCardinalityZero(T) bool
}

// ruleIDMatcher validates one internal ID without materializing a result.
type ruleIDMatcher[T any] interface {
	matchesID(T, uint32) bool
}

// candidateFilter narrows an existing candidate bitmap without materializing
// the rule's complete result. Implementations must only remove IDs from dst.
type candidateFilter[T any] interface {
	filterCandidates(T, *roaring.Bitmap, *bitmapPool)
}

// sharedWildcardEquality exposes the two disjoint parts of an equality match
// to All. Equal wildcard pointers are guaranteed immutable after Build and
// arise naturally from bitmap interning.
type sharedWildcardEquality[T any] interface {
	sharedWildcard() *roaring.Bitmap
	addConcreteMatches(T, *roaring.Bitmap)
	intersectConcreteMatches(T, *roaring.Bitmap, *bitmapPool)
}

// equalityResultComponents exposes the immutable wildcard and concrete
// posting that form an equality result. All assigns IDs only to components
// that Build finds in more than one child.
type equalityResultComponents[T any] interface {
	visitEqualityResultBitmaps(func(*roaring.Bitmap))
	lookupEqualityResultComponents(T) (wildcard, posting *roaring.Bitmap, deduplicable bool)
}

// equalityClassCompiler exposes representation-native sources only while Build
// assigns dense query-result classes. The first pass reports source pairs
// without retaining per-posting setters; the second writes the selected class
// directly into each representation's stable slots.
type equalityClassCompiler interface {
	equalitySourceCount() int
	visitEqualitySources(func(equalitySourcePair))
	assignEqualityClasses(map[equalitySourcePair]uint32)
}

// equalityClassProvider returns the dense All-local class selected by the
// query. Zero means that this result is unique and needs no execution check.
type equalityClassProvider[T any] interface {
	lookupEqualityClass(T) uint32
}

type equalitySourcePair struct {
	wildcard physicalSourceID
	posting  physicalSourceID
}

type exclusionRule[T any] interface {
	exclude(T, *roaring.Bitmap, *bitmapPool)
	isExcluded(T, uint32) bool
	hasExclusions() bool
}

func collectExclusionRules[T any](rule Rule[T], dst []exclusionRule[T]) []exclusionRule[T] {
	switch typed := rule.(type) {
	case *inspectedRuntimeRule[T]:
		if exclusion, ok := typed.child.(exclusionRule[T]); ok && exclusion.hasExclusions() {
			dst = append(dst, &inspectedExclusionRule[T]{child: exclusion, metrics: typed.metrics})
		} else {
			dst = collectExclusionRules(typed.child, dst)
		}
	case *allRule[T]:
		for _, child := range typed.children {
			dst = collectExclusionRules(child, dst)
		}
	case exclusionRule[T]:
		if typed.hasExclusions() {
			dst = append(dst, typed)
		}
	}
	return dst
}

// removeExclusionRules removes Exclude nodes from the positive match tree.
// Exclusions are evaluated separately after candidate selection, so retaining
// their wildcard/concrete union in the positive tree only duplicates every ID.
func removeExclusionRules[T any](rule Rule[T], universe *roaring.Bitmap) Rule[T] {
	switch typed := rule.(type) {
	case *inspectedRuntimeRule[T]:
		if _, ok := typed.child.(exclusionRule[T]); ok {
			return newMatchAllRule[T](universe)
		}
		return &inspectedRuntimeRule[T]{child: removeExclusionRules(typed.child, universe), metrics: typed.metrics}
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			children[i] = removeExclusionRules(child, universe)
		}
		return (&allRule[T]{children: children}).optimize(universe.GetCardinality())
	case exclusionRule[T]:
		return newMatchAllRule[T](universe)
	default:
		return rule
	}
}

func prepareRuleSearch[T any](rule Rule[T]) {
	if observed, ok := rule.(*inspectedRuntimeRule[T]); ok {
		prepareRuleSearch(observed.child)
		return
	}
	if preparer, ok := rule.(ruleSearchPreparer); ok {
		preparer.prepareSearch()
	}
}

// prepareBitmapForSearch enables cheap sharing of immutable posting-list
// containers. Clone marks every existing container copy-on-write up front;
// doing it during Build avoids both allocation and mutation (and therefore a
// potential data race) on the first concurrent Search.
func prepareBitmapForSearch(bits *roaring.Bitmap) {
	bits.SetCopyOnWrite(true)
	_ = bits.Clone()
}

func optimizeRule[T any](rule Rule[T], total uint64) Rule[T] {
	if optimizer, ok := rule.(ruleOptimizer[T]); ok {
		return optimizer.optimize(total)
	}
	return rule
}

type matchAllRule[T any] struct{ bits *roaring.Bitmap }

func (*matchAllRule[T]) rule()                                                 {}
func (r *matchAllRule[T]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*matchAllRule[T]) validate(T) error                                      { return nil }
func (*matchAllRule[T]) insert(T, uint32)                                      {}
func (r *matchAllRule[T]) cardinality(T, *bitmapPool) uint64                   { return r.bits.GetCardinality() }
func (r *matchAllRule[T]) estimateCardinality(T) uint64                        { return r.bits.GetCardinality() }
func (r *matchAllRule[T]) estimateCheapCardinality(T) uint64                   { return r.bits.GetCardinality() }
func (r *matchAllRule[T]) lookupPlanningBitmap(T) (*roaring.Bitmap, bool)      { return r.bits, true }
func (r *matchAllRule[T]) matchesID(_ T, id uint32) bool                       { return r.bits.Contains(id) }
func (r *matchAllRule[T]) search(_ T, dst *roaring.Bitmap, _ *bitmapPool)      { dst.Or(r.bits) }
func (*matchAllRule[T]) exclude(T, *roaring.Bitmap, *bitmapPool)               {}
func (*matchAllRule[T]) collectBuildStatistics([]nodeBuildStatistics)          {}
func (*matchAllRule[T]) inspectionStrategy() string                            { return "match-all" }
func (r *matchAllRule[T]) prepareSearch()                                      { prepareBitmapForSearch(r.bits) }
func (r *matchAllRule[T]) internBitmaps(interner *bitmapInterner)              { interner.intern(&r.bits) }

func newMatchAllRule[T any](bits *roaring.Bitmap) Rule[T] {
	return &matchAllRule[T]{bits: bits}
}

type nodeID uint32

type nodeIDAllocator struct {
	next      nodeID
	canonical map[any]any
}

type canonicalRepresentation uint8

const (
	canonicalAll canonicalRepresentation = iota + 1
	canonicalEquality
	canonicalOrdered
	canonicalBetween
	canonicalCompareBy
)

type canonicalBoundRole uint8

const (
	canonicalWholeValue canonicalBoundRole = iota
	canonicalLowerBound
	canonicalUpperBound
	canonicalStoredOperator
)

type canonicalWildcardPolicy uint8

const (
	canonicalMissingStoredMatches canonicalWildcardPolicy = iota
)

type canonicalComparatorPolicy uint8

const (
	canonicalNoComparator canonicalComparatorPolicy = iota
	canonicalOpaqueComparator
)

// canonicalOperationID is a build-scoped proof, not a content fingerprint.
// owner and queryBound are pointer-backed schema objects, so independently
// opaque getters and comparators can never become aliases merely because they
// return the same values for the entries in one Build.
type canonicalOperationID struct {
	representation canonicalRepresentation
	owner          any
	queryBound     any
	role           canonicalBoundRole
	operator       Operator
	direction      direction
	inclusive      bool
	wildcard       canonicalWildcardPolicy
	comparator     canonicalComparatorPolicy
}

// canonicalRuleDescriptor is deliberately build-scoped and identity-based.
// The representation tag records the complete operator/wildcard/comparator
// policy selected by the constructor; schema is the exact pointer-backed rule
// instance carrying its getters and children. Independently constructed rules
// are therefore never inferred equivalent from function pointers or postings.
type canonicalRuleDescriptor struct {
	representation canonicalRepresentation
	schema         any
	operations     [5]canonicalOperationID
	operationCount uint8
}

type canonicalBuildRule interface {
	canonicalDescriptor() canonicalRuleDescriptor
}

func canonicalRuleState[T any](rule Rule[T], ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	state, _ := canonicalRuleStateReuse(rule, ids, hints)
	return state
}

func canonicalRuleStateReuse[T any](
	rule Rule[T],
	ids *nodeIDAllocator,
	hints *buildStatistics,
) (Rule[T], bool) {
	canonical, ok := rule.(canonicalBuildRule)
	if !ok {
		return rule.newState(ids, hints), false
	}
	if ids.canonical == nil {
		ids.canonical = make(map[any]any)
	}
	descriptor := canonical.canonicalDescriptor()
	if state, ok := ids.canonical[descriptor]; ok {
		return state.(Rule[T]), true
	}
	state := rule.newState(ids, hints)
	ids.canonical[descriptor] = state
	return state, false
}

func (a *nodeIDAllocator) allocate() nodeID {
	id := a.next
	a.next++
	return id
}

type orderedBuildStatistics struct {
	uniqueValues int
	blocks       int
}

type nodeBuildStatistics struct {
	equalityValues int
	ordered        orderedBuildStatistics
	between        [2]orderedBuildStatistics
	compareBy      [5]orderedBuildStatistics
}

type buildStatistics struct {
	entries   int
	uniqueIDs int
	nodes     []nodeBuildStatistics
}

// capacityHint adds five percent to the last observed size. It saturates at
// the largest int so corrupt or unusually large statistics cannot overflow
// an allocation size. Zero remains zero: there is no useful history yet.
func capacityHint(previous int) int {
	if previous <= 0 {
		return 0
	}
	maxInt := int(^uint(0) >> 1)
	growth := previous / 20
	if growth < 1 {
		growth = 1
	}
	if previous > maxInt-growth {
		return maxInt
	}
	return previous + growth
}

func (s *buildStatistics) node(id nodeID) nodeBuildStatistics {
	if s == nil || int(id) >= len(s.nodes) {
		return nodeBuildStatistics{}
	}
	return s.nodes[id]
}

func measuredCardinality[T any](r Rule[T], value T, pool *bitmapPool) uint64 {
	bm := pool.get()
	r.search(value, bm, pool)
	n := bm.GetCardinality()
	pool.put(bm)
	return n
}
