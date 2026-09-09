# README audit

The README was reviewed against the public API and canonical documentation on
2026-09-09.

## Changes

- Corrected the minimum Go version from 1.23 to the `go.mod` requirement of
  Go 1.24.
- Replaced the long introductory example with a smaller complete quick start.
- Made the `Index.Search` versus `Local.Search` memory and concurrency trade-off
  explicit.
- Kept user-visible wildcard, result-order, build, Lossy, and Inspect contracts
  while moving detailed implementation policy to focused documents.
- Removed the proposed Rankix API link from the active search documentation.
- Removed streaming checkpoints, physical key levels, cache admission internals,
  and inspector counter implementation details that duplicated canonical design
  documents and obscured first-use guidance.

## Verification

The embedded quick start is compiled and executed as a temporary example test.
Repository examples and tests pass with `go test ./...`; the race suite,
Markdown link validation, the 500-line limit, and `git diff --check` also pass.
`make lint` still reports 30 pre-existing findings in unchanged Go files; the
documentation revision introduces no lint findings.
