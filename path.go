package ruleix

// Path composes two pointer getters while preserving nil along the path.
// Wildcard semantics remain the responsibility of the concrete filter using
// the resulting getter. It is commonly used to apply a filter to an optional
// nested field:
//
//	ruleix.Include(ruleix.Path(
//		func(c Constraint) *Customer { return c.Customer },
//		func(customer Customer) *string { return customer.Tier },
//	))
func Path[A any, B any, C any](ab func(A) *B, bc func(B) *C) func(A) *C {
	return func(a A) *C {
		b := ab(a)
		if b == nil {
			return nil
		}
		return bc(*b)
	}
}

// Path3 composes three pointer getters and returns nil as soon as any
// intermediate getter returns nil.
func Path3[A any, B any, C any, D any](
	ab func(A) *B,
	bc func(B) *C,
	cd func(C) *D,
) func(A) *D {
	return Path(Path(ab, bc), cd)
}

// Path4 composes four pointer getters and returns nil as soon as any
// intermediate getter returns nil.
func Path4[A any, B any, C any, D any, E any](
	ab func(A) *B,
	bc func(B) *C,
	cd func(C) *D,
	de func(D) *E,
) func(A) *E {
	return Path(Path3(ab, bc, cd), de)
}

// Path5 composes five pointer getters and returns nil as soon as any
// intermediate getter returns nil.
func Path5[A any, B any, C any, D any, E any, F any](
	ab func(A) *B,
	bc func(B) *C,
	cd func(C) *D,
	de func(D) *E,
	ef func(E) *F,
) func(A) *F {
	return Path(Path4(ab, bc, cd, de), ef)
}
