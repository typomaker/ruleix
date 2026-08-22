package ruleix

import (
	"fmt"
	"hash/fnv"
	"math"

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

func compileLossyRules[T any](rule Rule[T], inside bool) (Rule[T], error) {
	switch typed := rule.(type) {
	case *lossyRule[T]:
		if inside {
			return nil, fmt.Errorf("ruleix: nested Lossy policies are not supported")
		}
		if err := typed.validatePolicy(); err != nil {
			return nil, err
		}
		if _, ok := typed.child.(*allRule[T]); ok {
			return nil, fmt.Errorf("ruleix: Lossy cannot directly decorate All")
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
	encoded, ok := canonicalScalar(nil, value)
	if !ok {
		return 0, false
	}
	h := fnv.New64a()
	_, _ = h.Write(encoded)
	return h.Sum64(), true
}

type lossyEqualityRule[T any, V comparable] struct {
	nodeID   nodeID
	get      Getter[T, V]
	wildcard *roaring.Bitmap
	shift    uint
	buckets  map[uint64]*roaring.Bitmap
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
func (r *lossyEqualityRule[T, V]) cardinality(v T, p *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, p)
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

func (*lossyOrderedRule[T, V]) rule()                                                 {}
func (r *lossyOrderedRule[T, V]) newState(*nodeIDAllocator, *buildStatistics) Rule[T] { return r }
func (*lossyOrderedRule[T, V]) validate(T) error                                      { return nil }
func (*lossyOrderedRule[T, V]) insert(T, uint32)                                      {}
func (r *lossyOrderedRule[T, V]) search(v T, dst *roaring.Bitmap, _ *bitmapPool) {
	dst.Or(r.wildcard)
	value, ok := r.get(v)
	if !ok || len(r.buckets) == 0 {
		return
	}
	key, ok := orderedScalarKey(any(value))
	if !ok {
		return
	}
	if r.dir == greaterThan {
		if key < r.min || (!r.inclusive && key == r.min) {
			return
		}
		last := uint64(len(r.buckets) - 1)
		if key < r.max {
			last = (key - r.min) / r.width
		}
		for n := uint64(0); n <= last; n++ {
			if b := r.buckets[n]; b != nil {
				dst.Or(b)
			}
		}
		return
	}
	if key > r.max || (!r.inclusive && key == r.max) {
		return
	}
	first := uint64(0)
	if key > r.min {
		first = (key - r.min) / r.width
	}
	for n := first; n < uint64(len(r.buckets)); n++ {
		if b := r.buckets[n]; b != nil {
			dst.Or(b)
		}
	}
}
func (r *lossyOrderedRule[T, V]) cardinality(v T, p *bitmapPool) uint64 {
	return measuredCardinality[T](r, v, p)
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
