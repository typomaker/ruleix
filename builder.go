package ruleix

import (
	"fmt"
	"iter"
	"math"

	"github.com/RoaringBitmap/roaring/v2"
)

// Builder constructs immutable indexes from the same Rule schema. A Builder is
// not safe for concurrent calls to Build; callers that need them must provide
// their own synchronization. Indexes returned by completed builds are
// independent and safe for concurrent use.
type Builder[C any, ID comparable] struct {
	schema Rule[C]
	hints  buildStatistics
}

// Index maps query values to the unique IDs of all matching stored constraints.
// It is immutable after Build and safe for concurrent calls to Search and Visit.
type Index[C any, ID comparable] struct {
	root       Rule[C]
	values     []ID
	pool       *bitmapPool
	nodes      int
	exclusions []exclusionRule[C]
}

// Local is a search context that keeps per-node cached results between calls.
// It must not be used concurrently by multiple goroutines.
type Local[C any, ID comparable] struct {
	index *Index[C, ID]
	pool  *bitmapPool
}

// New constructs a Builder from a strongly typed rule schema. New panics when
// schema is nil.
func New[C any, ID comparable](schema Rule[C]) *Builder[C, ID] {
	if schema == nil {
		panic("ruleix: nil schema")
	}
	return &Builder[C, ID]{schema: schema}
}

// Build consumes entries and returns an immutable, concurrently searchable
// Index. Constraints that share an external ID are combined under that ID,
// which is returned at most once by a search. Build must not be called
// concurrently on the same Builder; the library deliberately leaves
// synchronization of builds to the caller. A failed build does not prevent
// later calls.
func (b *Builder[C, ID]) Build(entries iter.Seq2[C, ID]) (*Index[C, ID], error) {
	ix, statistics, err := buildIndex[C, ID](b.schema, entries, true, &b.hints)
	if err != nil {
		return nil, err
	}
	// Publish statistics only after the input sequence has returned and the
	// complete index has passed validation. Failed builds must leave the last
	// successful hints intact for the next rebuild.
	b.hints = statistics
	return ix, nil
}

func buildIndex[C any, ID comparable](
	schema Rule[C],
	entries iter.Seq2[C, ID],
	collectStatistics bool,
	hints *buildStatistics,
) (*Index[C, ID], buildStatistics, error) {
	if entries == nil {
		return nil, buildStatistics{}, fmt.Errorf("ruleix: nil entry sequence")
	}
	// A schema contains only immutable getters, comparators, and structure.
	// Every build receives fresh mutable indexes, so even a future reusable
	// schema cannot modify an Index returned by an earlier build.
	ids := &nodeIDAllocator{}
	state := schema.newState(ids, hints)
	uniqueIDCapacity := 0
	if hints != nil {
		uniqueIDCapacity = capacityHint(hints.uniqueIDs)
	}
	ix := &Index[C, ID]{root: state, values: make([]ID, 0, uniqueIDCapacity), pool: newBitmapPool()}
	internalIDs := make(map[ID]uint32, uniqueIDCapacity)
	var buildErr error
	entryIndex := 0
	// TODO: Explore an optional bulk-build path that buffers IDs per posting and
	// flushes them with roaring.AddMany. It substantially reduces insertion CPU
	// for large postings, but per-posting buffers increase peak build memory and
	// do not suit the current streaming contract; see BenchmarkBitmapAddMany.
	entries(func(constraint C, id ID) bool {
		if uint64(len(ix.values)) > math.MaxUint32 {
			buildErr = fmt.Errorf("ruleix: at most 2^32 rules are supported")
			return false
		}
		if err := ix.root.validate(constraint); err != nil {
			buildErr = fmt.Errorf("ruleix: entry %d: %w", entryIndex, err)
			return false
		}
		internalID, exists := internalIDs[id]
		if !exists {
			internalID = uint32(len(ix.values))
			internalIDs[id] = internalID
			ix.values = append(ix.values, id)
		}
		ix.root.insert(constraint, internalID)
		entryIndex++
		return true
	})
	if buildErr != nil {
		return nil, buildStatistics{}, buildErr
	}
	statistics := buildStatistics{entries: entryIndex, uniqueIDs: len(ix.values)}
	if collectStatistics {
		statistics.nodes = make([]nodeBuildStatistics, int(ids.next))
		ix.root.collectBuildStatistics(statistics.nodes)
	}
	ix.root = optimizeRule(ix.root, uint64(len(ix.values)))
	ix.exclusions = collectExclusionRules(ix.root, nil)
	if len(ix.exclusions) != 0 {
		universe := roaring.New()
		universe.AddRange(0, uint64(len(ix.values)))
		ix.root = removeExclusionRules(ix.root, universe)
		// Keep the specialized All search path even when removing exclusions
		// leaves one positive child. It can test small candidate sets directly
		// without materializing a separate exclusion bitmap.
		if _, ok := ix.root.(*allRule[C]); !ok {
			ix.root = &allRule[C]{children: []Rule[C]{ix.root}}
		}
	}
	interner := newBitmapInterner()
	internRuleWith(interner, ix.root)
	for _, exclusion := range ix.exclusions {
		if rule, ok := exclusion.(bitmapInternable); ok {
			// Exclude nodes are no longer reachable from the positive tree.
			// Their value postings remain immutable and independently internable.
			rule.internBitmaps(interner)
		}
		if preparer, ok := exclusion.(ruleSearchPreparer); ok {
			preparer.prepareSearch()
		}
	}
	prepareRuleSearch(ix.root)
	ix.nodes = int(ids.next)
	return ix, statistics, nil
}

