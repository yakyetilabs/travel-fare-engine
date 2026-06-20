To test your `main.go` locally, you don’t need any cloud setup (Cloud Run, Kubernetes, etc.) – the whole A2A server runs as a single Go binary on your machine. However, you **do** need:

1. **A Gemini API key** (set as `GEMINI_API_KEY`).
2. **Go modules downloaded** (the ADK and all dependencies).
3. A **JSON‑RPC client** to send a request – `curl` is fine.
4. (Optional) An environment variable `HOST_URL` for local agent‑card URL; if not set it falls back to `http://localhost:<PORT>`.

Below is a step‑by‑step to get you from zero to a working test.

---

## 1. Prepare your environment variables

Open a terminal and set:

```bash
export GEMINI_API_KEY="your-api-key-here"
export PORT="8081"                    # optional, 8081 is the default
```

You don’t need `HOST_URL` unless you want the agent card to show a custom address during local dev. Your code will automatically fall back to `http://localhost:$PORT`.

---

## 2. Download dependencies and build

In the project root (where `go.mod` lives):

```bash
go mod tidy
go build -o travel-fare-engine ./cmd/server
```

If the build succeeds, the tool handler, agent chain, and all imports are correct.  
If you get compilation errors about missing packages, run `go get` for the missing imports (the ADK ones are already in your `main.go`).

---

## 3. Run the server

```bash
./travel-fare-engine
```

You should see:

```
Starting travel-fare-engine A2A server on port 8081...
```

> If you get a “CRITICAL: GEMINI_API_KEY environment variable is required”, double‑check your `export`.

---

## 4. Send a test request with `curl`

Open another terminal. The server’s JSON‑RPC endpoint is at **`/`** (root). Your `curl` command must be a POST to `http://localhost:8081/` with a JSON body following the A2A spec.

Here’s a minimal valid request (you can copy‑paste it):

The server speaks the A2A JSON-RPC method `message/send` (the same shape as the
smoke test in `CLAUDE.md`). The orchestrator sends a natural-language `user` part;
the pricing agent parses it into a `FareQuoteRequest` and calls the tool.

```bash
curl -X POST http://localhost:8081/ \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "message/send",
    "params": {
      "message": {
        "messageId": "00000000-0000-0000-0000-000000000001",
        "role": "user",
        "parts": [{
          "kind": "text",
          "text": "Compute a fare quote for base_distance_miles=1000, advance_purchase_days=21, passengers=[{\"count\":2,\"type\":\"adult\"},{\"count\":1,\"type\":\"child\"}], cabin_class=economy, booking_class=Y, route_type=domestic, season_code=low."
        }]
      }
    }
  }'
```

**What to expect:**

- The LLM chain will run – it may take a few seconds.
- The response should be a JSON‑RPC result containing a `FareQuote` object **without any surrounding prose**, because the formatter’s `OutputSchema` is now active.
- Example (truncated):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "base_fare": 200.00,
    "taxes": [...],
    "total_fare": 230.50,
    "currency": "USD",
    "booking_class": "Y",
    "fare_basis_code": "...",
    "fare_rules": {
      "refundable": true,
      "changeable": false,
      "advance_purchase_min": 21
    },
    "pricing_breakdown": [...],
    "quote_id": "...",
    "expires_at": "2026-07-10T12:34:56Z"
  }
}
```

If you get a JSON-RPC method error, verify you are POSTing `message/send` to `/` (the root path), not a custom method. The root agent name (`"travel_fare_engine"`, set in `sequentialagent.Config`) is internal and not part of the request body.

---

## 5. (Optional) Verify the agent card

```bash
curl http://localhost:8081/.well-known/agent-card.json
```

This should return a JSON card with the interface URL set to `http://localhost:8081/` (or your `HOST_URL` if set). The server rewrites the `supportedInterfaces[].url` field of the static `agent-card.json` at startup — see `resolvePublicURL` / `renderAgentCard` in `cmd/server/main.go` — so the static file's URL is only a default. The skills section should list `compute_fare`.

---

## 6. Testing edge cases

- **Empty passengers**: Send `"passengers": []` → expect an error from the tool.
- **Invalid booking class**: Use `"booking_class": "Z"` → the pricing agent should ask for clarification.
- **Missing field**: Omit `"cabin_class"` entirely → the LLM should not call the tool; instead it might ask for the missing field.

---

## 7. Shutdown gracefully

In the terminal running the server, press `Ctrl+C` – you should see:

```
Shutting down server...
Server exited
```

That confirms the signal handler works.

---

## Cloud setup (only for production deployment)

When you’re ready to deploy to Cloud Run:

- **Set `HOST_URL` to the service's public URL** so the agent card advertises a reachable address. Cloud Run does **not** inject the service URL into a standard env var automatically (the only auto-injected vars are `K_SERVICE`, `K_REVISION`, `K_CONFIGURATION`), so `renderAgentCard` falls back to `http://localhost:<port>` if `HOST_URL` is unset — which the orchestrator cannot reach. Set it explicitly (e.g. `--set-env-vars HOST_URL=https://travel-fare-engine-xxxx.run.app/`).
- Set `GEMINI_API_KEY` as a Secret Manager secret rather than a plain env var.
- The graceful shutdown is already in place, so Cloud Run won’t drop in‑flight requests.

