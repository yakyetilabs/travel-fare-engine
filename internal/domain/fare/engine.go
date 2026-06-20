package fare

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"
)

// QuoteValidityWindow is how long a returned quote is held before it expires.
// A fare hold is a short, fixed window — it is deliberately NOT tied to how far in
// advance the trip departs (advance_purchase_days), which only affects pricing.
const QuoteValidityWindow = 24 * time.Hour

// Calculate transforms a validated FareQuoteRequest into a FareQuote deterministically.
func Calculate(req FareQuoteRequest) (FareQuote, error) {
	if err := validate(req); err != nil {
		return FareQuote{}, err
	}

	// Compute adult base fare for the route
	baseKey := BaseFareKey{
		CabinClass:   req.CabinClass,
		BookingClass: req.BookingClass,
		RouteType:    req.RouteType,
		SeasonCode:   req.SeasonCode,
	}

	perMileRate, ok := BaseFareMatrix[baseKey]
	if !ok {
		return FareQuote{}, fmt.Errorf("no base fare matrix entry for %v", baseKey)
	}

	rawAdultBaseFare := float64(req.BaseDistanceMiles) * perMileRate

	// Apply advance-purchase discount
	var discountFactor float64
	var apBucketIndex int // Used for FareBasisCode synthesis
	for i, tier := range AdvancePurchaseCurve {
		if req.AdvancePurchaseDays >= tier.MinDays {
			discountFactor = tier.Multiplier
			apBucketIndex = i
			break
		}
	}

	adultBaseFare := rawAdultBaseFare * discountFactor

	// 4. Compute passenger fares, taxes, and build breakdown
	var totalBaseFare float64
	var taxes []TaxLineItem
	var breakdown []string
	var totalTaxAmount float64

	breakdown = append(breakdown, fmt.Sprintf("Base fare: %d miles * $%.4f/mile = $%.2f", req.BaseDistanceMiles, perMileRate, rawAdultBaseFare))
	if discountFactor < 1.0 {
		discountAmt := rawAdultBaseFare - adultBaseFare
		discountPct := (1.0 - discountFactor) * 100
		breakdown = append(breakdown, fmt.Sprintf("Advance purchase discount (%d days): -$%.2f (%.0f%%)", req.AdvancePurchaseDays, discountAmt, discountPct))
	}

	for _, group := range req.Passengers {
		var typeMultiplier float64
		var breakdownMsg string

		switch group.Type {
		case "adult":
			typeMultiplier = AdultFareMultiplier
			groupBase := adultBaseFare * typeMultiplier
			breakdownMsg = fmt.Sprintf("%d adult(s): $%.2f", group.Count, groupBase*float64(group.Count))
		case "child":
			typeMultiplier = ChildFareMultiplier
			groupBase := adultBaseFare * typeMultiplier
			breakdownMsg = fmt.Sprintf("%d child(ren): $%.2f", group.Count, groupBase*float64(group.Count))
		case "infant":
			typeMultiplier = InfantFareMultiplier
			groupBase := adultBaseFare * typeMultiplier
			breakdownMsg = fmt.Sprintf("%d infant(s): $%.2f (%.0f%% of adult base)", group.Count, groupBase*float64(group.Count), typeMultiplier*100)
		}

		groupFare := adultBaseFare * typeMultiplier * float64(group.Count)
		totalBaseFare += groupFare
		breakdown = append(breakdown, breakdownMsg)

		// Taxes lookup
		taxKey := TaxKey{
			RouteType:     req.RouteType,
			PassengerType: group.Type,
		}

		if rules, ok := TaxTables[taxKey]; ok {
			for _, rule := range rules {
				taxAmt := (groupFare * rule.Rate) + (rule.Fixed * float64(group.Count))
				taxAmt = roundToTwoDP(taxAmt)

				totalTaxAmount += taxAmt
				taxes = append(taxes, TaxLineItem{
					Code:   rule.Code,
					Name:   rule.Name,
					Amount: taxAmt,
				})

				breakdown = append(breakdown, fmt.Sprintf("%s (%s): $%.2f", rule.Name, group.Type, taxAmt))
			}
		}
	}

	totalBaseFare = roundToTwoDP(totalBaseFare)
	totalFare := roundToTwoDP(totalBaseFare + totalTaxAmount)
	breakdown = append(breakdown, fmt.Sprintf("Total: $%.2f", totalFare))

	// Synthesize Fare Rules and codes
	// Reverse index so that 0-6 days (index 7) becomes "01", 7-13 days (index 6) becomes "02", etc.
	apBucketStr := fmt.Sprintf("%02d", len(AdvancePurchaseCurve)-apBucketIndex)
	fareBasis := generateFareBasisCode(req.CabinClass, req.BookingClass, req.SeasonCode, req.RouteType, apBucketStr)
	fareRules := generateFareRules(req.BookingClass)

	return FareQuote{
		QuoteID:          generateQuoteID(),
		BaseFare:         totalBaseFare,
		Taxes:            taxes,
		TotalFare:        totalFare,
		Currency:         "USD",
		BookingClass:     req.BookingClass,
		FareBasisCode:    fareBasis,
		FareRules:        fareRules,
		PricingBreakdown: breakdown,
		ExpiresAt:        time.Now().UTC().Add(QuoteValidityWindow).Format(time.RFC3339),
	}, nil
}

