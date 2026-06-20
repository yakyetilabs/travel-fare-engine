package fare_test

import (
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"travel-fare-engine/internal/domain/fare"
)

// --- Test Helpers ---

func makeValidRequest() fare.FareQuoteRequest {
	return fare.FareQuoteRequest{
		Passengers:          []fare.PassengerGroup{{Count: 1, Type: "adult"}},
		BaseDistanceMiles:   1000,
		AdvancePurchaseDays: 30,
		BookingClass:        "Y",
		CabinClass:          "economy",
		RouteType:           "domestic",
		SeasonCode:          "low",
	}
}

func assertValid(t *testing.T, req fare.FareQuoteRequest) fare.FareQuote {
	t.Helper()
	quote, err := fare.Calculate(req)
	if err != nil {
		t.Fatalf("expected valid request, got error: %v", err)
	}
	return quote
}

func assertError(t *testing.T, req fare.FareQuoteRequest, containsStr string) {
	t.Helper()
	_, err := fare.Calculate(req)
	if err == nil {
		t.Fatalf("expected error containing %q, got nil", containsStr)
	}
	if !strings.Contains(err.Error(), containsStr) {
		t.Errorf("expected error containing %q, got: %v", containsStr, err)
	}
}

// --- Table-Driven Happy Path Tests ---

func TestCalculate_HappyPaths(t *testing.T) {
	tests := []struct {
		name             string
		modifier         func(*fare.FareQuoteRequest)
		expectedTotal    float64
		expectedBaseFare float64
		expectedCode     string
	}{
		{
			name:     "Single adult, economy/Y/domestic/low, 1000 miles, 30 AP",
			modifier: func(r *fare.FareQuoteRequest) {},
			// Arithmetic:
			// Rate: 0.10 (econ/dom) * 1.0 (Y) * 1.0 (low) = 0.10. Base = 100.
			// AP: 30 days hits {MinDays: 21, Multiplier: 0.92}. Base = 92.00.
			// Taxes (Dom/Adult): US (92*0.075=6.90) + XF (4.50) + AY (5.60) = 17.00.
			// Total: 92.00 + 17.00 = 109.00
			expectedBaseFare: 92.00,
			expectedTotal:    109.00,
			expectedCode:     "EYLD04", // Index 4 mapped to 8-4="04"
		},
		{
			name: "Booking class G with 30 days AP",
			modifier: func(r *fare.FareQuoteRequest) {
				r.BookingClass = "G"
			},
			// Rate: 0.10 * 0.50 (G) * 1.0 = 0.05. Base = 50.
			// AP: 30 days (0.92). Base = 46.00.
			// Taxes: US (46*0.075=3.45) + XF (4.50) + AY (5.60) = 13.55.
			// Total: 46.00 + 13.55 = 59.55.
			expectedBaseFare: 46.00,
			expectedTotal:    59.55,
			expectedCode:     "EGLD04",
		},
		{
			name: "Mixed passengers (2 Ad, 1 Ch, 1 Inf), Econ/Y/Intl/Low, 1000m, 60 AP",
			modifier: func(r *fare.FareQuoteRequest) {
				r.RouteType = "international"
				r.AdvancePurchaseDays = 60
				r.Passengers = []fare.PassengerGroup{
					{Count: 2, Type: "adult"},
					{Count: 1, Type: "child"},
					{Count: 1, Type: "infant"},
				}
			},
			// Arithmetic:
			// Rate: 0.15 (econ/intl) * 1.0 (Y) * 1.0 (low) = 0.15. Raw Base = 150.
			// AP: 60 days hits {MinDays: 46, Multiplier: 0.88}. Adult Base = 132.00.
			// Fares: Adult(132*2=264), Child(132*0.75*1=99), Infant(132*0.10*1=13.20). Sum Base = 376.20.
			// Taxes Adult(2): US(21.10*2=42.20), XT(264*0.05+15*2=43.20), AY(5.60*2=11.20) = 96.60
			// Taxes Child(1): US(21.10), XT(99*0.05+15=19.95), AY(5.60) = 46.65
			// Taxes Infant(1): US(21.10), XT(0+5.00=5.00) = 26.10
			// Total Taxes: 96.60 + 46.65 + 26.10 = 169.35
			// Total Fare: 376.20 + 169.35 = 545.55
			expectedBaseFare: 376.20,
			expectedTotal:    545.55,
			expectedCode:     "EYLI05", // Index 3 -> 8-3="05"
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := makeValidRequest()
			tc.modifier(&req)

			quote := assertValid(t, req)

			if math.Abs(quote.BaseFare-tc.expectedBaseFare) > 0.01 {
				t.Errorf("Expected BaseFare %.2f, got %.2f", tc.expectedBaseFare, quote.BaseFare)
			}
			if math.Abs(quote.TotalFare-tc.expectedTotal) > 0.01 {
				t.Errorf("Expected TotalFare %.2f, got %.2f\nBreakdown: %v", tc.expectedTotal, quote.TotalFare, quote.PricingBreakdown)
			}
			if !strings.HasPrefix(quote.FareBasisCode, tc.expectedCode[:4]) {
				t.Errorf("Expected FareBasisCode starting with %s, got %s", tc.expectedCode[:4], quote.FareBasisCode)
			}

			_, err := time.Parse(time.RFC3339, quote.ExpiresAt)
			if err != nil {
				t.Errorf("ExpiresAt is not valid RFC3339: %v", err)
			}
			if quote.QuoteID == "" {
				t.Errorf("QuoteID must not be empty")
			}
		})
	}
}

