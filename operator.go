package ruleix

// Operator identifies a comparison stored in a CompareBy constraint. The
// comparison is written from the query's perspective: OperatorGTE means query
// >= stored.
type Operator uint8

const (
	// OperatorEQ selects query == stored.
	OperatorEQ Operator = iota
	// OperatorLT selects query < stored.
	OperatorLT
	// OperatorLTE selects query <= stored.
	OperatorLTE
	// OperatorGT selects query > stored.
	OperatorGT
	// OperatorGTE selects query >= stored.
	OperatorGTE
)
