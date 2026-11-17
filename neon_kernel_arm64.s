//go:build arm64 && !purego

#include "textflag.h"

// The comparison positions fit in one byte. The caller masks unused lanes.
TEXT ·neonBetweenMask(SB), NOSPLIT, $0-32
 MOVD data+0(FP), R0
 MOVD lower+8(FP), R1
 MOVD upper+16(FP), R2
 VDUP R1, V4.B16
 VDUP R2, V5.B16
 VLD1 (R0), [V0.B16, V1.B16, V2.B16, V3.B16]
 VCMHS V0.B16, V4.B16, V4.B16
 VCMEQ $0, V0.B16, V6.B16
 VBIC V6.B16, V4.B16, V4.B16
 VORR V2.B16, V4.B16, V4.B16
 VCMHI V5.B16, V1.B16, V5.B16
 VORR V3.B16, V5.B16, V5.B16
 VAND V4.B16, V5.B16, V0.B16
 MOVD $·neonLaneWeights(SB), R0
 VLD1 (R0), [V1.B16]
 VAND V1.B16, V0.B16, V0.B16
 VMOV V0.D[0], R0
 VMOV V0.D[1], R1
 MOVD $0x0101010101010101, R2
 MUL R2, R0, R0
 MUL R2, R1, R1
 LSR $56, R0, R0
 LSR $56, R1, R1
 ORR R1<<8, R0, R0
 MOVD R0, ret+24(FP)
 RET

DATA ·neonLaneWeights+0(SB)/8, $0x8040201008040201
DATA ·neonLaneWeights+8(SB)/8, $0x8040201008040201
GLOBL ·neonLaneWeights(SB), RODATA|NOPTR, $16


// Cached data contains sixteen little-endian uint16 bound pairs.
TEXT ·neonSimpleBetweenMask(SB), NOSPLIT, $0-32
 MOVD data+0(FP), R0
 MOVD lower+8(FP), R1
 MOVD upper+16(FP), R2
 VLD1 (R0), [V0.B16, V1.B16]
 VUZP1 V1.B16, V0.B16, V2.B16
 VUZP2 V1.B16, V0.B16, V3.B16
 VDUP R1, V4.B16
 VDUP R2, V5.B16
 VCMHS V2.B16, V4.B16, V4.B16
 VCMEQ $0, V2.B16, V6.B16
 VBIC V6.B16, V4.B16, V4.B16
 VCMHI V5.B16, V3.B16, V5.B16
 VAND V4.B16, V5.B16, V0.B16
 MOVD $·neonLaneWeights(SB), R0
 VLD1 (R0), [V1.B16]
 VAND V1.B16, V0.B16, V0.B16
 VMOV V0.D[0], R0
 VMOV V0.D[1], R1
 MOVD $0x0101010101010101, R2
 MUL R2, R0, R0
 MUL R2, R1, R1
 LSR $56, R0, R0
 LSR $56, R1, R1
 ORR R1<<8, R0, R0
 MOVD R0, ret+24(FP)
 RET
