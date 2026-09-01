# Rule logic test coverage audit

Audit date: 2026-09-01.

This document records the current semantic test coverage of Ruleix's public
rule constructors. It distinguishes the presence of representative positive
tests from exhaustive contract coverage; statement coverage alone is not used
as proof that every logical case is tested.

## Scope and verification

The audit covers `Include`, `Exclude`, `Greater`, `GreaterOrEqual`, `Less`,
`LessOrEqual`, `CompareBy`, `Between`, and `All`. `Lossy` and `Inspect` are
wrappers rather than independent matching predicates, so their relevant
contracts are approximation safety and semantic transparency respectively.

Verification commands:

```text
go test ./...
go test -coverprofile=<temporary-file> .
go tool cover -func=<temporary-file>
```

Measured result: all tests passed and aggregate statement coverage for the
root package was 84.5%. This percentage is supporting evidence only and does
not imply complete semantic coverage.

## Coverage matrix

| Rule | Covered valid logic | Confirmed gaps |
| --- | --- | --- |
| `Include` | exact match, non-match, stored wildcard, missing query through the production-shape matrix, duplicate IDs | no focused table that explicitly distinguishes a present zero value from an absent value |
| `Exclude` | forbidden match, non-match, missing query, stored missing value, repeated ID exclusion, `Search` and `Visit` | no focused present-zero-versus-absent case |
| `Greater`, `GreaterOrEqual`, `Less`, `LessOrEqual` | all four directions, strict and inclusive equality boundaries, less/equal/greater values, stored wildcard, missing query | no focused present-zero-versus-absent case for each constructor |
| `CompareBy` | all five operators, equality and broad scanning-reference comparisons, ignored query operator, stored wildcard, missing query value, unsupported operator | a present stored value with a missing operator is specified to fail build but has no direct test; boundary truth tables are split across tests instead of one explicit per-operator matrix |
| `Between` | covered, non-covered, exact inclusive bounds, both stored bounds missing, each stored bound missing independently, both query bounds missing, repeated IDs, scanning-reference comparisons | query missing only `from` and query missing only `until` are not tested independently; inverted stored/query intervals are not covered or explicitly rejected by contract |
| `All` | multi-child AND, nesting/flattening, wildcard children, equality/ordered/`Between` combinations, exclusions, repeated children and adaptive execution paths | public semantics of zero children are not asserted; there is no single exact differential oracle spanning every public child type including `Exclude` |
| `Lossy` | central exact-versus-lossy superset matrix for `Include`, all ordered rules, `Between`, `CompareBy`, and `All`; local and streaming paths | `Exclude` is absent from the central public-rule matrix (even if retained exact internally); wrapper combinations are spread across specialized tests |
| `Inspect` | transparent results for normal and local search plus exact/lossy reporting | no semantic gap found in the reviewed public contract |

## Conclusion

Every public matching rule type has representative valid-logic coverage, but
the full logical case space is not covered. The highest-value additions are:

1. test the documented `CompareBy` build error for a concrete value with a
   missing operator;
2. add the two one-sided missing-query cases for `Between`;
3. define and test the public contract of `All()` with zero children;
4. add compact zero-value-versus-absent tables for getter-based rules;
5. extend one exact differential oracle across every public predicate and
   wrapper-relevant mode, including `Exclude`.

Inverted intervals require a contract decision before a test can be written:
either reject them during build/search validation or document their current
comparison-derived behavior.