// Zip pairs equally sized constraint and ID slices into a sequence accepted by
// Builder.Build. It panics when the slice lengths differ. The slices are read
// when the returned sequence is consumed, not when Zip is called.
func Zip[C any, ID any](constraints []C, ids []ID) iter.Seq2[C, ID] {
	if len(constraints) != len(ids) {
		panic(fmt.Sprintf("ruleix: cannot zip %d constraints with %d IDs", len(constraints), len(ids)))
	}
	return func(yield func(C, ID) bool) {
		for i := range constraints {
			if !yield(constraints[i], ids[i]) {
				return
			}
		}
	}
}

// Search appends the unique IDs of every stored rule matching value to dst,
// reports whether this call found any matches, and updates the slice through
// its pointer if append allocates a larger backing array. Existing elements in
// dst do not affect the reported result. Results preserve first-insertion
// order. Search panics when dst is nil.
func (ix *Index[C, ID]) Search(value C, dst *[]ID) bool {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	return ix.search(value, dst, ix.pool)
}

// Local returns a search context that caches the two most recent intermediate
// bitmap results per filter node. It can reduce repeated work when adjacent
// searches share constraint values, at the cost of retaining those bitmaps for
// the lifetime of the Local.
//
// A Local is not safe for concurrent use. Create one per goroutine:
//
//	local := index.Local()
//	var matches []ID
//	for value := range values {
//		local.Search(value, &matches)
//	}
//
// The Index remains immutable and may be shared by all of those goroutines.
func (ix *Index[C, ID]) Local() *Local[C, ID] {
	return &Local[C, ID]{index: ix, pool: newLocalBitmapPool(ix.nodes)}
}

// Search appends matching IDs to dst while reusing this Local's cached state
// and reports whether this call found any matches. Existing elements in dst do
// not affect the reported result. Search panics when dst is nil.
func (local *Local[C, ID]) Search(value C, dst *[]ID) bool {
	if dst == nil {
		panic("ruleix: nil search destination")
	}
	return local.index.search(value, dst, local.pool)
}

// Reset releases all per-node cached search results while keeping the Local
// usable. Call it when a worker becomes idle or after an unusually broad query
// to return the context to its small cold-memory footprint.
func (local *Local[C, ID]) Reset() {
	local.pool = newLocalBitmapPool(local.index.nodes)
}

// Visit calls yield for matching IDs while reusing this Local's cached state.
// A nil yield function is a no-op.
func (local *Local[C, ID]) Visit(value C, yield func(ID) bool) {
	if yield == nil {
		return
	}
	visitMatches(local.index.root, local.index.values, local.pool, local.index.exclusions, value, yield)
}

