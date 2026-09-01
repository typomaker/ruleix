package ruleix

// Include matches a query field when it equals the stored field. A missing
// stored value is a wildcard and matches every concrete query value; a missing
// query value matches only stored wildcards.
//
// For example, to match a rule's optional country:
//
//	ruleix.Include(func(c Constraint) (string, bool) { return c.Country, true })
func Include[T any, V comparable](get Getter[T, V]) Rule[T] {
	return &eqRule[T, V]{get: get}
}
