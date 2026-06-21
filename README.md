# Travel Fare Engine

A standalone **A2A microservice** that computes deterministic travel fare quotes.
It is the pricing half of a two-repo agentic system: an LLM orchestrator
(`travel-agent`) collects trip details and calls this engine over A2A; the engine
returns a structured `FareQuote` — no PII, no airports, just derived numeric values.

## Why this service is separate

The fare engine is not a library inside the orchestrator. It is an independent
service with its own codebase, deployment, and IAM scope. This separation exists
to enforce a hard boundary that is valuable in a production system:

- **Privacy by construction.** The engine never sees traveler names, email
  addresses, employee IDs, or even raw airport codes. Its request contract
  contains only distance in miles, passenger counts, advance‑purchase days, and
  classification codes. Logs from this service can be retained without exposing
  personally identifiable information.
- **Auditable pricing surface.** The only artefact of a pricing decision is the
  output of a pure Go function (`fare.Calculate()`). The function is fully
  unit‑tested, and the CI pipeline blocks deployment if it regresses.
- **Independent deployability.** Fare calculations can change without touching
  the orchestrator’s conversation logic. The two services evolve on their own
  cadences, bound only by the A2A contract.
- **A2A protocol demonstration.** The engine’s capabilities are published via
  `/.well-known/agent-card.json` and consumed by the orchestrator at runtime.
  No code is shared; the contract is the only shared truth.
- **Defence in depth.** The engine contains an LLM agent that refuses to call
  `compute_fare` if a required field is missing. Under normal operation the
  orchestrator provides a complete request, but this guard catches integration
  bugs before they produce incorrect fares.

## Design principles

The codebase holds itself to these rules:

1. **Deterministic core.** All math lives in `internal/domain/fare/engine.go`.
   The LLM never calculates fares; it only decides _when_ to call the
   deterministic tool.
2. **Pinned boundary contract.** Input (`FareQuoteRequest`) and output
   (`FareQuote`) are plain structs. Enum vocabularies are duplicated in the
   orchestrator repository intentionally — the price of independent
   deployability. Tripwire tests on both sides fail the build if they drift.
3. **Stateless.** No database, no session affinity, no quote persistence.
   Quote IDs are audit references for the orchestrator, not keys into engine
   storage.
4. **Eval‑gated delivery.** A golden‑case eval suite pins the pricing math.
   CI runs evals on relevant paths and blocks merge on regression.

See [`DECISIONS.md`](DECISIONS.md) for the full Architecture Decision Record.

---

## Architecture (internal)

```
┌──────────────────────┐        A2A (JSON-RPC 2.0, message/send)
│  Orchestrator repo   │        + GCP ID token (Cloud Run IAM)
│(SequentialAgent, Py) │ ───────────────────────────────────────┐
└──────────────────────┘                                        │
                                                                ▼
                              ┌────────────────────────────────────────────────┐
                              │              travel-fare-engine                │
                              │                                                │
                              │ A2A server (a2a-go)  ──►  ADK SequentialAgent  │
                              │                            ├─ pricing (LlmAgent) ← validates, normalises, calls      compute_fare
                              │                            └─ formatter (LlmAgent) ← transcribes tool output to FareQuote schema
                              │                                      │         │
                              │                          compute_fare│ tools   │
                              │                                      ▼         │
                              │            fare.Calculate()  ── pure Go ───────│
                              │            tables.go (static fare matrices)    │
                              └────────────────────────────────────────────────┘
```

**Two-step agent pipeline:**

1. **pricing** carries the instruction to never compute a fare itself and to ask
   for missing information if a field is absent. When a complete request arrives
   it calls `compute_fare` and the tool result becomes its entire response.
2. **formatter** (if present) is a lightweight agent with `output_schema =
FareQuote` and no tools. It ensures the wire response is exactly the
   schema‑validated struct, with no stray commentary.

The A2A server is wired in [`cmd/server/main.go`](cmd/server/main.go) and serves
the agent card at `/.well-known/agent-card.json`.

---

## The boundary contract

### Input — `FareQuoteRequest`

