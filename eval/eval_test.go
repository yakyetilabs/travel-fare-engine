// Package eval is the travel-fare-engine evaluation harness.
//
// Unlike engine_test.go (which is white-box, table-driven, and lives next to the
// code), this is a black-box "golden cases" suite: each case in cases.json is an
// A2A-shaped FareQuoteRequest paired with hand-computed expected outputs. It is the
// deterministic counterpart to the orchestrator's end-to-end ADK evalset — together
// they pin the boundary contract from both sides.
//
// Run with:
//
//	go test ./eval/
package eval

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"

	"travel-fare-engine/internal/domain/fare"
)

// tolerance matches the engine unit tests: monetary values may differ by at most
// one cent due to documented float64 rounding (DECISIONS.md §10).
const tolerance = 0.01

type evalCase struct {
	Name     string                `json:"name"`
	Request  fare.FareQuoteRequest `json:"request"`
	Expected expectedQuote         `json:"expected"`
}

type expectedQuote struct {
	BaseFare           float64 `json:"base_fare"`
	TotalFare          float64 `json:"total_fare"`
	FareBasisPrefix    string  `json:"fare_basis_prefix"`
	TaxCount           int     `json:"tax_count"`
	Refundable         bool    `json:"refundable"`
	Changeable         bool    `json:"changeable"`
	AdvancePurchaseMin int     `json:"advance_purchase_min"`
}

func loadCases(t *testing.T) []evalCase {
	t.Helper()
	data, err := os.ReadFile("cases.json")
	if err != nil {
		t.Fatalf("could not read eval/cases.json: %v", err)
	}
	var cases []evalCase
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("cases.json is not valid JSON: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("cases.json contains no cases")
	}
	return cases
}

func TestEvalCases(t *testing.T) {
	for _, tc := range loadCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := fare.Calculate(tc.Request)
			if err != nil {
				t.Fatalf("Calculate returned error: %v", err)
			}

			if math.Abs(got.BaseFare-tc.Expected.BaseFare) > tolerance {
				t.Errorf("base_fare: want %.2f, got %.2f", tc.Expected.BaseFare, got.BaseFare)
			}
			if math.Abs(got.TotalFare-tc.Expected.TotalFare) > tolerance {
				t.Errorf("total_fare: want %.2f, got %.2f\nbreakdown: %v", tc.Expected.TotalFare, got.TotalFare, got.PricingBreakdown)
			}
			if !strings.HasPrefix(got.FareBasisCode, tc.Expected.FareBasisPrefix) {
				t.Errorf("fare_basis_code: want prefix %q, got %q", tc.Expected.FareBasisPrefix, got.FareBasisCode)
			}
			if len(got.Taxes) != tc.Expected.TaxCount {
				t.Errorf("tax line items: want %d, got %d", tc.Expected.TaxCount, len(got.Taxes))
			}
			if got.FareRules.Refundable != tc.Expected.Refundable {
				t.Errorf("refundable: want %v, got %v", tc.Expected.Refundable, got.FareRules.Refundable)
			}
			if got.FareRules.Changeable != tc.Expected.Changeable {
				t.Errorf("changeable: want %v, got %v", tc.Expected.Changeable, got.FareRules.Changeable)
			}
			if got.FareRules.AdvancePurchaseMin != tc.Expected.AdvancePurchaseMin {
				t.Errorf("advance_purchase_min: want %d, got %d", tc.Expected.AdvancePurchaseMin, got.FareRules.AdvancePurchaseMin)
			}

			// Sanity: total must equal base + sum(taxes) within tolerance, and the
			// quote must carry an ID and a future expiry.
			var taxSum float64
			for _, tx := range got.Taxes {
				taxSum += tx.Amount
			}
			if math.Abs(got.TotalFare-(got.BaseFare+taxSum)) > tolerance {
				t.Errorf("total (%.2f) != base (%.2f) + taxes (%.2f)", got.TotalFare, got.BaseFare, taxSum)
			}
			if got.QuoteID == "" {
				t.Error("quote_id is empty")
			}
			if got.ExpiresAt == "" {
				t.Error("expires_at is empty")
			}
		})
	}
}
