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
| `Include` | exact match, non-match, stored wildcard, missing query, duplicate IDs, present zero versus absent | no known public semantic gap |
| `Exclude` | forbidden match, non-match, missing query, stored missing value, repeated ID exclusion, present zero versus absent, scanning-reference differential, `Search` | no known public semantic gap |
| `Greater`, `GreaterOrEqual`, `Less`, `LessOrEqual` | all four directions, strict and inclusive equality boundaries, less/equal/greater values, stored wildcard, missing query, present zero versus absent | no known public semantic gap |
| `CompareBy` | all five operators, equality and broad scanning-reference comparisons, ignored query operator, stored wildcard, missing query value, missing operator error, unsupported operator error | boundary truth tables remain split across focused and scanning-reference tests rather than duplicated in one table |
| `Between` | covered, non-covered, exact inclusive bounds, stored/query bounds missing independently and together, repeated IDs, inverted intervals, scanning-reference comparisons | no known public semantic gap; inverted intervals intentionally use the two independent bound comparisons |
| `All` | zero-, one-, multi-child AND, nesting/flattening, wildcard children, equality/ordered/`Between` combinations, exclusions, repeated children, adaptive execution and underestimated wide bitmap paths | no known public semantic gap |
| `Lossy` | central exact-versus-lossy superset matrix for every lossy-capable predicate; local and streaming paths | `Exclude` is intentionally exact-only and is covered by its scanning oracle; wrapper combinations remain spread across specialized tests |
| `Inspect` | transparent results for normal and local search plus exact/lossy reporting | no semantic gap found in the reviewed public contract |

## Conclusion

The previously identified public semantic cases now have focused regression
coverage. This remains a maintained risk inventory rather than a claim that
the combinatorial input space can be exhaustively enumerated.

## Risk assessment

The 84.5% statement result leaves two different kinds of uncovered code. Many
0% methods are sealed-interface stubs or pre-compilation placeholders and are
not evidence of a missing public runtime scenario. They should not be treated
as critical merely because their function percentage is zero.

Focused tests now cover the `CompareBy` missing-operator branch, independent
missing `Between` query bounds, zero-value distinction, inverted intervals,
zero-child `All`, an `Exclude` scanning oracle, and both small-inline and
more-than-eight-child underestimated wide-`All` bitmap assembly. The new empty
`All` test exposed and fixed a correctness defect: it previously returned no
IDs instead of acting as the identity of logical AND.

After these additions aggregate statement coverage is 85.2%; the active
`materializeRankedAfterFirst` and `appendBitmapAllMatches` helpers are now
covered. No remaining 0% function was classified as an uncovered public
valid-logic contract in this audit.
