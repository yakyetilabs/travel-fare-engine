package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2acompat/a2av0"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"google.golang.org/genai"

	// Correct ADK v1.4.0 Imports
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/agent/workflowagents/sequentialagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adka2a/v2"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"

	"travel-fare-engine/internal/domain/fare"
)

func fareQuoteOutputSchema() *genai.Schema {
	taxLineItem := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"code":   {Type: genai.TypeString},
			"name":   {Type: genai.TypeString},
			"amount": {Type: genai.TypeNumber},
		},
		Required: []string{"code", "name", "amount"},
	}

	fareRules := &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"refundable":           {Type: genai.TypeBoolean},
			"changeable":           {Type: genai.TypeBoolean},
			"advance_purchase_min": {Type: genai.TypeInteger},
		},
		Required: []string{"refundable", "changeable", "advance_purchase_min"},
	}

	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"base_fare": {Type: genai.TypeNumber},
			"taxes": {
				Type:  genai.TypeArray,
				Items: taxLineItem,
			},
			"total_fare":      {Type: genai.TypeNumber},
			"currency":        {Type: genai.TypeString},
			"booking_class":   {Type: genai.TypeString},
			"fare_basis_code": {Type: genai.TypeString},
			"fare_rules":      fareRules,
			"pricing_breakdown": {
				Type:  genai.TypeArray,
				Items: &genai.Schema{Type: genai.TypeString},
			},
			"quote_id":   {Type: genai.TypeString},
			"expires_at": {Type: genai.TypeString},
		},
		Required: []string{
			"base_fare",
			"taxes",
			"total_fare",
			"currency",
			"booking_class",
			"fare_basis_code",
			"fare_rules",
			"pricing_breakdown",
			"quote_id",
			"expires_at",
		},
	}
}

// useVertexAI reports whether the Vertex AI backend was requested via env.
func useVertexAI() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("GOOGLE_GENAI_USE_VERTEXAI")))
	return v == "true" || v == "1"
}

// firstNonEmpty returns the first non-empty string of its arguments.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolvePublicURL determines the externally reachable base URL advertised in the
// agent card. Cloud Run does not inject the service URL automatically, so it is
// passed explicitly via HOST_URL; locally it falls back to http://localhost:<port>.
func resolvePublicURL(port string) string {
	if host := os.Getenv("HOST_URL"); host != "" {
		return host
	}
	return "http://localhost:" + port + "/"
}

// renderAgentCard loads the static agent-card.json and rewrites every advertised
// interface URL to the runtime public URL, so discovery never serves a stale or
// localhost address in production. The static file's URL field is just a default.
func renderAgentCard(path, publicURL string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var card map[string]any
	if err := json.Unmarshal(data, &card); err != nil {
		return nil, err
	}
	// The A2A AgentCard schema uses the top-level "url" (the primary endpoint) plus
	// "additionalInterfaces"; rewrite both to the runtime public URL.
	card["url"] = publicURL
	if ifaces, ok := card["additionalInterfaces"].([]any); ok {
		for _, raw := range ifaces {
			if iface, ok := raw.(map[string]any); ok {
				iface["url"] = publicURL
			}
		}
	}
	return json.MarshalIndent(card, "", "  ")
}