| Field                   | Type               | Constraints                                    |
| ----------------------- | ------------------ | ---------------------------------------------- |
| `base_distance_miles`   | int                | 100–10000 (orchestrator derives from airports) |
| `advance_purchase_days` | int                | 0–365                                          |
| `passengers`            | `[]PassengerGroup` | total count ≤ 9; types: adult, child, infant   |
| `cabin_class`           | string             | `economy` `premium_economy` `business` `first` |
| `booking_class`         | string             | `Y B M H Q G K`                                |
| `route_type`            | string             | `domestic` `international`                     |
| `season_code`           | string             | `low` `shoulder` `peak`                        |

### Output — `FareQuote`

`base_fare`, `taxes[]`, `total_fare`, `currency`, `booking_class`,
`fare_basis_code`, `fare_rules` (refundable / changeable / advance_purchase_min),
`pricing_breakdown[]`, `quote_id`, `expires_at`.

Full struct definitions: [`internal/domain/fare/schema.go`](internal/domain/fare/schema.go).

---

## Pricing model

All figures are derived from static tables in
[`internal/domain/fare/tables.go`](internal/domain/fare/tables.go) — stand‑ins for
live ATPCO or negotiated fares:

- **Base fare** = distance × per‑mile rate, where the rate is a product of cabin,
  route, booking class, and season multipliers.
- **Advance‑purchase discount** — tiered curve based on days booked ahead.
- **Passenger factors** — adult ×1.00, child ×0.75, infant ×0.10.
- **Taxes** — keyed by route type and passenger type.
- **Fare basis code** — synthesised deterministically from inputs.
- **Expiry** — fixed 24‑hour validity window, independent of advance‑purchase days.

### Validation

The engine rejects requests with empty passengers, total count > 9, unknown enum
values, or a booking class booked inside its advance‑purchase minimum. It does
**not** enforce business policy (e.g., who may fly business class) — that belongs
to the orchestrator.

## Security

- Deployed to **Cloud Run** with `--no-allow-unauthenticated`.
- Requires a valid Google identity token on every call.
- Service account `travel-fare-engine-sa` scoped to `roles/aiplatform.user` only
  (for Vertex AI access). No database, no GCS, no secret access.
- CI/CD authenticates via **Workload Identity Federation** — no service account
  keys exist.

---

## Getting started

### Prerequisites

- Go 1.26+
- A Gemini API key (only needed to run the LLM agent server; the pure engine and
  all tests run without one).

### Run the A2A server

```bash
export GEMINI_API_KEY="your-key"
export PORT=8081            # optional, default 8081
go run ./cmd/server
```

Test with a raw A2A request:

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

See [`LOCAL_TESTING.md`](LOCAL_TESTING.md) for step‑by‑step instructions.

---

## Testing

```bash
go test ./...                                   # everything
go test ./internal/domain/fare/                 # unit + tripwire
go test ./internal/domain/fare/ -run TestTripwire   # contract tripwire only
go test ./eval/                                 # golden-case eval harness
```

The engine’s math is covered without a live model. End‑to‑end, model‑in‑the‑loop
evaluations live in the orchestrator’s repository.

---

## Deployment

```bash
# Emergency only — CI is the primary deploy path
gcloud run deploy travel-fare-engine --source . --region us-central1 \
  --no-allow-unauthenticated --set-env-vars HOST_URL=https://<service-url>/
```

CI is the primary deploy path. The orchestrator’s service account must be granted
roles/run.invoker on this service.

---

## Known gaps

Honest about what a production system would add:

- **Static fare tables** instead of real-time pricing / ATPCO data.
- **No persistence.** Quotes are not stored; the quote ID is an audit reference only.
- **Approximate taxes.** No airport‑specific or stopover fees.
- `float64` rounded to cents — production would use integer cents or a decimal
  library to eliminate floating-point drift.
- **No automated rollback.** Cloud Run’s revision model keeps the previous
  version serving, but the pipeline does not automatically revert on smoke‑test failure.
- No fair-lending / compliance review of fare adjustments.

---

## Related

- **Orchestrator:** `travel-agent` — the Python multi-agent system that calls
  this engine over A2A.
- **Architecture decisions:** [`DECISIONS.md`](DECISIONS.md)
