package ruleix

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBitmapPoolReturnsEmptyBitmapAfterPut(t *testing.T) {
	pool := newBitmapPool()
	bits := pool.get()
	bits.AddRange(0, 1_000)
	require.LessOrEqual(t, bits.GetSizeInBytes(), uint64(maxPooledBitmapBytes))

	pool.put(bits)

	reused := pool.get()
	require.True(t, reused.IsEmpty())
	reused.Add(42)
	require.True(t, reused.Contains(42))
}

func TestBitmapPoolDiscardsOversizedBitmap(t *testing.T) {
	pool := newBitmapPool()
	bits := pool.get()
	// One value per high-16-bit key creates enough containers to exceed the
	// pool limit without requiring a large cardinality.
	for id := uint32(0); bits.GetSizeInBytes() <= maxPooledBitmapBytes; id++ {
		bits.Add(id << 16)
	}
	require.Greater(t, bits.GetSizeInBytes(), uint64(maxPooledBitmapBytes))

	pool.put(bits)

	require.NotSame(t, bits, pool.get())
}

// BenchmarkBitmapPoolRareWide models a large scratch result appearing among
// predominantly narrow searches. It guards both narrow reuse and the cost of
// rejecting the occasional oversized bitmap.
func BenchmarkBitmapPoolRareWide(b *testing.B) {
	pool := newBitmapPool()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bits := pool.get()
		if i%128 == 0 {
			for id := uint32(0); id < 8_192; id++ {
				bits.Add(id << 16)
			}
		} else {
			bits.Add(uint32(i))
		}
		pool.put(bits)
	}
}
