# Roadmap

This file is the active implementation plan. Completed work and rejected
experiments belong in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md);
release-facing behavior belongs in [`CHANGELOG.md`](CHANGELOG.md). Do not keep
completed, superseded, or unrelated proposals here.

## Objective

Measure and improve warm `Local` execution from the latest accepted exact
query-key cache implementation. Each experiment must use the immediately
preceding accepted revision as its parent and preserve zero-allocation warm
searches, bounded retained memory, deterministic results, and concurrent search
safety.

The completed selective aggregate and nested `Lossy` policy plan, including
verification results and rejected scoring experiments, is recorded in
[`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Its release-facing behavior is
documented in [`CHANGELOG.md`](CHANGELOG.md), [`README.md`](README.md), and
[`docs/lossy-index.md`](docs/lossy-index.md).

## Active queue

The Warm-Local L1--L5 queue is complete. Its implementations, rejected
experiments, profiles, measurements, and verification commands are preserved
in [`ROADMAP_HISTORY.md`](ROADMAP_HISTORY.md). Add a new measured objective
before starting another optimization experiment.
