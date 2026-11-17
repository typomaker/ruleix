package ruleix

// Build admits only exact equality operands without wildcard rows.
// Their key absence is a complete empty-result proof for the All group.
type emptyKeyOperand[T any] interface {
	supportsEmptyKeyCheck() bool
	isEmptyKey(T) bool
}

func (r *eqRule[T, V, K]) supportsEmptyKeyCheck() bool {
	return r.coarsen == nil && r.wildcard.IsEmpty()
}
func (r *eqRule[T, V, K]) isEmptyKey(v T) bool {
	value, present := r.get(v)
	return !present || r.values.get(r.equalityKey(value)) == nil
}
func (r *allRule[T]) prepareEmptyKeys() {
	r.emptyKeys = 0
	if len(r.children) > 8 {
		return
	}
	for i, child := range r.children {
		if operand, ok := child.(emptyKeyOperand[T]); ok && operand.supportsEmptyKeyCheck() {
			r.emptyKeys |= uint8(1) << i
		}
	}
}