// Visit calls yield once for each unique matching ID in first-match order.
// Iteration stops immediately when yield returns false. A nil yield function is
// a no-op.
func (ix *Index[C, ID]) Visit(value C, yield func(ID) bool) {
	if yield == nil {
		return
	}
	visitMatches(ix.root, ix.values, ix.pool, ix.exclusions, value, yield)
}

func (ix *Index[C, ID]) search(value C, dst *[]ID, pool *bitmapPool) bool {
	before := len(*dst)
	if root, ok := ix.root.(*allRule[C]); ok {
		searchAllMatches(root, ix.values, pool, ix.exclusions, value, dst)
		return len(*dst) != before
	}
	bits := pool.get()
	defer pool.put(bits)
	ix.root.search(value, bits, pool)
	if len(ix.exclusions) != 0 {
		excluded := pool.get()
		addExclusions(ix.exclusions, value, excluded, pool)
		bits.AndNot(excluded)
		pool.put(excluded)
	}
	*dst = appendBitmapValues(bits, ix.values, *dst)
	return len(*dst) != before
}

func searchAllMatches[C any, ID comparable](
	root *allRule[C],
	values []ID,
	pool *bitmapPool,
	exclusions []exclusionRule[C],
	value C,
	dst *[]ID,
) {
	result := *dst
	var inline [8]rankedBitmap
	var rankedChildren []rankedBitmap
	var buffer *rankedBitmapBuffer
	if len(root.children) > len(inline) {
		buffer = pool.getRanked(len(root.children))
		rankedChildren = buffer.items
	} else {
		rankedChildren = inline[:len(root.children)]
	}
	if !root.rankChildren(value, pool, rankedChildren) || len(rankedChildren) == 0 {
		if buffer != nil {
			pool.putRanked(buffer)
		}
		*dst = result
		return
	}

	if !prepareRankedAllCandidates(root, value, pool, rankedChildren) {
		root.releaseRanked(pool, rankedChildren)
		if buffer != nil {
			pool.putRanked(buffer)
		}
		*dst = result
		return
	}

	excluded := buildAllExclusions(exclusions, value, rankedChildren[0].card, pool)
	if rankedChildren[0].card > allCandidateScanLimit {
		result = appendBitmapAllMatches(rankedChildren, excluded, values, pool, result)
	} else {
		result = appendScannedAllMatches(root, rankedChildren, exclusions, excluded, value, values, pool, result)
	}
	if excluded != nil {
		pool.put(excluded)
	}
	root.releaseRanked(pool, rankedChildren)
	if buffer != nil {
		pool.putRanked(buffer)
	}
	*dst = result
}

func prepareRankedAllCandidates[C any](
	root *allRule[C],
	value C,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	if rankedChildren[0].card > allCandidateScanLimit {
		return root.collectRankedInOrder(value, pool, rankedChildren)
	}
	bits := pool.get()
	root.children[rankedChildren[0].childIdx].search(value, bits, pool)
	rankedChildren[0].bits = bits
	rankedChildren[0].card = bits.GetCardinality()
	return rankedChildren[0].card <= allCandidateScanLimit ||
		materializeRankedAfterFirst(root, value, pool, rankedChildren)
}

func materializeRankedAfterFirst[C any](
	root *allRule[C],
	value C,
	pool *bitmapPool,
	rankedChildren []rankedBitmap,
) bool {
	for i := 1; i < len(rankedChildren); i++ {
		bits := pool.get()
		root.children[rankedChildren[i].childIdx].search(value, bits, pool)
		rankedChildren[i].bits = bits
		rankedChildren[i].card = bits.GetCardinality()
		if bits.IsEmpty() {
			return false
		}
	}
	return true
}