// --- Infant-Only (No Policy Leak) Test ---

func TestCalculate_InfantOnly(t *testing.T) {
	req := makeValidRequest()
	req.Passengers = []fare.PassengerGroup{{Count: 1, Type: "infant"}}

	quote := assertValid(t, req)

	// Adult base fare = 92. Infant multiplier = 0.10. Infant base = 9.20.
	if quote.BaseFare <= 0 {
		t.Errorf("Infant base fare must be > 0, got %.2f", quote.BaseFare)
	}

	breakdownFound := false
	for _, b := range quote.PricingBreakdown {
		if strings.Contains(b, "10% of adult base") {
			breakdownFound = true
			break
		}
	}
	if !breakdownFound {
		t.Errorf("PricingBreakdown missing infant calculation explanation: %v", quote.PricingBreakdown)
	}
}

// --- Quote Expiry Tests ---

// The quote hold window is a short, fixed duration and must NOT scale with
// advance_purchase_days. In particular, advance_purchase_days=0 must still
// produce a quote that expires in the future (not "now").
func TestCalculate_ExpiresAtIsFixedWindow(t *testing.T) {
	for _, apDays := range []int{0, 30, 365} {
		req := makeValidRequest()
		req.AdvancePurchaseDays = apDays
		before := time.Now().UTC()

		quote := assertValid(t, req)

		exp, err := time.Parse(time.RFC3339, quote.ExpiresAt)
		if err != nil {
			t.Fatalf("AP=%d: ExpiresAt not RFC3339: %v", apDays, err)
		}
		if !exp.After(before) {
			t.Errorf("AP=%d: quote already expired at issue time (expires_at=%s)", apDays, quote.ExpiresAt)
		}
		// Should be the fixed hold window, never days/years out.
		if d := exp.Sub(before); d > fare.QuoteValidityWindow+time.Minute {
			t.Errorf("AP=%d: expiry %v exceeds fixed window %v (must not scale with advance purchase)", apDays, d, fare.QuoteValidityWindow)
		}
	}
}

// --- Validation Rejection Tests ---

func TestCalculate_ValidationRejections(t *testing.T) {
	cases := []struct {
		name        string
		modifier    func(*fare.FareQuoteRequest)
		expectedErr string
	}{
		{"Empty passengers", func(r *fare.FareQuoteRequest) { r.Passengers = nil }, "at least one passenger"},
		{"Unknown passenger type", func(r *fare.FareQuoteRequest) { r.Passengers[0].Type = "senior" }, "unknown"},
		{"Count = 0", func(r *fare.FareQuoteRequest) { r.Passengers[0].Count = 0 }, "must be 1–9"},
		{"Count = 10", func(r *fare.FareQuoteRequest) { r.Passengers[0].Count = 10 }, "must be 1–9"},
		{"Total passengers > 9", func(r *fare.FareQuoteRequest) {
			r.Passengers = []fare.PassengerGroup{{Count: 9, Type: "adult"}, {Count: 9, Type: "child"}}
		}, "total passenger count"},
		{"Unknown booking class", func(r *fare.FareQuoteRequest) { r.BookingClass = "Z" }, "unknown"},
		{"G class AP < 21", func(r *fare.FareQuoteRequest) { r.BookingClass = "G"; r.AdvancePurchaseDays = 7 }, "advance purchase >= 21"},
		{"Q class AP < 14", func(r *fare.FareQuoteRequest) { r.BookingClass = "Q"; r.AdvancePurchaseDays = 13 }, "advance purchase >= 14"},
		{"K class AP < 7", func(r *fare.FareQuoteRequest) { r.BookingClass = "K"; r.AdvancePurchaseDays = 6 }, "advance purchase >= 7"},
		{"Distance < 100", func(r *fare.FareQuoteRequest) { r.BaseDistanceMiles = 99 }, "out of range"},
		{"Distance > 10000", func(r *fare.FareQuoteRequest) { r.BaseDistanceMiles = 10001 }, "out of range"},
		{"AP days < 0", func(r *fare.FareQuoteRequest) { r.AdvancePurchaseDays = -1 }, "out of range"},
		{"AP days > 365", func(r *fare.FareQuoteRequest) { r.AdvancePurchaseDays = 366 }, "out of range"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := makeValidRequest()
			tc.modifier(&req)
			assertError(t, req, tc.expectedErr)
		})
	}
}

