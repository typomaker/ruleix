package ruleix

import (
	"fmt"
	"iter"
	"math"
	"sync"
	"sync/atomic"

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
	root               Rule[C]
	observedRoot       Rule[C]
	rootMetrics        *inspectorRuntime
	values             []ID
	pool               *bitmapPool
	nodes              int
	exclusions         []exclusionRule[C]
	observedExclusions []exclusionRule[C]
	locals             sync.Pool
	observedLocals     sync.Pool
	localTelemetry     atomic.Uint64
	localInspectors    [][]*inspectorRuntime
}

// Local is a search context that keeps per-node cached results between calls.
// It must not be used concurrently by multiple goroutines.
type Local[C any, ID comparable] struct {
	index    *Index[C, ID]
	pool     *bitmapPool
	closed   bool
	observed bool
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
	return buildIndexPhysicalAliases(schema, entries, collectStatistics, hints, buildOptions{
		compilePhysicalAliases: true,
		compileStrictAntonyms:  true,
		// Lossy policies must bound their build state while consuming the one-pass
		// iterator. Compiled leaves accept later values directly, so this does not
		// add a wrapper or another operation to the published search path.
		enableStreaming: true,
	})
}

// buildOptions is intentionally internal. It lets correctness and benchmark
// fixtures compare the one-pass streaming policy with exact-first planning
// without expanding the public build contract.
type buildOptions struct {
	compilePhysicalAliases bool
	compileStrictAntonyms  bool
	enableStreaming        bool
	// identityLossy selects the exact-key head of every Lossy representation
	// ladder after normal policy analysis. It is a test/benchmark control, not
	// a public memory-limit mode.
	identityLossy       bool
	observeWorkingUsage func(usage, target uint64)
}

