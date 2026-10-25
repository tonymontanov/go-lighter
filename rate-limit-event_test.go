/*
FILE: rate-limit-event_test.go

DESCRIPTION:
Tier helpers of the rate-limit model: tier names as the exchange / operators
spell them and the documented per-minute budgets of every tier.
*/

package lighter

import "testing"

func TestTierFromName(t *testing.T) {
	var cases = []struct {
		name string
		tier RateLimitTier
		ok   bool
	}{
		{"standard", RateLimitTierStandard, true},
		{"Standard", RateLimitTierStandard, true},
		{"PREMIUM", RateLimitTierPremium, true},
		{"plus", RateLimitTierPlus, true},
		{"Builder", RateLimitTierBuilder, true},
		{"", RateLimitTierStandard, false},
		{"vip", RateLimitTierStandard, false},
	}
	var i int
	for i = 0; i < len(cases); i++ {
		var tier, ok = TierFromName(cases[i].name)
		if tier != cases[i].tier || ok != cases[i].ok {
			t.Errorf("TierFromName(%q) = %v, %v; want %v, %v", cases[i].name, tier, ok, cases[i].tier, cases[i].ok)
		}
	}
}

func TestTierBudget(t *testing.T) {
	var standard RateLimitBudget = TierBudget(RateLimitTierStandard, 0)
	if standard.WeightedPerMinute != 24_000 || standard.RequestsPerMinute != 60 || standard.SendTxPerMinute != 0 {
		t.Fatalf("standard = %+v", standard)
	}
	var premium RateLimitBudget = TierBudget(RateLimitTierPremium, 3_000)
	if premium.WeightedPerMinute != 24_000 || premium.RequestsPerMinute != 0 || premium.SendTxPerMinute != 6_000 {
		t.Fatalf("premium(3000 LIT) = %+v", premium)
	}
	if TierBudget(RateLimitTierPremium, 0).SendTxPerMinute != 4_000 {
		t.Fatal("premium without stake must get the 4000 bucket")
	}
	var plus RateLimitBudget = TierBudget(RateLimitTierPlus, 1_000_000)
	if plus.WeightedPerMinute != 24_000 || plus.RequestsPerMinute != 0 || plus.SendTxPerMinute != 4_000 {
		t.Fatalf("plus = %+v", plus)
	}
	var builder RateLimitBudget = TierBudget(RateLimitTierBuilder, 0)
	if builder.WeightedPerMinute != 240_000 || builder.RequestsPerMinute != 0 || builder.SendTxPerMinute != 0 {
		t.Fatalf("builder = %+v", builder)
	}
}
