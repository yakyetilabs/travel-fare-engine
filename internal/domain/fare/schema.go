package fare

// FareQuoteRequest defines the exact A2A boundary contract for incoming pricing requests.
type FareQuoteRequest struct {
	// BaseDistanceMiles: 100–10000 miles, derived by orchestrator.
	BaseDistanceMiles int `json:"base_distance_miles"`
	// AdvancePurchaseDays: Number of days prior to departure, used for fare rules.
	AdvancePurchaseDays int `json:"advance_purchase_days"`
	// Passengers: List of passenger groupings (e.g., 2 adults, 1 child). No omitempty.
	Passengers []PassengerGroup `json:"passengers"`
	// CabinClass: Target cabin (e.g., economy, premium_economy, business, first).
	CabinClass string `json:"cabin_class"`
	// BookingClass: The single-letter booking class (e.g., Y, B, M).
	BookingClass string `json:"booking_class"`
	// RouteType: Geography scope (domestic or international).
	RouteType string `json:"route_type"`
	// SeasonCode: Pricing tier (low, shoulder, peak).
	SeasonCode string `json:"season_code"`
}

// PassengerGroup holds the count and classification of passengers.
type PassengerGroup struct {
	// Count: Range 1–9 passengers, enforced by the engine logic.
	Count int `json:"count"`
	// Type: adult, child, or infant.
	Type string `json:"type"`
}

// FareQuote defines the exact A2A boundary contract for the engine's output.
type FareQuote struct {
	// BaseFare: The computed base price before taxes.
	BaseFare float64 `json:"base_fare"`
	// Taxes: Breakdown of all applied taxes.
	Taxes []TaxLineItem `json:"taxes"`
	// TotalFare: The final sum of BaseFare and Taxes.
	TotalFare float64 `json:"total_fare"`
	// Currency: Three-letter currency code.
	Currency string `json:"currency"`
	// BookingClass: The confirmed single-letter booking class.
	BookingClass string `json:"booking_class"`
	// FareBasisCode: The generated string dictating fare rules.
	FareBasisCode string `json:"fare_basis_code"`
	// FareRules: High-level boolean flags for agent interpretation.
	FareRules FareRulesSummary `json:"fare_rules"`
	// PricingBreakdown: Human-readable steps showing how the math was applied.
	PricingBreakdown []string `json:"pricing_breakdown"`
	// QuoteID: Unique identifier used for booking references.
	QuoteID string `json:"quote_id"`
	// ExpiresAt: ISO 8601 timestamp string representing the quote's expiration.
	ExpiresAt string `json:"expires_at"`
}

// TaxLineItem defines an individual tax calculation.
type TaxLineItem struct {
	// Code: Identifier for the tax (e.g., "US").
	Code string `json:"code"`
	// Amount: The computed monetary value of the tax.
	Amount float64 `json:"amount"`
	// Name: Human-readable name for the tax item.
	Name string `json:"name"`
}

// FareRulesSummary provides agent-friendly policy enforcement indicators.
type FareRulesSummary struct {
	// Refundable: Indicates if the ticket value can be returned.
	Refundable bool `json:"refundable"`
	// Changeable: Indicates if dates/routes can be modified.
	Changeable bool `json:"changeable"`
	// AdvancePurchaseMin: The minimum required days in advance this was purchased.
	AdvancePurchaseMin int `json:"advance_purchase_min"`
}

// Exported Vocabularies / Enums
// These are defined as string slices for use in engine validation and tripwire testing.
var (
	ValidCabinClasses   = []string{"economy", "premium_economy", "business", "first"}
	ValidBookingClasses = []string{"Y", "B", "M", "H", "Q", "G", "K"}
	ValidRouteTypes     = []string{"domestic", "international"}
	ValidSeasonCodes    = []string{"low", "shoulder", "peak"}
	ValidPassengerTypes = []string{"adult", "child", "infant"}
)