//nolint:gocognit // The build pipeline is intentionally kept in one linear ownership scope.
func buildIndexPhysicalAliases[C any, ID comparable](
	schema Rule[C],
	entries iter.Seq2[C, ID],
	collectStatistics bool,
	hints *buildStatistics,
	options buildOptions,
) (*Index[C, ID], buildStatistics, error) {
	if entries == nil {
		return nil, buildStatistics{}, fmt.Errorf("ruleix: nil entry sequence")
	}
	ids := &nodeIDAllocator{}
	state := schema.newState(ids, hints)
	if err := validateLossyPolicies(state, "Lossy"); err != nil {
		return nil, buildStatistics{}, err
	}
	uniqueIDCapacity := 0
	if hints != nil {
		uniqueIDCapacity = capacityHint(hints.uniqueIDs)
	}
	values := make([]ID, 0, uniqueIDCapacity)
	internalIDs := make(map[ID]uint32, uniqueIDCapacity)
	var buildErr error
	entryIndex := 0
	streaming := false
	entries(func(constraint C, id ID) bool {
		if uint64(len(values)) > math.MaxUint32 {
			buildErr = fmt.Errorf("ruleix: at most 2^32 rules are supported")
			return false
		}
		if err := state.validate(constraint); err != nil {
			buildErr = fmt.Errorf("ruleix: entry %d: %w", entryIndex, err)
			return false
		}
		internalID, exists := internalIDs[id]
		if !exists {
			internalID = uint32(len(values))
			internalIDs[id] = internalID
			values = append(values, id)
		}
		state.insert(constraint, internalID)
		entryIndex++
		if options.enableStreaming && !options.identityLossy && entryIndex%lossyBuildPressureInterval == 0 {
			var usage, target uint64
			var hasTarget bool
			if streaming {
				usage, target, hasTarget = lossyStreamingBuildPressure(state)
			} else {
				var err error
				usage, target, hasTarget, err = lossyBuildPressure(state)
				if err != nil {
					buildErr = err
					return false
				}
			}
			if hasTarget && options.observeWorkingUsage != nil {
				options.observeWorkingUsage(usage, target)
			}
			if hasTarget && usage > target {
				if streaming {
					fitStreamingPolicies(state)
				} else {
					var err error
					state, err = compileLossyRules(state, false)
					if err != nil {
						buildErr = err
						return false
					}
					state = wrapStreamingLossyLeaves(state)
					streaming = true
				}
			}
		}
		return true
	})
	if buildErr != nil {
		return nil, buildStatistics{}, buildErr
	}
	if !streaming && options.observeWorkingUsage != nil {
		usage, target, hasTarget, pressureErr := lossyBuildPressure(state)
		if pressureErr != nil {
			return nil, buildStatistics{}, pressureErr
		}
		if hasTarget {
			options.observeWorkingUsage(usage, target)
		}
	}
	ix := &Index[C, ID]{root: state, values: values, pool: newBitmapPool()}
	var err error
	ix.root, err = compileLossyRules(ix.root, options.identityLossy)
	if err != nil {
		return nil, buildStatistics{}, err
	}
	ix.root, _, err = refreshStreamingLossyDetails(ix.root)
	if err != nil {
		return nil, buildStatistics{}, err
	}
	ix.root = unwrapStreamingAdaptiveLeaves(ix.root)
	statistics := buildStatistics{entries: entryIndex, uniqueIDs: len(ix.values)}
	if collectStatistics {
		statistics.nodes = make([]nodeBuildStatistics, int(ids.next))
		ix.root.collectBuildStatistics(statistics.nodes)
	}
	internalCount := uint64(len(ix.values))
	ix.root = optimizeRule(ix.root, internalCount)
	var inspections []pendingInspection
	ix.root, err = stripInspectors(ix.root, make(map[*inspectorState]struct{}), &inspections)
	if err != nil {
		return nil, buildStatistics{}, err
	}
	if options.compilePhysicalAliases {
		ix.root = compileAllPhysicalOperands(ix.root)
	}
	// Removing transparent decorators can expose All simplifications that were
	// intentionally hidden while retaining the inspector-to-child association.
	ix.root = optimizeRule(ix.root, internalCount)
	ix.exclusions = collectExclusionRules(ix.root, nil)
	if len(ix.exclusions) != 0 {
		universe := roaring.New()
		universe.AddRange(0, uint64(len(ix.values)))
		ix.root = removeExclusionRules(ix.root, universe)
		// Keep the specialized All search path even when removing exclusions
		// leaves one positive child. It can test small candidate sets directly
		// without materializing a separate exclusion bitmap.
		_, directAll := ix.root.(*allRule[C])
		if observed, ok := ix.root.(*inspectedRuntimeRule[C]); ok {
			_, directAll = observed.child.(*allRule[C])
		}
		if !directAll {
			ix.root = &allRule[C]{children: []Rule[C]{ix.root}}
		}
	}
	if observed, ok := ix.root.(*inspectedRuntimeRule[C]); ok {
		ix.rootMetrics = observed.metrics
		ix.root = observed.child
	}
	ix.observedRoot, ix.observedExclusions = ix.root, ix.exclusions
	if len(inspections) != 0 {
		ix.root = removeRuntimeInspectors(ix.root)
		ix.exclusions = removeRuntimeExclusionInspectors(ix.exclusions)
	}
	ix.pool.observeRuntime = false
	interner := newBitmapInterner()
	internRuleWith(interner, ix.observedRoot)
	if options.compilePhysicalAliases {
		compileAllEqualityClasses(ix.observedRoot)
	}
	if options.compileStrictAntonyms {
		ix.root = compileStrictEqualityAntonyms(ix.root)
	}
	for _, exclusion := range ix.observedExclusions {
		if rule, ok := exclusion.(bitmapInternable); ok {
			// Exclude nodes are no longer reachable from the positive tree.
			// Their value postings remain immutable and independently internable.
			rule.internBitmaps(interner)
		}
		if preparer, ok := exclusion.(ruleSearchPreparer); ok {
			preparer.prepareSearch()
		}
	}
	prepareRuleSearch(ix.observedRoot)
	prepareRuleSearch(ix.root)
	ix.nodes = int(ids.next)
	for _, inspection := range inspections {
		for _, id := range inspection.nodes {
			if ix.localInspectors == nil {
				ix.localInspectors = make([][]*inspectorRuntime, ix.nodes)
			}
			ix.localInspectors[int(id)] = append(ix.localInspectors[int(id)], &inspection.dst.runtime)
		}
		inspection.dst.published.Store(&inspectorSnapshotBox{snapshot: exactInspectorSnapshot{
			strategyName: inspection.strategy,
			modeName:     inspection.mode,
			entries:      uint64(statistics.entries),
			rules:        uint64(statistics.uniqueIDs),
			detail:       inspection.details,
		}})
	}
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
