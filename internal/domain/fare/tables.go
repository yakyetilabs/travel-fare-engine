package fare

import "math"

// Note: These tables are stand-ins for real ATPCO (Airline Tariff Publishing Company) data.
// We use static data structures in memory rather than querying
// external dynamic databases for base fares and taxes.

// BaseFareKey uniquely identifies a pricing segment to determine the per-mile base rate.
type BaseFareKey struct {
	CabinClass   string
	BookingClass string
	RouteType    string
	SeasonCode   string
}

// BaseFareMatrix holds the per-mile rate (in USD) for all 168 combinations of the 4 key dimensions.
// Populated at initialization to ensure mathematical consistency across test cases while remaining a pure static lookup map.
var BaseFareMatrix = make(map[BaseFareKey]float64)

// init pre-computes the 168 static base fare combinations.
func init() {
	// Base per-mile rates by cabin and route
	baseRates := map[string]map[string]float64{
		"economy":         {"domestic": 0.10, "international": 0.15},
		"premium_economy": {"domestic": 0.15, "international": 0.22},
		"business":        {"domestic": 0.30, "international": 0.45},
		"first":           {"domestic": 0.50, "international": 0.80},
	}

	// Multipliers for booking classes (Y is full fare, K is deepest discount)
	bookingMultipliers := map[string]float64{
		"Y": 1.00, "B": 0.90, "M": 0.80, "H": 0.70, "Q": 0.60, "G": 0.50, "K": 0.40,
	}

	// Multipliers for seasonality
	seasonMultipliers := map[string]float64{
		"low": 1.00, "shoulder": 1.15, "peak": 1.30,
	}

	for _, cabin := range ValidCabinClasses {
		for _, booking := range ValidBookingClasses {
			for _, route := range ValidRouteTypes {
				for _, season := range ValidSeasonCodes {
					key := BaseFareKey{
						CabinClass:   cabin,
						BookingClass: booking,
						RouteType:    route,
						SeasonCode:   season,
					}
					rate := baseRates[cabin][route] * bookingMultipliers[booking] * seasonMultipliers[season]
					// Round (not truncate) to 4 decimal places for clean floating point storage.
					BaseFareMatrix[key] = math.Round(rate*10000) / 10000
				}
			}
		}
	}
}

// AdvancePurchaseTier maps a minimum number of days prior to departure to a discount multiplier.
type AdvancePurchaseTier struct {
	MinDays    int
	Multiplier float64
}

// AdvancePurchaseCurve defines the discount breakpoints.
// The engine should iterate through this descending list and pick the first tier where Request.AdvancePurchaseDays >= MinDays.
var AdvancePurchaseCurve = []AdvancePurchaseTier{
	{MinDays: 271, Multiplier: 0.80}, // 271-365 days
	{MinDays: 181, Multiplier: 0.82}, // 181-270 days
	{MinDays: 91, Multiplier: 0.85},  // 91-180 days
	{MinDays: 46, Multiplier: 0.88},  // 46-90 days
	{MinDays: 21, Multiplier: 0.92},  // 21-45 days
	{MinDays: 14, Multiplier: 0.95},  // 14-20 days
	{MinDays: 7, Multiplier: 0.98},   // 7-13 days
	{MinDays: 0, Multiplier: 1.00},   // 0-6 days (No discount)
}

// TaxKey maps a tax lookup by geographical scope and the type of passenger.
type TaxKey struct {
	RouteType     string
	PassengerType string
}

// TaxRule defines a single applicable tax. It supports both proportional and flat-fee taxes.
// Final calculation format: Amount = (BaseFare * Rate) + Fixed
type TaxRule struct {
	Code  string
	Name  string
	Rate  float64 // Percentage multiplier (e.g., 0.075 for 7.5%)
	Fixed float64 // Flat currency amount (e.g., 4.50)
}

// TaxTables provides all applicable taxes for the 6 distinct RouteType x PassengerType combinations.
var TaxTables = map[TaxKey][]TaxRule{
	{"domestic", "adult"}: {
		{Code: "US", Name: "U.S. Transportation Tax", Rate: 0.075, Fixed: 0.00},
		{Code: "XF", Name: "Passenger Facility Charge", Rate: 0.000, Fixed: 4.50},
		{Code: "AY", Name: "September 11th Security Fee", Rate: 0.000, Fixed: 5.60},
	},
	{"domestic", "child"}: {
		{Code: "US", Name: "U.S. Transportation Tax", Rate: 0.075, Fixed: 0.00},
		{Code: "XF", Name: "Passenger Facility Charge", Rate: 0.000, Fixed: 4.50},
		{Code: "AY", Name: "September 11th Security Fee", Rate: 0.000, Fixed: 5.60},
	},
	{"domestic", "infant"}: {
		// Lap infants are priced at InfantFareMultiplier of the adult base fare (not free)
		// and are exempt from the per-passenger flat domestic fees (PFC / security).
		{Code: "US", Name: "U.S. Transportation Tax", Rate: 0.000, Fixed: 0.00},
	},
	{"international", "adult"}: {
		{Code: "US", Name: "U.S. International Departure Tax", Rate: 0.000, Fixed: 21.10},
		{Code: "XT", Name: "Foreign Arrival/Misc Taxes", Rate: 0.050, Fixed: 15.00},
		{Code: "AY", Name: "September 11th Security Fee", Rate: 0.000, Fixed: 5.60},
	},
	{"international", "child"}: {
		{Code: "US", Name: "U.S. International Departure Tax", Rate: 0.000, Fixed: 21.10},
		{Code: "XT", Name: "Foreign Arrival/Misc Taxes", Rate: 0.050, Fixed: 15.00},
		{Code: "AY", Name: "September 11th Security Fee", Rate: 0.000, Fixed: 5.60},
	},
	{"international", "infant"}: {
		// Infants traveling internationally usually pay reduced percentages and flat fees.
		{Code: "US", Name: "U.S. International Departure Tax", Rate: 0.000, Fixed: 21.10},
		{Code: "XT", Name: "Foreign Arrival/Misc Taxes", Rate: 0.000, Fixed: 5.00},
	},
}

const (
	// AdultFareMultiplier represents the standard baseline rate multiplier.
	AdultFareMultiplier = 1.00
	// ChildFareMultiplier applies a standard 25% discount to the adult base fare.
	ChildFareMultiplier = 0.75
	// InfantFareMultiplier applies to lap infants (typically 10% of the adult fare).
	InfantFareMultiplier = 0.10
)

// MaxTotalPassengers is the contract cap on the sum of all passenger group counts
// in a single request (see DECISIONS.md §3: "min 1 group, total count ≤ 9").
const MaxTotalPassengers = 9

// BookingAdvancePurchaseMin is the single source of truth for the minimum
// advance-purchase days each restricted booking class requires. It is consumed by
// both validate() (rejection) and generateFareRules() (advertised rule) so the two
// can never drift. Classes absent from this map have no advance-purchase minimum.
var BookingAdvancePurchaseMin = map[string]int{
	"G": 21,
	"Q": 14,
	"K": 7,
}
