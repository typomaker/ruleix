package ruleix

import (
	"fmt"
	"math"
	"math/bits"
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

// LossyOption configures a memory-bounded representation selected during Build.
// Implementations are sealed so new options can be added without weakening
// validation of a schema.
type LossyOption interface{ lossyOption() }

type memoryLimitOption struct{ bytes uint64 }

func (memoryLimitOption) lossyOption() {}

// MemoryLimit sets the maximum accounted bytes retained by one Lossy rule.
func MemoryLimit(bytes uint64) LossyOption { return memoryLimitOption{bytes: bytes} }

// Lossy permits rule to use a conservative approximation when its exact
// representation does not fit the configured memory limit. Approximate
// searches can return false positives, but never omit an exact match.
func Lossy[T any](rule Rule[T], options ...LossyOption) Rule[T] {
	if rule == nil {
		panic("ruleix: nil lossy rule")
	}
	return &lossyRule[T]{child: rule, options: options}
}

type lossyRule[T any] struct {
	child   Rule[T]
	options []LossyOption
	limit   uint64
}

func (*lossyRule[T]) rule() {}
func (r *lossyRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &lossyRule[T]{child: r.child.newState(ids, hints), options: r.options, limit: r.limit}
}
func (r *lossyRule[T]) validate(v T) error {
	if err := r.validatePolicy(); err != nil {
		return err
	}
	return r.child.validate(v)
}
func (r *lossyRule[T]) validatePolicy() error {
	if r.limit != 0 {
		return nil
	}
	for _, option := range r.options {
		value, ok := option.(memoryLimitOption)
		if !ok {
			return fmt.Errorf("ruleix: invalid Lossy option")
		}
		if r.limit != 0 {
			return fmt.Errorf("ruleix: Lossy requires exactly one MemoryLimit")
		}
		r.limit = value.bytes
	}
	if r.limit == 0 {
		return fmt.Errorf("ruleix: Lossy requires one non-zero MemoryLimit")
	}
	return nil
}
func (r *lossyRule[T]) insert(v T, id uint32)                           { r.child.insert(v, id) }
func (r *lossyRule[T]) cardinality(v T, p *bitmapPool) uint64           { return r.child.cardinality(v, p) }
func (r *lossyRule[T]) search(v T, dst *roaring.Bitmap, p *bitmapPool)  { r.child.search(v, dst, p) }
func (r *lossyRule[T]) exclude(v T, dst *roaring.Bitmap, p *bitmapPool) { r.child.exclude(v, dst, p) }
func (r *lossyRule[T]) collectBuildStatistics(s []nodeBuildStatistics) {
	r.child.collectBuildStatistics(s)
}

type lossyCompiler[T any] interface{ compileLossy(uint64) (Rule[T], error) }

type lossyAllPlanner[T any] interface{ compile(uint64) (Rule[T], error) }

type lossyAllCompiler[T any] interface{ newLossyAllPlanner() lossyAllPlanner[T] }

type defaultLossyAllPlanner[T any] struct{ compiler lossyCompiler[T] }

func (p defaultLossyAllPlanner[T]) compile(limit uint64) (Rule[T], error) {
	return p.compiler.compileLossy(limit)
}

type inspectionDetailsRule[T any] struct {
	child   Rule[T]
	details inspectionDetails
}

func (*inspectionDetailsRule[T]) rule() {}
func (r *inspectionDetailsRule[T]) newState(ids *nodeIDAllocator, hints *buildStatistics) Rule[T] {
	return &inspectionDetailsRule[T]{child: r.child.newState(ids, hints), details: r.details}
}
func (r *inspectionDetailsRule[T]) validate(v T) error    { return r.child.validate(v) }
func (r *inspectionDetailsRule[T]) insert(v T, id uint32) { r.child.insert(v, id) }
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

//nolint:gocognit // Recursive policy validation is clearest as one exhaustive type switch.
func compileLossyRules[T any](rule Rule[T], inside bool) (Rule[T], error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		if inside {
			return nil, fmt.Errorf("ruleix: nested Lossy policies are not supported")
		}
		if err := typed.validatePolicy(); err != nil {
			return nil, err
		}
		if _, ok := typed.child.(*lossyRule[T]); ok {
			return nil, fmt.Errorf("ruleix: nested Lossy policies are not supported")
		}
		child := typed.child
		var inspected *inspectRule[T]
		if value, ok := child.(*inspectRule[T]); ok {
			inspected = value
			child = value.child
		}
		if composite, ok := child.(*allRule[T]); ok {
			compiled, details, err := compileLossyAll(composite, typed.limit)
			if err != nil {
				return nil, err
			}
			wrapped := Rule[T](&inspectionDetailsRule[T]{child: compiled, details: details})
			if inspected != nil {
				return &inspectRule[T]{dst: inspected.dst, child: wrapped}, nil
			}
			return wrapped, nil
		}
		compiler, ok := child.(lossyCompiler[T])
		if !ok {
			return nil, fmt.Errorf("ruleix: Lossy does not support this rule representation")
		}
		compiled, err := compiler.compileLossy(typed.limit)
		if err != nil {
			return nil, err
		}
		details := lossyinspectionDetails(compiled, typed.limit)
		if inspected != nil {
			return &inspectRule[T]{dst: inspected.dst, child: &inspectionDetailsRule[T]{child: compiled, details: details}}, nil
		}
		return &inspectionDetailsRule[T]{child: compiled, details: details}, nil
	case *allRule[T]:
		children := make([]Rule[T], len(typed.children))
		for i, child := range typed.children {
			compiled, err := compileLossyRules(child, inside)
			if err != nil {
				return nil, err
			}
			children[i] = compiled
		}
		return &allRule[T]{children: children}, nil
	case *inspectRule[T]:
		child, err := compileLossyRules(typed.child, inside)
		if err != nil {
			return nil, err
		}
		return &inspectRule[T]{dst: typed.dst, child: child}, nil
	default:
		return rule, nil
	}
}