func buildAllExclusions[C any](
	rules []exclusionRule[C],
	value C,
	candidates uint64,
	pool *bitmapPool,
) *roaring.Bitmap {
	// Direct exclusion checks only run in appendScannedAllMatches. Bitmap
	// execution always needs an exclusion bitmap, even when the candidate set
	// is below the otherwise profitable direct-lookup limit.
	direct := candidates <= allDirectExclusionScanLimit && candidates <= allCandidateScanLimit
	if len(rules) == 0 || direct {
		return nil
	}
	excluded := pool.get()
	addExclusions(rules, value, excluded, pool)
	return excluded
}

func appendBitmapAllMatches[ID comparable](
	rankedChildren []rankedBitmap,
	excluded *roaring.Bitmap,
	values []ID,
	pool *bitmapPool,
	result []ID,
) []ID {
	// FastAnd can return the final result directly here. The generic All search
	// cannot use it efficiently because it must copy that result into dst.
	var inline [8]*roaring.Bitmap
	if len(rankedChildren) > len(inline) {
		bits := pool.get()
		bits.Or(rankedChildren[0].bits)
		for _, child := range rankedChildren[1:] {
			if bits.IsEmpty() {
				break
			}
			bits.And(child.bits)
		}
		if excluded != nil {
			bits.AndNot(excluded)
		}
		result = appendBitmapValues(bits, values, result)
		pool.put(bits)
		return result
	}
	postings := inline[:len(rankedChildren)]
	for i := range rankedChildren {
		postings[i] = rankedChildren[i].bits
	}
	bits := roaring.FastAnd(postings...)
	if excluded != nil {
		bits.AndNot(excluded)
	}
	result = appendBitmapValues(bits, values, result)
	return result
}

// Below this size Iterate avoids an iterator allocation and its callback cost
// is lower than the batch setup. Wide results benefit substantially from
// decoding IDs in batches.
const manyIteratorCardinalityThreshold = 4 << 10

func appendBitmapValues[ID comparable](bits *roaring.Bitmap, values []ID, result []ID) []ID {
	if bits.GetCardinality() < manyIteratorCardinalityThreshold {
		bits.Iterate(func(id uint32) bool {
			result = append(result, values[id])
			return true
		})
		return result
	}

	iterator := bits.ManyIterator()
	var ids [256]uint32
	for count := iterator.NextMany(ids[:]); count != 0; count = iterator.NextMany(ids[:]) {
		for _, id := range ids[:count] {
			result = append(result, values[id])
		}
	}
	return result
}

func appendScannedAllMatches[C any, ID comparable](
	root *allRule[C],
	rankedChildren []rankedBitmap,
	exclusions []exclusionRule[C],
	excluded *roaring.Bitmap,
	value C,
	values []ID,
	pool *bitmapPool,
	result []ID,
) []ID {
	candidates := rankedChildren[0].bits.Iterator()
	for candidates.HasNext() {
		id := candidates.Next()
		if excluded != nil && excluded.Contains(id) || excluded == nil && isExcluded(exclusions, value, id) {
			continue
		}
		matches := true
		for _, child := range rankedChildren[1:] {
			if !matchesRuleID(root.children[child.childIdx], value, id, pool) {
				matches = false
				break
			}
		}
		if matches {
			result = append(result, values[id])
		}
	}
	return result
}

func visitMatches[C any, ID comparable](
	root Rule[C],
	values []ID,
	pool *bitmapPool,
	exclusions []exclusionRule[C],
	value C,
	yield func(ID) bool,
) {
	bits := pool.get()
	defer pool.put(bits)
	root.search(value, bits, pool)
	if len(exclusions) != 0 {
		excluded := pool.get()
		addExclusions(exclusions, value, excluded, pool)
		bits.AndNot(excluded)
		pool.put(excluded)
	}
	bits.Iterate(func(id uint32) bool { return yield(values[id]) })
}

func addExclusions[C any](rules []exclusionRule[C], value C, dst *roaring.Bitmap, pool *bitmapPool) {
	for _, rule := range rules {
		rule.exclude(value, dst, pool)
	}
}

func isExcluded[C any](rules []exclusionRule[C], value C, id uint32) bool {
	for _, rule := range rules {
		if rule.isExcluded(value, id) {
			return true
		}
	}
	return false
}
