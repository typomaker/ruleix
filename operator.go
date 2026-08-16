package ruleix

import "fmt"

// Operator identifies a comparison performed by CompareBy. The comparison is
// written from the query's perspective: OperatorGTE means query >= stored.
type Operator uint8

const (
	// OperatorEQ represents query == stored and is parsed from "=".
	OperatorEQ Operator = iota
	// OperatorLT represents query < stored and is parsed from "<".
	OperatorLT
	// OperatorLTE represents query <= stored and is parsed from "<=".
	OperatorLTE
	// OperatorGT represents query > stored and is parsed from ">".
	OperatorGT
	// OperatorGTE represents query >= stored and is parsed from ">=".
	OperatorGTE
)

// ParseOperator parses one of "=", "<", "<=", ">", or ">=" for use by
// CompareBy. It returns an error for every other string.
func ParseOperator(value string) (Operator, error) {
	switch value {
	case "=":
		return OperatorEQ, nil
	case "<":
		return OperatorLT, nil
	case "<=":
		return OperatorLTE, nil
	case ">":
		return OperatorGT, nil
	case ">=":
		return OperatorGTE, nil
	default:
		return 0, fmt.Errorf("ruleix: unsupported operator %q", value)
	}
}
