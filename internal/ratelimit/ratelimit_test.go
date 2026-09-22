package ratelimit

import (
	"testing"

	"github.com/tonymontanov/go-lighter/types"
)

func TestWindow(t *testing.T) {
	var second int64 = 1000
	var w = NewWindowWithClock(100, func() int64 { return second })
	if w.Add(10) != 10 || w.Used() != 10 || w.Remaining() != 90 || w.Limit() != 100 {
		t.Fatal("first add")
	}
	second += 30
	if w.Add(20) != 30 {
		t.Fatal("same window")
	}
	second += 31
	if w.Used() != 20 {
		t.Fatalf("first bucket expired: used=%d", w.Used())
	}
	second += 60
	if w.Used() != 0 {
		t.Fatal("all expired")
	}
	w.Add(1000)
	if w.Remaining() != 0 {
		t.Fatal("remaining never negative")
	}
}

func TestWeights(t *testing.T) {
	if Weight("sendTx") != 6 || Weight("nextNonce") != 6 || Weight("apikeys") != 150 || Weight("recentTrades") != 600 || Weight("tokens/create") != 23000 || Weight("account") != 300 {
		t.Fatal("weights table")
	}
	if PremiumSendTxLimit(0) != 4000 || PremiumSendTxLimit(999) != 4000 || PremiumSendTxLimit(1000) != 5000 || PremiumSendTxLimit(500000) != 48000 {
		t.Fatal("premium sendTx table")
	}
	if TxTypeLimit(types.TxTypeL2Withdraw) != 2 || TxTypeLimit(types.TxTypeL2UpdateLeverage) != 40 || TxTypeLimit(types.TxTypeL2CreateOrder) != 0 {
		t.Fatal("tx type table")
	}
	if ParseTier("premium") != TierPremium || ParseTier("PLUS") != TierPlus || ParseTier("Builder") != TierBuilder || ParseTier("") != TierStandard {
		t.Fatal("ParseTier")
	}
	if TierPremium.String() != "premium" || TierStandard.String() != "standard" {
		t.Fatal("Tier.String")
	}
}

func TestStandardLimiter(t *testing.T) {
	var second int64 = 5000
	var events []Event
	var l = New(Config{Tier: TierStandard, Observer: func(e Event) { events = append(events, e) }, Now: func() int64 { return second }})
	var i int
	for i = 0; i < 60; i++ {
		if !l.AdmitREST("orderBooks") {
			t.Fatalf("request %d must be admitted", i)
		}
		l.AccountREST("orderBooks", 200, CategoryMarket)
	}
	if l.AdmitREST("orderBooks") || l.AdmitSendTx(types.TxTypeL2CreateOrder, 1) {
		t.Fatal("61st request must be refused on the standard tier")
	}
	if events[59].UsedRequests != 60 || events[59].RequestLimit != 60 || events[59].UsedWeight != 60*300 {
		t.Fatalf("event = %+v", events[59])
	}
	second += 61
	// changeAccountTier: 24000 / 3000 = 8 per minute even on standard.
	for i = 0; i < 8; i++ {
		if !l.AdmitREST("changeAccountTier") {
			t.Fatalf("tier change %d must be admitted", i)
		}
		l.AccountREST("changeAccountTier", 200, CategoryOther)
	}
	if l.AdmitREST("changeAccountTier") {
		t.Fatal("9th tier change must be refused")
	}
	if l.AdmitREST("orderBooks") != false {
		t.Fatal("weighted window is exhausted")
	}
	var s = l.Snapshot()
	if s.Tier != TierStandard || s.UsedRequests != 8 || s.UsedWeight != 24000 || s.SendTxLimit != 0 {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestPremiumLimiterAndCooldown(t *testing.T) {
	var second int64 = 9000
	var last Event
	var l = New(Config{Tier: TierPremium, StakedLIT: 1000, Observer: func(e Event) { last = e }, Now: func() int64 { return second }})
	if !l.AdmitSendTx(types.TxTypeL2CreateOrder, 1) {
		t.Fatal("first sendTx")
	}
	l.AccountSendTx(EndpointSendTx, TransportREST, 200, types.TxTypeL2CreateOrder, 1, CategoryPlace)
	if last.UsedSendTx != 1 || last.SendTxLimit != 5000 || last.Weight != 0 || last.TxType != types.TxTypeL2CreateOrder {
		t.Fatalf("sendTx event = %+v", last)
	}
	// Leverage bucket: 40 per minute.
	var i int
	for i = 0; i < 40; i++ {
		l.AccountSendTx(EndpointSendTx, TransportWS, 200, types.TxTypeL2UpdateLeverage, 1, CategoryOther)
	}
	if l.AdmitSendTx(types.TxTypeL2UpdateLeverage, 1) {
		t.Fatal("41st leverage update must be refused")
	}
	if !l.AdmitSendTx(types.TxTypeL2CreateOrder, 1) {
		t.Fatal("orders are not limited by the leverage bucket")
	}
	// A 429 on a weight-300 endpoint: 300 / (24000 / 60) = 0.75 s cooldown.
	l.AccountREST("account", 429, CategoryQuery)
	if !l.InCooldown() || last.CooldownUntilMs != second*1000+750 || l.AdmitREST("account") {
		t.Fatalf("cooldown: %v %d", l.InCooldown(), last.CooldownUntilMs)
	}
	second += 1
	if l.InCooldown() {
		t.Fatal("cooldown must expire")
	}
	l.AccountREST("account", 405, CategoryQuery)
	if l.CooldownUntilMs() != second*1000+60_000 {
		t.Fatalf("firewall cooldown = %d", l.CooldownUntilMs())
	}
	var s = l.Snapshot()
	if s.SendTxLimit != 5000 || s.RequestLimit != 0 || s.CooldownUntilMs == 0 {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestDefaultTxTypeLimit(t *testing.T) {
	var l = New(Config{Tier: TierPlus, DefaultTxTypeLimit: 2})
	l.AccountSendTx(EndpointSendTxBatch, TransportREST, 200, types.TxTypeL2CreateOrder, 2, CategoryPlace)
	if l.AdmitSendTx(types.TxTypeL2CreateOrder, 1) {
		t.Fatal("default tx type limit must apply")
	}
	if !l.AdmitSendTx(types.TxTypeL2CancelOrder, 1) {
		t.Fatal("other types unaffected")
	}
}

func BenchmarkAccountSendTx(b *testing.B) {
	var l = New(Config{Tier: TierPremium})
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.AccountSendTx(EndpointSendTx, TransportREST, 200, types.TxTypeL2CreateOrder, 1, CategoryPlace)
	}
}
