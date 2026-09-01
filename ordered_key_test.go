package ruleix

import (
	"math"
	"testing"
)

type namedOrderedInt32 int32

func mustCompileOrderedKeyEncoder[V any]() orderedKeyEncoder[V] {
	encoder, ok := compileOrderedKeyEncoder[V]()
	if !ok {
		panic("ordered key encoder unavailable")
	}
	return encoder
}

func TestCompiledOrderedKeyEncoderMatchesScalarContract(t *testing.T) {
	assertOrderedEncoder(t, []int8{math.MinInt8, -1, 0, 1, math.MaxInt8})
	assertOrderedEncoder(t, []int16{math.MinInt16, -1, 0, 1, math.MaxInt16})
	assertOrderedEncoder(t, []int32{math.MinInt32, -1, 0, 1, math.MaxInt32})
	assertOrderedEncoder(t, []int64{math.MinInt64, -1, 0, 1, math.MaxInt64})
	assertOrderedEncoder(t, []uint8{0, 1, math.MaxUint8})
	assertOrderedEncoder(t, []uint16{0, 1, math.MaxUint16})
	assertOrderedEncoder(t, []uint32{0, 1, math.MaxUint32})
	assertOrderedEncoder(t, []uint64{0, 1, math.MaxUint64})
	assertOrderedEncoder(t, []float32{float32(math.Inf(-1)), -1, -0.0, 0, 1, float32(math.Inf(1)), float32(math.NaN())})
	assertOrderedEncoder(t, []float64{math.Inf(-1), -1, -0.0, 0, 1, math.Inf(1), math.NaN()})
}

func assertOrderedEncoder[V any](t *testing.T, values []V) {
	t.Helper()
	encoder, ok := compileOrderedKeyEncoder[V]()
	if !ok {
		t.Fatalf("encoder unavailable for %T", *new(V))
	}
	for _, value := range values {
		want, supported := orderedScalarKey(any(value))
		if !supported || encoder.key(value) != want {
			t.Fatalf("key(%v) = %d, want %d (supported %v)", value, encoder.key(value), want, supported)
		}
	}
}

func TestCompiledOrderedKeyEncoderRejectsOpaqueValues(t *testing.T) {
	if _, ok := compileOrderedKeyEncoder[string](); ok {
		t.Fatal("string unexpectedly has a numeric ordered encoder")
	}
	if _, ok := compileOrderedKeyEncoder[namedOrderedInt32](); ok {
		t.Fatal("named scalar unexpectedly changed physical representation")
	}
}

var orderedEncoderBenchmarkKey uint64

// Apple M1 Max, Go 1.26.0, GOMAXPROCS=1, 300ms x5: legacy int64 type switch
// 2.026-2.084 ns/op and compiled int64 2.181-2.207 ns/op, both at 0 B/op
// and 0 allocs/op. The production-shaped search gate is authoritative.
func BenchmarkCompiledOrderedKeyEncoder(b *testing.B) {
	b.Run("LegacyInt64", func(b *testing.B) {
		value := int64(-42)
		for b.Loop() {
			orderedEncoderBenchmarkKey, _ = orderedScalarKey(any(value))
		}
	})
	b.Run("CompiledInt64", func(b *testing.B) {
		encoder := mustCompileOrderedKeyEncoder[int64]()
		value := int64(-42)
		for b.Loop() {
			orderedEncoderBenchmarkKey = encoder.key(value)
		}
	})
}