type lossyAllLeaf[T any] struct {
	planner      lossyAllPlanner[T]
	exact        uint64
	minimum      uint64
	limit        uint64
	compiled     Rule[T]
	upgradeLimit uint64
	upgradeCost  uint64
	upgrade      Rule[T]
	upgradeKnown bool
}

// compileLossyAll treats the composite limit as a pool. Every leaf first gets
// enough bytes for its smallest viable representation, then the remaining
// bytes are divided proportionally. Representation granularity can leave part
// of a share unused, so those bytes are reclaimed for deterministic upgrades
// of other leaves.
func compileLossyAll[T any](rule *allRule[T], limit uint64) (*allRule[T], inspectionDetails, error) {
	var leaves []lossyAllLeaf[T]
	if err := collectLossyAllLeaves[T](rule, &leaves); err != nil {
		return nil, inspectionDetails{}, err
	}
	var total uint64
	for _, leaf := range leaves {
		if math.MaxUint64-total < leaf.exact {
			return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All memory accounting overflow")
		}
		total += leaf.exact
	}
	if total <= limit {
		for i := range leaves {
			leaves[i].limit = leaves[i].exact
			leaves[i].compiled, _ = leaves[i].planner.compile(leaves[i].limit)
		}
	} else if total != 0 {
		var minimumTotal uint64
		for i := range leaves {
			minimum, compiled, err := minimumLossyAllLimit(leaves[i].planner, leaves[i].exact)
			if err != nil {
				return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All child %d: %w", i+1, err)
			}
			leaves[i].minimum, leaves[i].limit, leaves[i].compiled = minimum, minimum, compiled
			if math.MaxUint64-minimumTotal < minimum {
				return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All memory accounting overflow")
			}
			minimumTotal += minimum
		}
		if minimumTotal > limit {
			return nil, inspectionDetails{}, fmt.Errorf("ruleix: Lossy All cannot fit the memory limit")
		}

		type remainder struct {
			index int
			value uint64
		}
		remainders := make([]remainder, len(leaves))
		available := limit - minimumTotal
		var headroomTotal, allocated uint64
		for i := range leaves {
			headroomTotal += leaves[i].exact - leaves[i].minimum
		}
		for i := range leaves {
			hi, lo := bits.Mul64(available, leaves[i].exact-leaves[i].minimum)
			quotient, rem := bits.Div64(hi, lo, headroomTotal)
			leaves[i].limit += quotient
			allocated += quotient
			remainders[i] = remainder{index: i, value: rem}
		}
		sort.SliceStable(remainders, func(i, j int) bool { return remainders[i].value > remainders[j].value })
		for i := uint64(0); i < available-allocated; i++ {
			leaves[remainders[i].index].limit++
		}
		var used uint64
		for i := range leaves {
			leaves[i].compiled, _ = leaves[i].planner.compile(leaves[i].limit)
			used += inspectionDetailsOf(leaves[i].compiled).MemoryUsageBytes
		}
		redistributeLossyAllBudget(leaves, limit-used)
	}
	index := 0
	compiled, details, err := materializeLossyAll[T](rule, leaves, &index)
	if err != nil {
		return nil, inspectionDetails{}, err
	}
	details.MemoryLimitBytes, details.MemoryLimitAvailable = limit, true
	return compiled.(*allRule[T]), details, nil
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
		compiler, ok := rule.(lossyCompiler[T])
		if !ok {
			return fmt.Errorf("ruleix: Lossy All does not support a child rule representation")
		}
		planner := lossyAllPlanner[T](defaultLossyAllPlanner[T]{compiler: compiler})
		if factory, ok := rule.(lossyAllCompiler[T]); ok {
			planner = factory.newLossyAllPlanner()
		}
		exact, err := planner.compile(math.MaxUint64)
		if err != nil {
			return err
		}
		details := inspectionDetailsOf(exact)
		if !details.MemoryUsageAvailable {
			return fmt.Errorf("ruleix: Lossy All child has no memory accounting")
		}
		*leaves = append(*leaves, lossyAllLeaf[T]{planner: planner, exact: details.MemoryUsageBytes})
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
	low, high := uint64(0), exact
	compiled, err := planner.compile(high)
	if err != nil {
		return 0, nil, err
	}
	for low < high {
		mid := low + (high-low)/2
		candidate, candidateErr := planner.compile(mid)
		if candidateErr != nil {
			low = mid + 1
			continue
		}
		high, compiled = mid, candidate
	}
	return low, compiled, nil
}

