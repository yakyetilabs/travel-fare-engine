package fare_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"travel-fare-engine/internal/domain/fare"
)

// agentCardPath resolves agent-card.json at the repo root, relative to this
// package directory (internal/domain/fare → repo root is three levels up).
const agentCardPath = "../../../agent-card.json"

// schemaProperty models the subset of a JSON Schema property we assert on.
type schemaProperty struct {
	Type  string        `json:"type"`
	Enum  []string      `json:"enum"`
	Items *objectSchema `json:"items"`
}

type objectSchema struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties"`
}

type agentCard struct {
	Name   string `json:"name"`
	Skills []struct {
		Name        string       `json:"name"`
		InputSchema objectSchema `json:"inputSchema"`
	} `json:"skills"`
}

func loadAgentCard(t *testing.T) agentCard {
	t.Helper()
	abs, err := filepath.Abs(agentCardPath)
	if err != nil {
		t.Fatalf("resolving agent-card.json path: %v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatalf("could not read agent-card.json at %s: %v", abs, err)
	}
	var card agentCard
	if err := json.Unmarshal(data, &card); err != nil {
		t.Fatalf("agent-card.json is not valid JSON: %v", err)
	}
	return card
}

// TestTripwire_AgentCardVocabularies enforces the boundary contract described in
// CLAUDE.md: the enum vocabularies advertised in agent-card.json must exactly
// match the exported slices the engine validates against. If either side adds or
// removes a value without the other, this fails at build time.
func TestTripwire_AgentCardVocabularies(t *testing.T) {
	card := loadAgentCard(t)

	if len(card.Skills) == 0 {
		t.Fatal("agent-card.json contains no skills")
	}
	skill := card.Skills[0]
	if skill.Name != "compute_fare" {
		t.Errorf("expected skill name 'compute_fare', got %q", skill.Name)
	}

	props := skill.InputSchema.Properties
	if props == nil {
		t.Fatal("agent-card.json skill inputSchema has no properties")
	}

	assertEnum(t, "cabin_class", props["cabin_class"].Enum, fare.ValidCabinClasses)
	assertEnum(t, "booking_class", props["booking_class"].Enum, fare.ValidBookingClasses)
	assertEnum(t, "route_type", props["route_type"].Enum, fare.ValidRouteTypes)
	assertEnum(t, "season_code", props["season_code"].Enum, fare.ValidSeasonCodes)

	// passengers.items.properties.type.enum is nested one level into the array.
	passengers := props["passengers"]
	if passengers.Items == nil {
		t.Fatal("agent-card.json passengers property has no items schema")
	}
	assertEnum(t, "passengers.items.type", passengers.Items.Properties["type"].Enum, fare.ValidPassengerTypes)
}

// assertEnum compares two vocabularies as sets (order-independent).
func assertEnum(t *testing.T, field string, got, want []string) {
	t.Helper()
	gc := append([]string(nil), got...)
	wc := append([]string(nil), want...)
	sort.Strings(gc)
	sort.Strings(wc)
	if len(gc) != len(wc) {
		t.Errorf("%s: agent-card.json has %v but code has %v", field, got, want)
		return
	}
	for i := range gc {
		if gc[i] != wc[i] {
			t.Errorf("%s: agent-card.json has %v but code has %v", field, got, want)
			return
		}
	}
}