For now, focus on the local test. Once `curl` returns a clean `FareQuote`, you’ll know the whole pipeline works.
The export commands set environment variables in your current terminal session. They aren’t stored in your Go project files or on your machine permanently. They only live as long as that terminal window is open. Once you close the terminal, the variables disappear.

## in a .env file (loaded by your code)

Create a file named .env in your project root (don’t commit it to git):

text
GEMINI_API_KEY=your-api-key-here
PORT=8081
Use a library like godotenv to load the file at startup. Add this in main.go before os.Getenv calls:

go
import "github.com/joho/godotenv"
...
func main() {
\_ = godotenv.Load() // silently ignore if no .env file
...
}
This is the recommended approach for local dev – no need to set variables by hand each time.

## For production (Cloud Run)

You don’t use export commands. Instead, you set environment variables in the Cloud Run service configuration (via the Google Cloud Console or gcloud run deploy --set-env-vars GEMINI_API_KEY=...). The code will read them just the same.

Summary
The export commands are shell‑level – not stored in any file, not part of the Go project.

For local testing, the simplest path: open a terminal, run the two export lines, then go run ./cmd/server.

For repeated local work, add a .env file and load it with godotenv.

The Go code itself never contains the key; it only reads it from the environment.

---

To prepare your project for version control and eventual deployment, you need to:

- Initialize a Git repository.
- Decide on a proper module path (so imports work both locally and from a remote).
- Create a `.gitignore` that excludes secrets, binaries, and editor clutter.
- Ensure your code doesn’t contain any hard‑coded keys.

Let’s tackle each step.

---

## 1. Choose the right module path

Your current `module travel-fare-engine` is a **local name**. If you push this to a remote (e.g., GitHub) and someone else tries to import a sub‑package like `travel-fare-engine/internal/domain/fare`, Go’s package resolver will not know where to fetch it.

**Standard practice:** change the module path to match the repository’s URL.  
For example, if you will push to `github.com/your-username/travel-fare-engine`, your `go.mod` should start with:

```
module github.com/your-username/travel-fare-engine
```

Then all your internal imports must use that prefix. For instance, in `main.go` you have:

```go
import "travel-fare-engine/internal/domain/fare"
```

Replace it with:

```go
import "github.com/your-username/travel-fare-engine/internal/domain/fare"
```

If you **don’t** intend to make the repository public (e.g., private company repo), you can keep the local name, but you’ll always need to use `replace` directives when importing it elsewhere. I recommend adopting the full path for consistency and future proofing.

---

## 2. Initialize Git and update the module

```bash
# Navigate to your project root
cd /path/to/travel-fare-engine

# Initialize a git repository
git init

# (Optional but recommended) rename module to match remote repository URL
# Example: if you plan to push to github.com/your-username/travel-fare-engine
go mod edit -module github.com/your-username/travel-fare-engine

# Now update all import paths in your source files to the new prefix.
# You can use an IDE’s refactoring tool or a command like:
find . -name "*.go" -exec sed -i 's|"travel-fare-engine/|"github.com/your-username/travel-fare-engine/|g' {} +

# Make sure everything compiles and dependencies are clean
go mod tidy
go build ./...
```

If you skip the rename and keep `travel-fare-engine`, you can still commit – but future consumers of your module would need a `replace` directive.

---

## 3. Create a robust `.gitignore` file

In the project root, create `.gitignore` with these entries:

```
# Environment file (contains secrets)
.env

# Compiled binary
travel-fare-engine

# OS & editor junk
.DS_Store
Thumbs.db
*.swp
*.swo
*.log

# IDE directories
.vscode/
.idea/

# Go workspace (if used)
go.work
go.work.sum

# Vendor directory (if you vendor dependencies)
vendor/

# Test binaries
*.test

# Coverage output
*.out
```

Make sure the `.env` file is **not** tracked. You can double‑check with `git status`.

---

## 4. Final checks before the first commit

- Verify that `GEMINI_API_KEY` is **never hard‑coded** in your source files. The code should only read it from the environment (`os.Getenv`).
- Run `go mod tidy` and ensure `go.sum` is updated. The `go.sum` file **should** be committed; it’s a lock‑file for dependency integrity.
- Run a final build and a quick manual test to make sure the refactored import paths work.

```bash
go build -o travel-fare-engine ./cmd/server
./travel-fare-engine   # Ctrl+C after it starts
```

---

## 5. Make the first commit

```bash
git add .
git commit -m "Initial commit: travel-fare-engine A2A server with pricing tool"
```

Now you’re ready to push to your remote (e.g., GitHub, GitLab). When you add a remote, the module path will match its location, and everything will be consistent.

---

## Additional note: API keys on Cloud Run later

When you deploy, you won’t use `.env`. Instead, you’ll set the environment variable `GEMINI_API_KEY` in the Cloud Run service configuration, where it’s injected securely into the container. No file needed.