func validate(req FareQuoteRequest) error {
	if len(req.Passengers) == 0 {
		return errors.New("passengers: must contain at least one passenger")
	}

	totalPassengers := 0
	for _, p := range req.Passengers {
		if !contains(ValidPassengerTypes, p.Type) {
			return fmt.Errorf("passenger type %q: unknown", p.Type)
		}
		if p.Count < 1 || p.Count > 9 {
			return fmt.Errorf("passenger count %d: must be 1–9", p.Count)
		}
		totalPassengers += p.Count
	}
	if totalPassengers > MaxTotalPassengers {
		return fmt.Errorf("total passenger count %d: must be ≤ %d", totalPassengers, MaxTotalPassengers)
	}

	if !contains(ValidBookingClasses, req.BookingClass) {
		return fmt.Errorf("booking class %q: unknown", req.BookingClass)
	}
	if !contains(ValidCabinClasses, req.CabinClass) {
		return fmt.Errorf("cabin class %q: unknown", req.CabinClass)
	}
	if !contains(ValidRouteTypes, req.RouteType) {
		return fmt.Errorf("route type %q: unknown", req.RouteType)
	}
	if !contains(ValidSeasonCodes, req.SeasonCode) {
		return fmt.Errorf("season code %q: unknown", req.SeasonCode)
	}

	if minDays, ok := BookingAdvancePurchaseMin[req.BookingClass]; ok && req.AdvancePurchaseDays < minDays {
		return fmt.Errorf("booking class %s requires advance purchase >= %d days", req.BookingClass, minDays)
	}

	if req.BaseDistanceMiles < 100 || req.BaseDistanceMiles > 10000 {
		return fmt.Errorf("base distance %d out of range (100-10000)", req.BaseDistanceMiles)
	}
	if req.AdvancePurchaseDays < 0 || req.AdvancePurchaseDays > 365 {
		return fmt.Errorf("advance purchase days %d out of range (0-365)", req.AdvancePurchaseDays)
	}

	return nil
}

func roundToTwoDP(val float64) float64 {
	return math.Round(val*100) / 100
}

func generateFareBasisCode(cabin, booking, season, route, apBucket string) string {
	cabinChar := "Y"
	switch cabin {
	case "economy":
		cabinChar = "E"
	case "premium_economy":
		cabinChar = "P"
	case "business":
		cabinChar = "B"
	case "first":
		cabinChar = "F"
	}

	seasonChar := "L"
	switch season {
	case "low":
		seasonChar = "L"
	case "shoulder":
		seasonChar = "S"
	case "peak":
		seasonChar = "P"
	}

	routeChar := "D"
	if route == "international" {
		routeChar = "I"
	}

	// Format: C + B + S + R + AP (e.g., E B S D 02)
	return fmt.Sprintf("%s%s%s%s%s", cabinChar, booking, seasonChar, routeChar, apBucket)
}

func generateFareRules(bookingClass string) FareRulesSummary {
	rules := FareRulesSummary{
		Refundable:         false,
		Changeable:         false,
		AdvancePurchaseMin: BookingAdvancePurchaseMin[bookingClass], // 0 if class is unrestricted
	}

	switch bookingClass {
	case "Y", "B", "M":
		rules.Refundable = true
		rules.Changeable = true
	case "H":
		rules.Changeable = true
	}
	return rules
}

func generateQuoteID() string {
	b := make([]byte, 16)
	// crypto/rand.Read never returns a short read; it only errors if the system
	// entropy source is unavailable, in which case we fail loudly rather than
	// emit a non-unique / all-zero quote ID for an audit-sensitive output.
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}

func contains(slice []string, item string) bool {
	return slices.Contains(slice, item)
}
