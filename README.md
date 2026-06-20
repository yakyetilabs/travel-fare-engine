# Travel Fare Engine

A standalone **A2A microservice** that computes deterministic travel fare quotes.
It is the pricing half of a two-repo agentic system: an LLM orchestrator
(`travel-agent`) gathers a trip and calls this engine over A2A; the engine prices
it and returns a structured `FareQuote`.

The headline idea: **a deterministic core wrapped in an LLM-as-transport adapter.**
Every dollar of arithmetic lives in pure, audited Go — the model never does math.

---

## Why this project exists

It's a portfolio piece demonstrating a production-shaped pattern for
**agentic microservices**: how to expose an audit-sensitive, deterministic
function as a discoverable, composable A2A agent without letting the LLM anywhere
near the numbers. The pricing problem is intentionally simple so the *architecture*
can be the point — boundary contracts, deterministic cores, tripwire tests, and
eval-gated delivery.

---

## Design philosophy & principles

These are the rules the codebase actually holds itself to (see `DECISIONS.md` for
the full ADR):

1. **Deterministic core, LLM-as-wrapper.** All pricing math is pure Go in
   [`internal/domain/fare/engine.go`](internal/domain/fare/engine.go) — no I/O, no
   network, no LLM. The model only (a) parses natural language into a structured
   request and (b) transcribes the tool's output to the wire schema. Fares are the
   most audit-sensitive output in the system, so they are never produced by a
   prompt.
2. **The boundary contract is the only shared truth.** Input (`FareQuoteRequest`)
   and output (`FareQuote`) are plain structs. The two repos share **no code** —
   they must stay independently deployable.
3. **Intentional duplication + tripwire tests.** The enum vocabularies (cabin,
   booking, route, season, passenger) are deliberately duplicated in the
   orchestrator repo. A tripwire test on each side fails the build if they drift —
   here, [`internal/domain/fare/schema_test.go`](internal/domain/fare/schema_test.go)
   checks the engine's `agent-card.json` against the code's exported slices.
4. **Statelessness.** No persistence, no inventory, no session affinity. Quote IDs
   are for the orchestrator's audit trail, not a database key.
5. **Eval-gated delivery.** A golden-case [`eval/`](eval/) suite pins the pricing
   math; CI blocks deploy if it regresses.
6. **Narrow boundary.** The engine knows only distances, dates, passenger counts,
   and booking classes. It never sees airports or traveler PII — deriving those is
   the orchestrator's job.

---

## Architecture

```
┌──────────────────────┐         A2A (JSON-RPC 2.0, message/send)
│  Orchestrator repo   │         + GCP ID token (Cloud Run IAM)
│  (travel-agent, Py)  │ ───────────────────────────────────────┐
└──────────────────────┘                                         │
                                                                 ▼
                              ┌──────────────────────────────────────────────┐
                              │              travel-fare-engine                │
                              │                                                │
                              │   A2A server (a2a-go)  ──►  ADK SequentialAgent│
                              │                              ├─ pricing  (LLM + compute_fare tool)
                              │                              └─ formatter(LLM + output_schema)
                              │                                      │         │
                              │                          compute_fare│ calls   │
                              │                                      ▼         │
                              │            fare.Calculate()  ── pure Go ───────│
                              │            tables.go (static fare matrices)    │
                              └──────────────────────────────────────────────┘
```

**Two-step agent pipeline:**

1. **`pricing`** — an `LlmAgent` with the `compute_fare` `FunctionTool`. It
   validates/normalizes the request and calls the tool. It has **no**
   `output_schema` (that would disable tool calling).
2. **`formatter`** — an `LlmAgent` with `output_schema` and **no** tools. It
   transcribes the tool's `FareQuote` into the schema-validated wire response.

Wiring lives in [`cmd/server/main.go`](cmd/server/main.go).

---

## The boundary contract

### Input — `FareQuoteRequest`

| Field                   | Type               | Constraints                                       |
| ----------------------- | ------------------ | ------------------------------------------------- |
| `base_distance_miles`   | int                | 100–10000 (orchestrator derives from airports)    |
| `advance_purchase_days` | int                | 0–365                                             |
| `passengers`            | `[]PassengerGroup` | ≥1 group; **total count ≤ 9**                     |
| `cabin_class`           | string             | `economy` `premium_economy` `business` `first`    |
| `booking_class`         | string             | `Y B M H Q G K`                                   |
| `route_type`            | string             | `domestic` `international`                        |
| `season_code`           | string             | `low` `shoulder` `peak`                           |

`PassengerGroup` = `{ count: 1–9, type: adult|child|infant }`.

### Output — `FareQuote`

`base_fare`, `taxes[]`, `total_fare`, `currency`, `booking_class`,
`fare_basis_code`, `fare_rules` (refundable / changeable / advance_purchase_min),
`pricing_breakdown[]`, `quote_id`, `expires_at`.

Full struct definitions: [`internal/domain/fare/schema.go`](internal/domain/fare/schema.go).

---

## Pricing model

All numbers come from static tables in
[`internal/domain/fare/tables.go`](internal/domain/fare/tables.go) (stand-ins for
real ATPCO data):

- **Base fare** = `distance × per_mile_rate`, where the rate is a product of
  cabin/route, booking-class, and season multipliers (168 precomputed combinations).
