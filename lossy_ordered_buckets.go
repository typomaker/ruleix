package ruleix

import (
	"sort"

	"github.com/RoaringBitmap/roaring/v2"
)

type lossyComparedBuckets[V any] struct {
	compare    Compare[V]
	minimum    V
	maximum    V
	capacity   int
	boundaries []V
	buckets    []*roaring.Bitmap
}

func cloneLossyComparedBuckets[V any](source lossyComparedBuckets[V]) lossyComparedBuckets[V] {
	clone := source
	clone.boundaries = append([]V(nil), source.boundaries...)
	clone.buckets = make([]*roaring.Bitmap, len(source.buckets))
	for i, bucket := range source.buckets {
		clone.buckets[i] = bucket.Clone()
	}
	return clone
}

func (r *lossyComparedBuckets[V]) insert(value V, id uint32) {
	if len(r.buckets) == 0 {
		r.minimum, r.maximum = value, value
		r.capacity = max(r.capacity, 1)
		r.boundaries = []V{value}
		r.buckets = []*roaring.Bitmap{roaring.BitmapOf(id)}
		return
	}
	if r.compare(value, r.minimum) < 0 {
		r.minimum = value
		r.boundaries = append([]V{value}, r.boundaries...)
		r.buckets = append([]*roaring.Bitmap{roaring.BitmapOf(id)}, r.buckets...)
		r.fitCapacity()
		return
	}
	if r.compare(value, r.maximum) > 0 {
		r.maximum = value
		r.boundaries = append(r.boundaries, value)
		r.buckets = append(r.buckets, roaring.BitmapOf(id))
		r.fitCapacity()
		return
	}
	bucket := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	r.buckets[bucket].Add(id)
}

func (r *lossyComparedBuckets[V]) fitCapacity() {
	for len(r.buckets) > max(r.capacity, 1) {
		r.coarsenOne()
	}
}

// coarsenOne merges the least-populated adjacent pair. Comparator-backed
// values have no arithmetic distance, so cardinality is the only stable
// build-time signal available without retaining the original exact values.
func (r *lossyComparedBuckets[V]) coarsenOne() {
	if len(r.buckets) <= 1 {
		return
	}
	merge := 0
	best := r.buckets[0].GetCardinality() + r.buckets[1].GetCardinality()
	for i := 1; i+1 < len(r.buckets); i++ {
		cardinality := r.buckets[i].GetCardinality() + r.buckets[i+1].GetCardinality()
		if cardinality < best {
			merge, best = i, cardinality
		}
	}
	r.buckets[merge].Or(r.buckets[merge+1])
	r.boundaries[merge] = r.boundaries[merge+1]
	copy(r.buckets[merge+1:], r.buckets[merge+2:])
	copy(r.boundaries[merge+1:], r.boundaries[merge+2:])
	r.buckets = r.buckets[:len(r.buckets)-1]
	r.boundaries = r.boundaries[:len(r.boundaries)-1]
}

func buildLossyComparedBuckets[V any](index *orderedIndex[V], wanted int) lossyComparedBuckets[V] {
	result := lossyComparedBuckets[V]{compare: index.compare, capacity: max(wanted, 1)}
	values := make([]*orderedItem[V], 0, index.buildStatistics().uniqueValues)
	for block := range index.blocks {
		values = append(values, index.blocks[block].items...)
	}
	if len(values) == 0 {
		return result
	}
	result.minimum, result.maximum = values[0].value, values[len(values)-1].value
	count := min(max(wanted, 1), len(values))
	width := (len(values) + count - 1) / count
	result.boundaries = make([]V, 0, count)
	result.buckets = make([]*roaring.Bitmap, 0, count)
	for first := 0; first < len(values); first += width {
		last := min(first+width, len(values))
		bits := roaring.New()
		for _, value := range values[first:last] {
			bits.Or(value.bits)
		}
		result.boundaries = append(result.boundaries, values[last-1].value)
		result.buckets = append(result.buckets, bits)
	}
	return result
}

func (r *lossyComparedBuckets[V]) matchingRange(value V, dir direction, inclusive bool) (int, int, bool) {
	if len(r.buckets) == 0 {
		return 0, 0, false
	}
	last := len(r.buckets) - 1
	if dir == greaterThan {
		minimum := r.compare(value, r.minimum)
		if minimum < 0 || (!inclusive && minimum == 0) {
			return 0, 0, false
		}
		if r.compare(value, r.maximum) >= 0 {
			return 0, last, true
		}
		end := sort.Search(len(r.boundaries), func(i int) bool {
			return r.compare(r.boundaries[i], value) >= 0
		})
		return 0, end, true
	}
	maximum := r.compare(value, r.maximum)
	if maximum > 0 || (!inclusive && maximum == 0) {
		return 0, 0, false
	}
	if r.compare(value, r.minimum) <= 0 {
		return 0, last, true
	}
	first := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	return first, last, true
}

func (r *lossyComparedBuckets[V]) exactRange(value V) (int, int, bool) {
	if len(r.buckets) == 0 || r.compare(value, r.minimum) < 0 || r.compare(value, r.maximum) > 0 {
		return 0, 0, false
	}
	bucket := sort.Search(len(r.boundaries), func(i int) bool {
		return r.compare(r.boundaries[i], value) >= 0
	})
	return bucket, bucket, true
}

func (r *lossyComparedBuckets[V]) addRange(first, last int, dst *roaring.Bitmap) {
	for i := first; i <= last; i++ {
		dst.Or(r.buckets[i])
	}
}

func (r *lossyComparedBuckets[V]) rangeCardinality(first, last int) uint64 {
	var result uint64
	for i := first; i <= last; i++ {
		result += r.buckets[i].GetCardinality()
	}
	return result
}

func (r *lossyComparedBuckets[V]) rangeContains(first, last int, id uint32) bool {
	for i := first; i <= last; i++ {
		if r.buckets[i].Contains(id) {
			return true
		}
	}
	return false
}

func (r *lossyComparedBuckets[V]) memoryUsage() uint64 {
	usage := uint64(16 * len(r.buckets))
	for i, bits := range r.buckets {
		usage += comparableValueBytes(any(r.boundaries[i])) + bitmapBytes(bits)
	}
	return usage
}

func (r *lossyComparedBuckets[V]) prepareSearch() {
	for _, bits := range r.buckets {
		prepareBitmapForSearch(bits)
	}
}
