# Eval harness

Black-box golden-case evaluation for the fare engine. Each entry in
[`cases.json`](cases.json) is an A2A-shaped `FareQuoteRequest` paired with
**hand-computed** expected outputs (base fare, total, fare-basis prefix, tax
count, and fare-rule flags). The arithmetic for each case is documented inline in
`internal/domain/fare/engine_test.go`.

This is the deterministic half of the system's evaluation story. The orchestrator
repo (`travel-prequal` / travel-agent) owns the *end-to-end* ADK evalset that
drives the full intake → policy → fare_prep → fare_engine → finalizer pipeline
through real model calls. This harness pins the pricing math without a model in
the loop, so a pricing regression fails fast and cheaply.

## Run

```bash
go test ./eval/            # run the eval suite
go test ./eval/ -v         # per-case detail
go test ./...              # everything (unit + tripwire + eval)
```

## Adding a case

1. Construct the `request` (must satisfy the engine's validation rules).
2. Compute the expected `base_fare` / `total_fare` by hand — do **not** copy them
   from a program run, or the eval just asserts the code agrees with itself.
3. Add the case to `cases.json`. Monetary comparisons use a one-cent tolerance to
   absorb the documented float64 rounding drift (DECISIONS.md §10).

## Gating

CI runs this suite on every PR that touches `internal/domain/fare/`. A failing
eval blocks deploy (DECISIONS.md §9, "Evaluation-gated CI/CD").
