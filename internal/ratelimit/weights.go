/*
FILE: internal/ratelimit/weights.go

DESCRIPTION:
Request weights and account-tier budgets of the Lighter API, as published on
the official "Rate Limits" page (fetched 2026-09-22). The exchange returns NO
rate-limit headers, so the SDK accounts for the weight of every request on
its own side.

REST (base URL /api/v1, everything except sendTx / sendTxBatch):
  Builder 240 000 weighted / rolling minute; Plus and Premium 24 000 weighted;
  Standard 60 requests (NOT weighted — but whenever
  24000 / endpoint_weight < 60 the weighted figure applies, e.g. 8 per minute
  for changeAccountTier).
  Weights: sendTx / sendTxBatch / nextNonce 6; publicPools, txFromL1TxHash
  50; accountInactiveOrders / accountActiveOrders / accountOrders /
  deposit/latest 100; exchangeMetrics 120; apikeys 150; trades 200;
  transferFeeInfo 500; recentTrades 600; changeAccountTier, tokens/*,
  setAccountMetadata, notification/ack, createIntentAddress, fastwithdraw,
  referral/* 3000; tokens/create 23 000; every other endpoint 300.

sendTx / sendTxBatch (Premium and Plus, REST and WS alike): a separate
bucket checked per L1 address — Premium by staked LIT (0 → 4000, 1 000 →
5000, 3 000 → 6000, 10 000 → 7000, 30 000 → 8000, 100 000 → 12 000,
300 000 → 24 000, 500 000 → 48 000 per minute); Plus 4000. Standard accounts
count sendTx in their 60 requests.

Per transaction type (all tiers): L2Withdraw 2/min, L2CreateSubAccount
2/min, L2CreatePublicPool 2/min, L2UpdateLeverage 40/min, L2ChangePubKey
300/min, L2Transfer 120/min, L2MintShares 1 per 15 s, L2UnstakeAssets 1 per
15 s. The page also lists "Default 40 requests / minute"; applying it to
order transactions would contradict the sendTx buckets, so the SDK does NOT
enforce it by default (Config.DefaultTxTypeLimit, see docs/API-NOTES.md).

Cooldown after 429 / 405: firewall 60 s static; API servers
weight / (total / 60) seconds (300 / (24000 / 60) = 0.75 s for `account`).
*/

package ratelimit

import "github.com/tonymontanov/go-lighter/types"

// Tier — account tier of the rate-limit policy.
type Tier uint8

const (
	// TierStandard — 60 requests per minute, sendTx included.
	TierStandard Tier = iota
	// TierPremium — 24 000 weighted per minute + sendTx bucket by staked LIT.
	TierPremium
	// TierPlus — 24 000 weighted per minute + 4000 sendTx per minute.
	TierPlus
	// TierBuilder — 240 000 weighted per minute; sendTx as Standard.
	TierBuilder
)

// String returns the tier name.
func (t Tier) String() string {
	switch t {
	case TierPremium:
		return "premium"
	case TierPlus:
		return "plus"
	case TierBuilder:
		return "builder"
	default:
		return "standard"
	}
}

const (
	// StandardRequestsPerMinute — unweighted budget of Standard accounts.
	StandardRequestsPerMinute int64 = 60
	// WeightedLimitPerMinute — weighted budget of Premium / Plus accounts.
	WeightedLimitPerMinute int64 = 24_000
	// BuilderWeightedLimitPerMinute — weighted budget of Builder accounts.
	BuilderWeightedLimitPerMinute int64 = 240_000
	// PlusSendTxPerMinute — sendTx budget of Plus accounts.
	PlusSendTxPerMinute int64 = 4_000
	// FirewallCooldownSeconds — static cooldown of the firewall.
	FirewallCooldownSeconds int64 = 60
	// defaultWeight — weight of every endpoint not listed explicitly.
	defaultWeight int64 = 300
)

// Endpoint names (path without the /api/v1/ prefix) used for weights.
const (
	EndpointSendTx      string = "sendTx"
	EndpointSendTxBatch string = "sendTxBatch"
	EndpointNextNonce   string = "nextNonce"
)

// Weight returns the REST weight of an endpoint (path without /api/v1/).
func Weight(endpoint string) int64 {
	switch endpoint {
	case EndpointSendTx, EndpointSendTxBatch, EndpointNextNonce:
		return 6
	case "publicPools", "publicPoolsMetadata", "txFromL1TxHash":
		return 50
	case "accountInactiveOrders", "accountActiveOrders", "accountOrders", "deposit/latest":
		return 100
	case "exchangeMetrics":
		return 120
	case "apikeys":
		return 150
	case "trades":
		return 200
	case "transferFeeInfo":
		return 500
	case "recentTrades":
		return 600
	case "changeAccountTier", "tokens", "tokens/revoke", "setAccountMetadata", "notification/ack", "createIntentAddress", "fastwithdraw",
		"referral/create", "referral/get", "referral/kickback/update", "referral/points", "referral/update", "referral/use", "referral/userReferrals":
		return 3000
	case "tokens/create":
		return 23000
	default:
		return defaultWeight
	}
}

// WeightedLimit returns the weighted per-minute REST budget of a tier.
func WeightedLimit(tier Tier) int64 {
	if tier == TierBuilder {
		return BuilderWeightedLimitPerMinute
	}
	return WeightedLimitPerMinute
}

// PremiumSendTxLimit returns the sendTx budget of a Premium account for the
// given amount of staked LIT (whole tokens).
func PremiumSendTxLimit(stakedLIT int64) int64 {
	switch {
	case stakedLIT >= 500_000:
		return 48_000
	case stakedLIT >= 300_000:
		return 24_000
	case stakedLIT >= 100_000:
		return 12_000
	case stakedLIT >= 30_000:
		return 8_000
	case stakedLIT >= 10_000:
		return 7_000
	case stakedLIT >= 3_000:
		return 6_000
	case stakedLIT >= 1_000:
		return 5_000
	default:
		return 4_000
	}
}

// TxTypeLimit returns the documented per-minute limit of a transaction type,
// or 0 when the type has no dedicated bucket. Types limited per 15 seconds
// are expressed as 4 per minute (the window is one minute).
func TxTypeLimit(txType types.TxType) int64 {
	switch txType {
	case types.TxTypeL2Withdraw, types.TxTypeL2CreateSubAccount, types.TxTypeL2CreatePublicPool:
		return 2
	case types.TxTypeL2UpdateLeverage:
		return 40
	case types.TxTypeL2ChangePubKey:
		return 300
	case types.TxTypeL2Transfer:
		return 120
	case types.TxTypeL2MintShares, types.TxTypeL2UnstakeAssets:
		return 4
	default:
		return 0
	}
}
