package ruleix

import (
	"hash/fnv"
	"math/bits"
	"testing"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/stretchr/testify/require"
)

// These prototypes deliberately live in tests until the representation budget
// API is implemented. They exercise the two grouping schemes against the same
// canonical keys that production planning will use.
type equalityBucketPrototype struct {
	shift   uint
	buckets map[uint64]*roaring.Bitmap
}

func newEqualityBucketPrototype(values []any, bucketBits uint) *equalityBucketPrototype {
	p := &equalityBucketPrototype{shift: 64 - bucketBits, buckets: make(map[uint64]*roaring.Bitmap)}
	for id, value := range values {
		encoded, ok := canonicalScalar(nil, value)
		if !ok {
			panic("unsupported prototype value")
		}
		h := fnv.New64a()
		_, _ = h.Write(encoded)
		bucket := h.Sum64() >> p.shift
		posting := p.buckets[bucket]
		if posting == nil {
			posting = roaring.New()
			p.buckets[bucket] = posting
		}
		posting.Add(uint32(id))
	}
	return p
}

func (p *equalityBucketPrototype) search(value any) *roaring.Bitmap {
	encoded, ok := canonicalScalar(nil, value)
	if !ok {
		return roaring.New()
	}
	h := fnv.New64a()
	_, _ = h.Write(encoded)
	posting := p.buckets[h.Sum64()>>p.shift]
	if posting == nil {
		return roaring.New()
	}
	return posting
}

type orderedBucketPrototype struct {
	min     uint64
	width   uint64
	buckets []*roaring.Bitmap
}

func newOrderedBucketPrototype(values []any, bucketBits uint) *orderedBucketPrototype {
	keys := make([]uint64, len(values))
	minKey, maxKey := ^uint64(0), uint64(0)
	for index, value := range values {
		key, ok := orderedScalarKey(value)
		if !ok {
			panic("unsupported prototype value")
		}
		keys[index] = key
		minKey = min(minKey, key)
		maxKey = max(maxKey, key)
	}
	bucketCount := uint64(1) << bucketBits
	span := maxKey - minKey
	width := span/bucketCount + 1
	usedBuckets := span/width + 1
	p := &orderedBucketPrototype{min: minKey, width: width, buckets: make([]*roaring.Bitmap, usedBuckets)}
	for id, value := range values {
		_ = value
		bucket := (keys[id] - p.min) / p.width
		posting := p.buckets[bucket]
		if posting == nil {
			posting = roaring.New()
			p.buckets[bucket] = posting
		}
		posting.Add(uint32(id))
	}
	return p
}

func (p *orderedBucketPrototype) greaterOrEqual(value any) *roaring.Bitmap {
	key, ok := orderedScalarKey(value)
	if !ok {
		return roaring.New()
	}
	first := uint64(0)
	if key > p.min {
		first = (key - p.min) / p.width
	}
	result := roaring.New()
	for bucket := first; bucket < uint64(len(p.buckets)); bucket++ {
		if posting := p.buckets[bucket]; posting != nil {
			result.Or(posting)
		}
	}
	return result
}

func prototypeBytes(buckets map[uint64]*roaring.Bitmap) uint64 {
	// Version 1 prototype accounting: an eight-byte bucket key, an eight-byte
	// logical table slot, and the portable Roaring payload.
	var n uint64
	for _, posting := range buckets {
		n += 16 + posting.GetSerializedSizeInBytes()
	}
	return n
}

func orderedPrototypeBytes(buckets []*roaring.Bitmap) uint64 {
	// Dense ordered slots need no retained key, only one eight-byte logical
	// slot each and the portable payloads of non-empty postings.
	n := uint64(len(buckets)) * 8
	for _, posting := range buckets {
		if posting != nil {
			n += posting.GetSerializedSizeInBytes()
		}
	}
	return n
}

func TestEqualityBucketPrototypeNeverDropsExactMatches(t *testing.T) {
	values := make([]any, 4096)
	for i := range values {
		values[i] = "customer-" + string(rune(i))
	}
	for _, bucketBits := range []uint{0, 4, 8, 12} {
		prototype := newEqualityBucketPrototype(values, bucketBits)
		for id, value := range values {
			require.True(t, prototype.search(value).Contains(uint32(id)), "bits=%d id=%d", bucketBits, id)
		}
	}
}

func TestOrderedBucketPrototypeNeverDropsExactMatches(t *testing.T) {
	values := make([]any, 4096)
	for i := range values {
		values[i] = int64(i - 2048)
	}
	for _, bucketBits := range []uint{0, 4, 8, 12, 16} {
		prototype := newOrderedBucketPrototype(values, bucketBits)
		for query := int64(-2048); query <= 2048; query += 127 {
			matches := prototype.greaterOrEqual(query)
			for id, value := range values {
				if value.(int64) >= query {
					require.True(t, matches.Contains(uint32(id)), "bits=%d query=%d id=%d", bucketBits, query, id)
				}
			}
		}
	}
}

func BenchmarkLossyStrategyPrototypes(b *testing.B) {
	const entries = 100_000
	equalityValues := make([]any, entries)
	orderedValues := make([]any, entries)
	for i := range entries {
		equalityValues[i] = uint64(bits.RotateLeft64(uint64(i)*0x9e3779b97f4a7c15, 17))
		orderedValues[i] = int64(i)
	}
	for _, bucketBits := range []uint{8, 12, 16} {
		b.Run("Equality/Bits"+prototypeUint(bucketBits), func(b *testing.B) {
			prototype := newEqualityBucketPrototype(equalityValues, bucketBits)
			accounted := prototypeBytes(prototype.buckets)
			candidates := prototype.search(equalityValues[entries/2]).GetCardinality()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = prototype.search(equalityValues[entries/2]).GetCardinality()
			}
			b.ReportMetric(float64(accounted), "accounted-bytes")
			b.ReportMetric(float64(candidates), "candidates/op")
		})
		b.Run("OrderedGE/Bits"+prototypeUint(bucketBits), func(b *testing.B) {
			prototype := newOrderedBucketPrototype(orderedValues, bucketBits)
			accounted := orderedPrototypeBytes(prototype.buckets)
			candidates := prototype.greaterOrEqual(int64(entries - 100)).GetCardinality()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = prototype.greaterOrEqual(int64(entries - 100)).GetCardinality()
			}
			b.ReportMetric(float64(accounted), "accounted-bytes")
			b.ReportMetric(float64(candidates), "candidates/op")
		})
	}
}

func prototypeUint(value uint) string {
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for value != 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