func redistributeLossyAllBudget[T any](leaves []lossyAllLeaf[T], available uint64) {
	for available != 0 {
		best := -1
		var bestCost uint64
		for i := range leaves {
			usage := inspectionDetailsOf(leaves[i].compiled).MemoryUsageBytes
			if usage >= leaves[i].exact {
				continue
			}
			prepareLossyAllUpgrade(&leaves[i], usage)
			cost := leaves[i].upgradeCost
			if cost != 0 && cost <= available && (best < 0 || cost < bestCost) {
				best, bestCost = i, cost
			}
		}
		if best < 0 {
			return
		}
		leaves[best].limit = leaves[best].upgradeLimit
		leaves[best].compiled = leaves[best].upgrade
		leaves[best].upgrade = nil
		leaves[best].upgradeCost = 0
		leaves[best].upgradeKnown = false
		available -= bestCost
	}
}

func prepareLossyAllUpgrade[T any](leaf *lossyAllLeaf[T], usage uint64) {
	if leaf.upgradeKnown {
		return
	}
	leaf.upgradeKnown = true
	low, high := usage+1, leaf.exact
	for low < high {
		mid := low + (high-low)/2
		candidate, err := leaf.planner.compile(mid)
		if err != nil || inspectionDetailsOf(candidate).MemoryUsageBytes <= usage {
			low = mid + 1
		} else {
			high = mid
		}
	}
	candidate, err := leaf.planner.compile(low)
	if err != nil {
		return
	}
	candidateUsage := inspectionDetailsOf(candidate).MemoryUsageBytes
	if candidateUsage <= usage {
		return
	}
	leaf.upgradeLimit = low
	leaf.upgradeCost = candidateUsage - usage
	leaf.upgrade = candidate
}

func lossyinspectionDetails[T any](rule Rule[T], limit uint64) inspectionDetails {
	d := inspectionDetailsOf(rule)
	d.MemoryLimitBytes, d.MemoryLimitAvailable = limit, true
	return d
}

const lossyMaxBucketBits = 16

func bitmapBytes(bits *roaring.Bitmap) uint64 {
	if bits == nil {
		return 0
	}
	return bits.GetSerializedSizeInBytes()
}