// --- Tripwire Tests ---

func TestTripwire_AllEnumValuesAccepted(t *testing.T) {
	for _, cc := range fare.ValidCabinClasses {
		req := makeValidRequest()
		req.CabinClass = cc
		assertValid(t, req)
	}
	for _, bc := range fare.ValidBookingClasses {
		req := makeValidRequest()
		req.BookingClass = bc
		req.AdvancePurchaseDays = 30 // safely bypass validation for G/Q
		assertValid(t, req)
	}
	for _, rt := range fare.ValidRouteTypes {
		req := makeValidRequest()
		req.RouteType = rt
		assertValid(t, req)
	}
	for _, sc := range fare.ValidSeasonCodes {
		req := makeValidRequest()
		req.SeasonCode = sc
		assertValid(t, req)
	}
	for _, pt := range fare.ValidPassengerTypes {
		req := makeValidRequest()
		req.Passengers = []fare.PassengerGroup{{Count: 1, Type: pt}}
		assertValid(t, req)
	}
}

func TestTripwire_UnknownValuesRejected(t *testing.T) {
	req1 := makeValidRequest()
	req1.CabinClass = "superfirst"
	assertError(t, req1, "unknown")
	req2 := makeValidRequest()
	req2.BookingClass = "X"
	assertError(t, req2, "unknown")
	req3 := makeValidRequest()
	req3.RouteType = "moon"
	assertError(t, req3, "unknown")
	req4 := makeValidRequest()
	req4.SeasonCode = "winter"
	assertError(t, req4, "unknown")
	req5 := makeValidRequest()
	req5.Passengers[0].Type = "pet"
	assertError(t, req5, "unknown")
}

// --- Fare Rules Tests ---

func TestFareRulesMapping(t *testing.T) {
	tests := []struct {
		bookingClass string
		refundable   bool
		changeable   bool
		apMin        int
	}{
		{"Y", true, true, 0},
		{"M", true, true, 0},
		{"H", false, true, 0},
		{"Q", false, false, 14},
		{"K", false, false, 7},
		{"G", false, false, 21},
	}

	for _, tc := range tests {
		t.Run("Class "+tc.bookingClass, func(t *testing.T) {
			req := makeValidRequest()
			req.BookingClass = tc.bookingClass
			req.AdvancePurchaseDays = 30

			quote := assertValid(t, req)

			if quote.FareRules.Refundable != tc.refundable {
				t.Errorf("expected refundable %v, got %v", tc.refundable, quote.FareRules.Refundable)
			}
			if quote.FareRules.Changeable != tc.changeable {
				t.Errorf("expected changeable %v, got %v", tc.changeable, quote.FareRules.Changeable)
			}
			if quote.FareRules.AdvancePurchaseMin != tc.apMin {
				t.Errorf("expected AP Min %d, got %d", tc.apMin, quote.FareRules.AdvancePurchaseMin)
			}
		})
	}
}

// --- Concurrency Safety Test ---

func TestCalculate_ConcurrencySafe(t *testing.T) {
	var wg sync.WaitGroup
	req := makeValidRequest()

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			localReq := req
			if id%2 == 0 {
				localReq.AdvancePurchaseDays = 5
				localReq.BookingClass = "Y"
			}

			_, err := fare.Calculate(localReq)
			if err != nil {
				t.Errorf("goroutine %d failed unexpectedly: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
}
