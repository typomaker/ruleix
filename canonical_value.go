package ruleix

import (
	"encoding/binary"
	"math"
)

// canonicalScalar appends a stable, architecture-independent encoding for the
// scalar types supported by the first lossy-index experiments. The type tag is
// part of the encoding so different concrete types cannot share a bucket.
func canonicalScalar(dst []byte, value any) ([]byte, bool) {
	switch value := value.(type) {
	case bool:
		if value {
			return append(dst, canonicalBool, 1), true
		}
		return append(dst, canonicalBool, 0), true
	case string:
		dst = append(dst, canonicalString)
		dst = binary.BigEndian.AppendUint64(dst, uint64(len(value)))
		return append(dst, value...), true
	case int:
		return appendCanonicalUint64(dst, canonicalInt, uint64(int64(value))), true
	case int8:
		return appendCanonicalUint64(dst, canonicalInt8, uint64(value)), true
	case int16:
		return appendCanonicalUint64(dst, canonicalInt16, uint64(value)), true
	case int32:
		return appendCanonicalUint64(dst, canonicalInt32, uint64(value)), true
	case int64:
		return appendCanonicalUint64(dst, canonicalInt64, uint64(value)), true
	case uint:
		return appendCanonicalUint64(dst, canonicalUint, uint64(value)), true
	case uint8:
		return appendCanonicalUint64(dst, canonicalUint8, uint64(value)), true
	case uint16:
		return appendCanonicalUint64(dst, canonicalUint16, uint64(value)), true
	case uint32:
		return appendCanonicalUint64(dst, canonicalUint32, uint64(value)), true
	case uint64:
		return appendCanonicalUint64(dst, canonicalUint64, value), true
	case uintptr:
		return appendCanonicalUint64(dst, canonicalUintptr, uint64(value)), true
	case float32:
		return appendCanonicalUint64(dst, canonicalFloat32, uint64(canonicalFloat32Bits(value))), true
	case float64:
		return appendCanonicalUint64(dst, canonicalFloat64, canonicalFloat64Bits(value)), true
	default:
		return dst, false
	}
}

func appendCanonicalUint64(dst []byte, tag byte, value uint64) []byte {
	dst = append(dst, tag)
	return binary.BigEndian.AppendUint64(dst, value)
}

// orderedScalarKey maps supported values to uint64 while preserving their
// natural order within one concrete type. Floating-point keys follow
// cmp.Compare: NaNs precede non-NaNs, and -0 and +0 are equal.
func orderedScalarKey(value any) (uint64, bool) {
	switch value := value.(type) {
	case int:
		return signedOrderedKey(int64(value), 64), true
	case int8:
		return signedOrderedKey(int64(value), 8), true
	case int16:
		return signedOrderedKey(int64(value), 16), true
	case int32:
		return signedOrderedKey(int64(value), 32), true
	case int64:
		return signedOrderedKey(value, 64), true
	case uint:
		return unsignedOrderedKey(uint64(value), 64), true
	case uint8:
		return unsignedOrderedKey(uint64(value), 8), true
	case uint16:
		return unsignedOrderedKey(uint64(value), 16), true
	case uint32:
		return unsignedOrderedKey(uint64(value), 32), true
	case uint64:
		return value, true
	case uintptr:
		return unsignedOrderedKey(uint64(value), 64), true
	case float32:
		return unsignedOrderedKey(uint64(orderedFloat32Bits(value)), 32), true
	case float64:
		return orderedFloat64Bits(value), true
	default:
		return 0, false
	}
}

func signedOrderedKey(value int64, width uint) uint64 {
	bits := uint64(value)
	if width < 64 {
		bits &= uint64(1)<<width - 1
	}
	bits ^= uint64(1) << (width - 1)
	return unsignedOrderedKey(bits, width)
}

func unsignedOrderedKey(value uint64, width uint) uint64 {
	if width == 64 {
		return value
	}
	return value << (64 - width)
}

func canonicalFloat32Bits(value float32) uint32 {
	if value == 0 {
		return 0
	}
	if math.IsNaN(float64(value)) {
		return 0x7fc00000
	}
	return math.Float32bits(value)
}

func canonicalFloat64Bits(value float64) uint64 {
	if value == 0 {
		return 0
	}
	if math.IsNaN(value) {
		return 0x7ff8000000000000
	}
	return math.Float64bits(value)
}

func orderedFloat32Bits(value float32) uint32 {
	if math.IsNaN(float64(value)) {
		return 0
	}
	if value == 0 {
		value = 0
	}
	bits := math.Float32bits(value)
	if bits&(uint32(1)<<31) != 0 {
		return ^bits
	}
	return bits ^ (uint32(1) << 31)
}

func orderedFloat64Bits(value float64) uint64 {
	if math.IsNaN(value) {
		return 0
	}
	if value == 0 {
		value = 0
	}
	bits := math.Float64bits(value)
	if bits&(uint64(1)<<63) != 0 {
		return ^bits
	}
	return bits ^ (uint64(1) << 63)
}

const (
	canonicalBool byte = iota + 1
	canonicalString
	canonicalInt
	canonicalInt8
	canonicalInt16
	canonicalInt32
	canonicalInt64
	canonicalUint
	canonicalUint8
	canonicalUint16
	canonicalUint32
	canonicalUint64
	canonicalUintptr
	canonicalFloat32
	canonicalFloat64
)