func main() {
	ctx := context.Background()

	// Two model backends, selected by environment:
	//   - Vertex AI (production): GOOGLE_GENAI_USE_VERTEXAI=true, with
	//     GOOGLE_CLOUD_PROJECT + GOOGLE_CLOUD_LOCATION. Auth is Application
	//     Default Credentials (the Cloud Run service account) — no API key.
	//   - AI Studio (local dev): GEMINI_API_KEY.
	// The genai SDK also reads these env vars itself; we branch explicitly so
	// misconfiguration fails fast with a clear message instead of a cryptic 401.
	clientConfig := &genai.ClientConfig{}
	if useVertexAI() {
		project := os.Getenv("GOOGLE_CLOUD_PROJECT")
		location := firstNonEmpty(os.Getenv("GOOGLE_CLOUD_LOCATION"), os.Getenv("GOOGLE_CLOUD_REGION"))
		if project == "" || location == "" {
			log.Fatal("CRITICAL: Vertex AI mode requires GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION")
		}
		clientConfig.Backend = genai.BackendVertexAI
		clientConfig.Project = project
		clientConfig.Location = location
		log.Printf("Using Vertex AI backend (project=%s, location=%s)", project, location)
	} else {
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			log.Fatal("CRITICAL: GEMINI_API_KEY is required (or set GOOGLE_GENAI_USE_VERTEXAI=true for Vertex AI)")
		}
		clientConfig.APIKey = apiKey
		log.Print("Using AI Studio (Gemini API key) backend")
	}

	model, err := gemini.NewModel(ctx, "gemini-2.5-flash", clientConfig)
	if err != nil {
		log.Fatalf("Failed to initialize Gemini model: %v", err)
	}

	computeFareTool, err := functiontool.New(
		functiontool.Config{
			Name:        "compute_fare",
			Description: "Compute a deterministic travel fare quote from a validated FareQuoteRequest. All math is done by the tool; never compute any numbers yourself.",
		},
		func(ctx agent.ToolContext, req fare.FareQuoteRequest) (*fare.FareQuote, error) {
			quote, err := fare.Calculate(req)
			if err != nil {
				return nil, err
			}
			return &quote, nil
		},
	)
	if err != nil {
		log.Fatalf("Failed to create compute_fare tool: %v", err)
	}

	// Initialize Sub-Agents
	pricingAgent, err := llmagent.New(llmagent.Config{
		Name:        "pricing",
		Description: "Validates the fare request and calls the compute_fare tool.",
		Model:       model,
		Instruction: PricingAgentInstruction,
		Tools:       []tool.Tool{computeFareTool},
	})
	if err != nil {
		log.Fatalf("Failed to initialize pricing agent: %v", err)
	}

	formatterAgent, err := llmagent.New(llmagent.Config{
		Name:         "formatter",
		Description:  "Transcribes the tool output into a structured FareQuote.",
		Model:        model,
		Instruction:  FormatterAgentInstruction,
		OutputSchema: fareQuoteOutputSchema(),
	})
	if err != nil {
		log.Fatalf("Failed to initialize formatter agent: %v", err)
	}

	// Sequential Agent Implementation
	rootAgent, err := sequentialagent.New(sequentialagent.Config{
		AgentConfig: agent.Config{
			Name:        "travel_fare_engine",
			Description: "Two-step travel fare quote pipeline: Validation/Pricing -> JSON Formatting",
			SubAgents:   []agent.Agent{pricingAgent, formatterAgent},
		},
	})
	if err != nil {
		log.Fatalf("Failed to create sequential root agent: %v", err)
	}

	// Server wiring
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	agentPath := "/"

	mux := http.NewServeMux()

	// Agent card – rendered once at startup from the static file, with the
	// advertised interface URL rewritten to the runtime public URL (HOST_URL or
	// http://localhost:<port>). Rendering once keeps serving allocation-free.
	publicURL := resolvePublicURL(port)
	agentCardJSON, err := renderAgentCard("agent-card.json", publicURL)
	if err != nil {
		log.Fatalf("Failed to render agent-card.json: %v", err)
	}
	log.Printf("Agent card advertising interface URL: %s", publicURL)
	mux.HandleFunc("/.well-known/agent-card.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(agentCardJSON)
	})

	executor := adka2a.NewExecutor(adka2a.ExecutorConfig{
		RunnerConfig: runner.Config{
			AppName:        rootAgent.Name(),
			Agent:          rootAgent,
			SessionService: session.InMemoryService(),
		},
	})
	requestHandler := a2asrv.NewHandler(executor)
	// Serve the STANDARD A2A JSON-RPC method names (message/send, message/stream, …)
	// via the a2av0 compat handler. The v2-native a2asrv.NewJSONRPCHandler dispatches
	// on gRPC-style names (SendMessage, …), which standard A2A clients — like the
	// orchestrator's a2a-python RemoteA2aAgent — do not send, yielding -32601
	// METHOD_NOT_FOUND.
	mux.Handle(agentPath, a2av0.NewJSONRPCHandler(requestHandler))

	server := &http.Server{Addr: ":" + port, Handler: mux}

	log.Printf("Starting travel-fare-engine A2A server on port %s...", port)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server stopped: %v", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down server...")

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctxShutdown); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}
