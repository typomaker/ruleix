package ruleix

import "fmt"

// Operator is a comparison operator stored in a rule.
type Operator uint8

const (
	OperatorEQ Operator = iota
	OperatorLT
	OperatorLTE
	OperatorGT
	OperatorGTE
)

// ParseOperator parses the operators supported by CompareBy.
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