func hashScalar(value any) (uint64, bool) {
	switch value := value.(type) {
	case bool:
		encoded := byte(0)
		if value {
			encoded = 1
		}
		return fnvHashByte(fnvHashByte(fnvOffset64, canonicalBool), encoded), true
	case string:
		hash := fnvHashUint64(fnvHashByte(fnvOffset64, canonicalString), uint64(len(value)))
		for index := range len(value) {
			hash = fnvHashByte(hash, value[index])
		}
		return hash, true
	case int:
		return fnvHashTaggedUint64(canonicalInt, uint64(int64(value))), true
	case int8:
		return fnvHashTaggedUint64(canonicalInt8, uint64(value)), true
	case int16:
		return fnvHashTaggedUint64(canonicalInt16, uint64(value)), true
	case int32:
		return fnvHashTaggedUint64(canonicalInt32, uint64(value)), true
	case int64:
		return fnvHashTaggedUint64(canonicalInt64, uint64(value)), true
	case uint:
		return fnvHashTaggedUint64(canonicalUint, uint64(value)), true
	case uint8:
		return fnvHashTaggedUint64(canonicalUint8, uint64(value)), true
	case uint16:
		return fnvHashTaggedUint64(canonicalUint16, uint64(value)), true
	case uint32:
		return fnvHashTaggedUint64(canonicalUint32, uint64(value)), true
	case uint64:
		return fnvHashTaggedUint64(canonicalUint64, value), true
	case uintptr:
		return fnvHashTaggedUint64(canonicalUintptr, uint64(value)), true
	case float32:
		return fnvHashTaggedUint64(canonicalFloat32, uint64(canonicalFloat32Bits(value))), true
	case float64:
		return fnvHashTaggedUint64(canonicalFloat64, canonicalFloat64Bits(value)), true
	default:
		return 0, false
	}
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

func fnvHashByte(hash uint64, value byte) uint64 {
	return (hash ^ uint64(value)) * fnvPrime64
}

func fnvHashUint64(hash, value uint64) uint64 {
	for shift := 56; shift >= 0; shift -= 8 {
		hash = fnvHashByte(hash, byte(value>>shift))
	}
	return hash
}

func fnvHashTaggedUint64(tag byte, value uint64) uint64 {
	return fnvHashUint64(fnvHashByte(fnvOffset64, tag), value)
}

type lossyEqualityRule[T any, V comparable] struct {
	nodeID   nodeID
	get      Getter[T, V]
	wildcard *roaring.Bitmap
	shift    uint
	buckets  map[uint64]*roaring.Bitmap
}

func (r *lossyEqualityRule[T, V]) lookupPlanningBitmap(v T) (*roaring.Bitmap, bool) {
	// A wildcard requires a union with the concrete bucket, so it cannot expose
	// one of its owned bitmaps as the complete child result.
	if !r.wildcard.IsEmpty() {
		return nil, false
	}
	value, ok := r.get(v)
	if !ok {
		return r.wildcard, true
	}
	hash, ok := hashScalar(any(value))
	if !ok {
		return r.wildcard, true
	}
	bits := r.buckets[hash>>r.shift]
	if bits == nil {
		return r.wildcard, true
	}
	return bits, true
}

func (*lossyEqualityRule[T, V]) rule()                                                 {}
func (r *lossyEqualityRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyEqualityRule[T, V]) validate(T) error                                      { return nil }
func (*lossyEqualityRule[T, V]) insert(T, uint32)                                      {}
func (r *lossyEqualityRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	value, ok := r.get(v)
	if !ok {
		return
	}
	hash, ok := hashScalar(any(value))
	if !ok {
		return
	}
	if bits := r.buckets[hash>>r.shift]; bits != nil {
		dst.Or(bits)
	}
}
func (r *lossyEqualityRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	value, ok := r.get(v)
	if !ok {
		return n
	}
	hash, ok := hashScalar(any(value))
	if !ok {
		return n
	}
	if bits := r.buckets[hash>>r.shift]; bits != nil {
		n += bits.GetCardinality()
	}
	return n
}
func (r *lossyEqualityRule[T, V]) estimateCheapCardinality(v T) uint64 {
	return r.estimateCardinality(v)
}
func (r *lossyEqualityRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyEqualityRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	value, ok := r.get(v)
	if !ok {
		return false
	}
	hash, ok := hashScalar(any(value))
	if !ok {
		return false
	}
	bits := r.buckets[hash>>r.shift]
	return bits != nil && bits.Contains(id)
}
func (r *lossyEqualityRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*lossyEqualityRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyEqualityRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyEqualityRule[T, V]) inspectionStrategy() string                   { return "lossy-grouped-hash" }
func (*lossyEqualityRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyEqualityRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, b := range r.buckets {
		prepareBitmapForSearch(b)
	}
}
func (r *lossyEqualityRule[T, V]) internBitmaps(i *bitmapInterner) {
	i.intern(&r.wildcard)
	for k, b := range r.buckets {
		i.intern(&b)
		r.buckets[k] = b
	}
}

type lossyOrderedRule[T any, V any] struct {
	nodeID          nodeID
	get             Getter[T, V]
	dir             direction
	inclusive       bool
	wildcard        *roaring.Bitmap
	min, max, width uint64
	buckets         []*roaring.Bitmap
}

func lossyOrderedBucket(key, minimum, width, count uint64) uint64 {
	bucket := (key - minimum) / width
	if bucket >= count {
		return count - 1
	}
	return bucket
}

func (*lossyOrderedRule[T, V]) rule()                                                 {}
func (r *lossyOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyOrderedRule[T, V]) validate(T) error                                      { return nil }
func (*lossyOrderedRule[T, V]) insert(T, uint32)                                      {}
func (r *lossyOrderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return
	}
	for n := first; n <= last; n++ {
		if b := r.buckets[n]; b != nil {
			dst.Or(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) matchingBucketRange(v T) (uint64, uint64, bool) {
	value, ok := r.get(v)
	if !ok || len(r.buckets) == 0 {
		return 0, 0, false
	}
	key, ok := orderedScalarKey(any(value))
	if !ok {
		return 0, 0, false
	}
	last := uint64(len(r.buckets) - 1)
	if r.dir == greaterThan {
		if key < r.min || (!r.inclusive && key == r.min) {
			return 0, 0, false
		}
		if key < r.max {
			last = lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
		}
		return 0, last, true
	}
	if key > r.max || (!r.inclusive && key == r.max) {
		return 0, 0, false
	}
	first := uint64(0)
	if key > r.min {
		first = lossyOrderedBucket(key, r.min, r.width, uint64(len(r.buckets)))
	}
	return first, last, true
}
func (r *lossyOrderedRule[T, V]) estimateCardinality(v T) uint64 {
	n := r.wildcard.GetCardinality()
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return n
	}
	for bucket := first; bucket <= last; bucket++ {
		if bits := r.buckets[bucket]; bits != nil {
			n += bits.GetCardinality()
		}
	}
	return n
}
func (r *lossyOrderedRule[T, V]) isCardinalityZero(v T) bool {
	return r.estimateCardinality(v) == 0
}
func (r *lossyOrderedRule[T, V]) matchesID(v T, id uint32) bool {
	if r.wildcard.Contains(id) {
		return true
	}
	first, last, ok := r.matchingBucketRange(v)
	if !ok {
		return false
	}
	for bucket := first; bucket <= last; bucket++ {
		if bits := r.buckets[bucket]; bits != nil && bits.Contains(id) {
			return true
		}
	}
	return false
}
func (r *lossyOrderedRule[T, V]) cardinality(v T, _ *bitmapPool) uint64 {
	return r.estimateCardinality(v)
}
func (*lossyOrderedRule[T, V]) exclude(T, *roaring.Bitmap, *bitmapPool)      {}
func (*lossyOrderedRule[T, V]) collectBuildStatistics([]nodeBuildStatistics) {}
func (*lossyOrderedRule[T, V]) inspectionStrategy() string                   { return "lossy-ordered-buckets" }
func (*lossyOrderedRule[T, V]) inspectionMode() RuleMode                     { return RuleModeLossy }
func (r *lossyOrderedRule[T, V]) prepareSearch() {
	prepareBitmapForSearch(r.wildcard)
	for _, b := range r.buckets {
		if b != nil {
			prepareBitmapForSearch(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) internBitmaps(i *bitmapInterner) {
	i.intern(&r.wildcard)
	for n, b := range r.buckets {
		if b != nil {
			i.intern(&b)
			r.buckets[n] = b
		}
	}
}

func addAccounting(total, add uint64) (uint64, bool) {
	if math.MaxUint64-total < add {
		return 0, false
	}
	return total + add, true
}
