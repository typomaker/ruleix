package ruleix

import (
	"math"
	"reflect"
	"time"
	"unsafe"
)

// orderedQuantizer maps numeric and time values onto a fixed, domain-wide
// monotonic grid. The origin never depends on observed input, so values seen
// after a downgrade use exactly the same boundaries as the published keys.
type orderedQuantizer[V any] struct {
	encode func(V) uint64
	decode func(uint64) V
	bits   uint32
	time   bool
}

func compileOrderedQuantizer[V any]() (orderedQuantizer[V], bool) {
	typeOf := reflect.TypeOf((*V)(nil)).Elem()
	if typeOf == reflect.TypeOf(time.Time{}) {
		return orderedQuantizer[V]{
			encode: func(value V) uint64 {
				instant := any(value).(time.Time)
				return signedOrderedKey(instant.Unix(), 64)
			},
			decode: func(key uint64) V {
				instant := time.Unix(int64(key^(uint64(1)<<63)), 0).UTC()
				return any(instant).(V)
			},
			bits: 64, time: true,
		}, true
	}

	kind := typeOf.Kind()
	switch kind {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Uintptr, reflect.Float32, reflect.Float64:
	default:
		return orderedQuantizer[V]{}, false
	}
	width := uint32(typeOf.Bits())
	q := orderedQuantizer[V]{bits: width}
	switch kind {
	case reflect.Int:
		q.encode, q.decode = signedQuantizerCodec[V, int](width)
	case reflect.Int8:
		q.encode, q.decode = signedQuantizerCodec[V, int8](width)
	case reflect.Int16:
		q.encode, q.decode = signedQuantizerCodec[V, int16](width)
	case reflect.Int32:
		q.encode, q.decode = signedQuantizerCodec[V, int32](width)
	case reflect.Int64:
		q.encode, q.decode = signedQuantizerCodec[V, int64](width)
	case reflect.Uint:
		q.encode, q.decode = unsignedQuantizerCodec[V, uint](width)
	case reflect.Uint8:
		q.encode, q.decode = unsignedQuantizerCodec[V, uint8](width)
	case reflect.Uint16:
		q.encode, q.decode = unsignedQuantizerCodec[V, uint16](width)
	case reflect.Uint32:
		q.encode, q.decode = unsignedQuantizerCodec[V, uint32](width)
	case reflect.Uint64:
		q.encode, q.decode = unsignedQuantizerCodec[V, uint64](width)
	case reflect.Uintptr:
		q.encode, q.decode = unsignedQuantizerCodec[V, uintptr](width)
	case reflect.Float32:
		q.encode = func(value V) uint64 {
			return uint64(orderedFloat32Bits(*(*float32)(unsafe.Pointer(&value)))) << 32
		}
		q.decode = func(key uint64) V {
			bits := uint32(key >> 32)
			if bits&(uint32(1)<<31) != 0 {
				bits ^= uint32(1) << 31
			} else {
				bits = ^bits
			}
			value := math.Float32frombits(bits)
			return *(*V)(unsafe.Pointer(&value))
		}
	case reflect.Float64:
		q.encode = func(value V) uint64 { return orderedFloat64Bits(*(*float64)(unsafe.Pointer(&value))) }
		q.decode = func(key uint64) V {
			bits := key
			if bits&(uint64(1)<<63) != 0 {
				bits ^= uint64(1) << 63
			} else {
				bits = ^bits
			}
			value := math.Float64frombits(bits)
			return *(*V)(unsafe.Pointer(&value))
		}
	}
	return q, true
}

func signedQuantizerCodec[V any, S signedOrderedScalar](width uint32) (func(V) uint64, func(uint64) V) {
	return func(value V) uint64 {
			return signedOrderedKey(int64(*(*S)(unsafe.Pointer(&value))), uint(width))
		}, func(key uint64) V {
			raw := key >> (64 - width)
			raw ^= uint64(1) << (width - 1)
			value := S(raw)
			return *(*V)(unsafe.Pointer(&value))
		}
}

func unsignedQuantizerCodec[V any, S unsignedOrderedScalar](width uint32) (func(V) uint64, func(uint64) V) {
	return func(value V) uint64 {
			return unsignedOrderedKey(uint64(*(*S)(unsafe.Pointer(&value))), uint(width))
		}, func(key uint64) V {
			value := S(key >> (64 - width))
			return *(*V)(unsafe.Pointer(&value))
		}
}

func (q orderedQuantizer[V]) terminalLevel() uint32 {
	if q.time {
		return q.bits + 1 // level one first removes sub-second precision.
	}
	return q.bits
}

func (q orderedQuantizer[V]) rounded(value V, level uint32, upward bool) V {
	if level == 0 {
		return value
	}
	key := q.encode(value)
	shift := level
	if q.time {
		// Unix seconds use floor semantics. Move a fractional upper boundary to
		// the following second before applying the coarser second grid.
		if upward && any(value).(time.Time).Nanosecond() != 0 && key != math.MaxUint64 {
			key++
		}
		shift--
	} else {
		shift += 64 - q.bits
	}
	key = roundOrderedKey(key, shift, upward)
	return q.decode(key)
}

func roundOrderedKey(key uint64, shift uint32, upward bool) uint64 {
	if shift >= 64 {
		if upward {
			return math.MaxUint64
		}
		return 0
	}
	if shift == 0 {
		return key
	}
	mask := uint64(1)<<shift - 1
	if !upward || key&mask == 0 {
		return key &^ mask
	}
	if math.MaxUint64-key < mask {
		return math.MaxUint64
	}
	return (key + mask) &^ mask
}