- **Advance-purchase discount** — a tiered curve keyed by days booked ahead.
- **Passenger factors** — adult ×1.00, child ×0.75, infant ×0.10.
- **Taxes** — keyed by `route_type` × passenger type (domestic US taxes;
  international departure/arrival fees), supporting both proportional and flat fees.
- **`fare_basis_code`** — synthesized deterministically from the inputs
  (e.g. `EYLD04`).
- **`expires_at`** — `now + QuoteValidityWindow` (a short, fixed 24h fare hold —
  deliberately independent of `advance_purchase_days`, which only affects price).

### What the engine validates (and what it doesn't)

**Rejects:** empty passengers; total passengers > 9; unknown enum values; a
booking class booked inside its advance-purchase minimum (G≥21, Q≥14, K≥7 — a
single source of truth that also feeds the advertised `fare_rules`); out-of-range
distance or advance-purchase days.

**Does not reject:** odd-but-valid passenger mixes (e.g. infant-only). Business
policy belongs to the orchestrator; the engine is pure math.

---

## Project structure

```
travel-fare-engine/
├── agent-card.json          # static A2A card, served at /.well-known/agent-card.json
├── cmd/server/
│   ├── main.go              # A2A server + SequentialAgent wiring + card rendering
│   └── prompts.go           # pricing & formatter agent instructions
├── internal/domain/fare/
│   ├── schema.go            # FareQuoteRequest / FareQuote structs + vocabularies
│   ├── engine.go            # pure Calculate() + validation
│   ├── tables.go            # static fare/tax matrices, AP curve, AP minimums
│   ├── engine_test.go       # table-driven unit tests
│   └── schema_test.go       # agent-card tripwire test
├── eval/                    # golden-case eval harness (cases.json + eval_test.go)
├── DECISIONS.md             # architecture decision record
└── LOCAL_TESTING.md         # step-by-step local test guide
```

---

## Getting started

### Prerequisites

- Go 1.26+
- A Gemini API key (only needed to run the LLM agent server; the pure engine and
  all tests run without one).

### Run the A2A server

```bash
export GEMINI_API_KEY="your-key"
export PORT=8081            # optional, defaults to 8081
# export HOST_URL=...       # optional; the URL advertised in the agent card
go run ./cmd/server
```

Then send a fare request (A2A `message/send`):

```bash
curl -sS -X POST http://localhost:8081/ \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": "smoke-1",
    "method": "message/send",
    "params": {
      "message": {
        "messageId": "00000000-0000-0000-0000-000000000001",
        "role": "user",
        "parts": [{
          "kind": "text",
          "text": "Compute a fare quote for base_distance_miles=2500, advance_purchase_days=30, passengers=[{\"count\":1,\"type\":\"adult\"}], cabin_class=economy, booking_class=Y, route_type=domestic, season_code=low."
        }]
      }
    }
  }' | jq .
```

Check the discovery card:

```bash
curl -sS http://localhost:8081/.well-known/agent-card.json | jq .
```

> **Agent card URLs:** `main.go` rewrites the card's advertised interface URL at
> startup from `HOST_URL` (falling back to `http://localhost:$PORT`). When
> deploying to Cloud Run, set `HOST_URL` to the service URL — Cloud Run does not
> inject it automatically, and an unset value would advertise `localhost`.

See [`LOCAL_TESTING.md`](LOCAL_TESTING.md) for the full walkthrough (`.env`
loading, edge cases, graceful shutdown).

---

## Testing

```bash
go test ./...                                   # everything
go test ./internal/domain/fare/                 # unit + tripwire
go test ./internal/domain/fare/ -run TestTripwire   # contract tripwire only
go test ./eval/                                 # golden-case eval harness
```

The engine's math is fully covered without a model in the loop: table-driven unit
tests, the agent-card tripwire, and the eval harness. The orchestrator repo owns
the end-to-end, model-in-the-loop ADK evals. See [`eval/README.md`](eval/README.md).

---

## Deployment

Deployed to **Cloud Run** with `--no-allow-unauthenticated` — every call requires
a valid GCP identity token. The orchestrator's service account is granted
`roles/run.invoker` and attaches an ID token to each A2A call. CI authenticates via
**Workload Identity Federation** (no service-account keys).

```bash
# Emergency only — CI is the primary deploy path
gcloud run deploy travel-fare-engine --source . --region us-central1 \
  --no-allow-unauthenticated --set-env-vars HOST_URL=https://<service-url>/
```

IAM, ingress, and rollback details are in `DECISIONS.md` §11–12.

---

## Known simplifications

Honest about what a real system would add (full list in `DECISIONS.md` §10):

- Static fare tables instead of real-time pricing / ATPCO data.
- No quote persistence; IDs are audit references only.
- Approximate taxes (no airport-specific fees or stopover logic).
- `float64` rounded to cents — production would use integer cents or a decimal
  library to eliminate floating-point drift.
- No fair-lending / compliance review of fare adjustments.
- The `eval/` harness exists; the orchestrator-side end-to-end evalsets and the
  eval-gated CI wiring are the next step.

---

## Related

- **Orchestrator:** `travel-agent` — the Python ADK multi-agent system that calls
  this engine over A2A.
- **Architecture decisions:** [`DECISIONS.md`](DECISIONS.md)
