/*
FILE: internal/tx/vectors_test.go

DESCRIPTION:
Byte-for-byte parity tests against the official Go SDK (lighter-go). For
every vector the transaction is built with THIS package and three things are
compared with the reference output:
 1. the transaction hash (Poseidon2, with attribute aggregation);
 2. the Schnorr signature for the fixed scalar k of the generator;
 3. the tx_info JSON, character for character.
The reference signature is also verified with the exchange's verifier
against the public key printed by the generator.

buildVectorTxs mirrors the CASES table of scripts/gen-vectors/main.go.
*/

package tx

import (
	"encoding/hex"
	"testing"

	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// testPrivateKey — the fixed test key shared with the generator. Not
// registered on any account.
const testPrivateKey string = "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728"

type generatedVector struct {
	name      string
	chainID   uint32
	txType    uint8
	hashHex   string
	sigBase64 string
	info      string
}

const (
	vecAccount     int64 = 1
	vecAPIKey      uint8 = 2
	vecExpiredAt   int64 = 1700000000000
	vecOrderExpiry int64 = 1800000000000
)

func head(nonce int64, attrs Attributes) Header {
	return Header{AccountIndex: vecAccount, APIKeyIndex: vecAPIKey, ExpiredAt: vecExpiredAt, Nonce: nonce, Attributes: attrs}
}

func orderInfo(market int16, cli int64, base int64, price uint32, isAsk uint8, typ types.OrderType, tif types.TimeInForce, ro uint8, trigger uint32, expiry int64) OrderInfo {
	return OrderInfo{MarketIndex: market, ClientOrderIndex: cli, BaseAmount: base, Price: price, IsAsk: isAsk, Type: typ, TimeInForce: tif, ReduceOnly: ro, TriggerPrice: trigger, OrderExpiry: expiry}
}

// buildVectorTxs mirrors cases() of scripts/gen-vectors/main.go.
func buildVectorTxs() map[string]Tx {
	return map[string]Tx{
		"createOrder limit gtt":                     &CreateOrder{Header: head(7, Attributes{}), Order: orderInfo(0, 1, 1000, 405000, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)},
		"createOrder market ioc skip nonce":         &CreateOrder{Header: head(1700000000123, Attributes{SkipNonce: true}), Order: orderInfo(1, 2, 500, 3000000, 1, types.OrderTypeMarket, types.TimeInForceIOC, 0, 0, 0)},
		"createOrder post-only integrator":          &CreateOrder{Header: head(9, Attributes{IntegratorAccountIndex: 77, IntegratorTakerFee: 100, IntegratorMakerFee: 50}), Order: orderInfo(2, 0, 20000, 123, 0, types.OrderTypeLimit, types.TimeInForcePostOnly, 0, 0, vecOrderExpiry)},
		"createOrder stop loss reduce only testnet": &CreateOrder{Header: head(10, Attributes{}), Order: orderInfo(0, 3, 0, 380000, 1, types.OrderTypeStopLoss, types.TimeInForceIOC, 1, 390000, vecOrderExpiry)},
		"createOrder take profit limit self trade":  &CreateOrder{Header: head(11, Attributes{SelfTradeBehaviorMode: SelfTradeBehaviorCancelBoth, SelfTradeEqualityMode: SelfTradeEqualityMasterAccountIndex}), Order: orderInfo(3, 4, 10, 250, 0, types.OrderTypeTakeProfitLimit, types.TimeInForceGTT, 0, 240, vecOrderExpiry)},
		"cancelOrder":                           &CancelOrder{Header: head(12, Attributes{}), MarketIndex: 0, Index: 1},
		"cancelOrder by order index skip nonce": &CancelOrder{Header: head(1700000000456, Attributes{SkipNonce: true}), MarketIndex: 5, Index: 281477872907039},
		"cancelAll immediate":                   &CancelAllOrders{Header: head(13, Attributes{}), TimeInForce: types.CancelAllImmediate, Time: 0},
		"cancelAll scheduled":                   &CancelAllOrders{Header: head(14, Attributes{}), TimeInForce: types.CancelAllScheduled, Time: 1700000600000},
		"cancelAll abort":                       &CancelAllOrders{Header: head(15, Attributes{}), TimeInForce: types.CancelAllAbort, Time: 0},
		"cancelAll immediate market scope":      &CancelAllOrders{Header: head(16, Attributes{HasCancelAllMarket: true, CancelAllMarketIndex: 0}), TimeInForce: types.CancelAllImmediate, Time: 0},
		"modifyOrder":                           &ModifyOrder{Header: head(17, Attributes{}), MarketIndex: 0, Index: 1, BaseAmount: 1100, Price: 410000, TriggerPrice: 0},
		"modifyOrder version skip nonce":        &ModifyOrder{Header: head(1700000000789, Attributes{SkipNonce: true, OrderVersion: 5}), MarketIndex: 0, Index: 281477872907039, BaseAmount: 1200, Price: 420000, TriggerPrice: 0},
		"updateLeverage cross":                  &UpdateLeverage{Header: head(18, Attributes{}), MarketIndex: 0, InitialMarginFraction: 500, MarginMode: types.MarginModeCross},
		"updateLeverage isolated testnet":       &UpdateLeverage{Header: head(19, Attributes{}), MarketIndex: 7, InitialMarginFraction: 10000, MarginMode: types.MarginModeIsolated},
		"groupedOrders otoco": &CreateGroupedOrders{Header: head(20, Attributes{}), GroupingType: types.GroupingOneTriggersOneCancelsTheOther, Orders: []OrderInfo{
			orderInfo(0, 21, 1000, 405000, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry),
			orderInfo(0, 22, 0, 380000, 1, types.OrderTypeStopLoss, types.TimeInForceIOC, 1, 390000, vecOrderExpiry),
			orderInfo(0, 23, 0, 430000, 1, types.OrderTypeTakeProfit, types.TimeInForceIOC, 1, 420000, vecOrderExpiry),
		}},
		"groupedOrders oco": &CreateGroupedOrders{Header: head(21, Attributes{}), GroupingType: types.GroupingOneCancelsTheOther, Orders: []OrderInfo{
			orderInfo(1, 31, 500, 2900000, 1, types.OrderTypeStopLossLimit, types.TimeInForceGTT, 1, 2950000, vecOrderExpiry),
			orderInfo(1, 32, 500, 3300000, 1, types.OrderTypeTakeProfit, types.TimeInForceIOC, 1, 3250000, vecOrderExpiry),
		}},
		"updateMargin add":          &UpdateMargin{Header: head(22, Attributes{}), MarketIndex: 0, USDCAmount: 5000000, Direction: MarginDirectionAdd},
		"updateMargin remove large": &UpdateMargin{Header: head(23, Attributes{}), MarketIndex: 0, USDCAmount: 1<<40 + 12345, Direction: MarginDirectionRemove},
	}
}

func TestTransactionsMatchLighterGo(t *testing.T) {
	var signer, err = signing.NewSigner(testPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	if signer.PublicKeyHex() != generatedPublicKeyHex {
		t.Fatalf("public key mismatch: got %s want %s", signer.PublicKeyHex(), generatedPublicKeyHex)
	}
	var k, kErr = hex.DecodeString(generatedNonceScalarHex)
	if kErr != nil {
		t.Fatal(kErr)
	}
	var txs = buildVectorTxs()
	if len(txs) != len(generatedVectors) {
		t.Fatalf("builders (%d) and generated vectors (%d) are out of sync", len(txs), len(generatedVectors))
	}
	var publicKey = signer.PublicKey()

	for _, vector := range generatedVectors {
		var transaction, ok = txs[vector.name]
		if !ok {
			t.Errorf("%s: no Go builder for this vector", vector.name)
			continue
		}
		if uint8(transaction.Type()) != vector.txType {
			t.Errorf("%s: tx type %d, want %d", vector.name, transaction.Type(), vector.txType)
		}
		if err = transaction.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", vector.name, err)
			continue
		}
		var hash signing.Hash
		transaction.Hash(vector.chainID, &hash)
		if got := hash.Hex(); got != vector.hashHex {
			t.Errorf("%s: hash mismatch\n got %s\nwant %s", vector.name, got, vector.hashHex)
			continue
		}
		var sig signing.Signature
		if err = signer.SignWithNonce(&hash, k, &sig); err != nil {
			t.Fatal(err)
		}
		if got := string(sig.AppendBase64(nil)); got != vector.sigBase64 {
			t.Errorf("%s: signature mismatch\n got %s\nwant %s", vector.name, got, vector.sigBase64)
		}
		if !signing.Verify(&publicKey, &hash, &sig) {
			t.Errorf("%s: signature does not verify", vector.name)
		}
		if got := string(transaction.AppendInfo(nil, &sig)); got != vector.info {
			t.Errorf("%s: tx_info mismatch\n got %s\nwant %s", vector.name, got, vector.info)
		}
	}
}

func TestValidationRejects(t *testing.T) {
	var cases = []struct {
		name string
		tx   Tx
		want error
	}{
		{"market with expiry", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeMarket, types.TimeInForceIOC, 0, 0, vecOrderExpiry)}, ErrOrderExpiryInvalid},
		{"gtt without expiry", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, 0)}, ErrOrderExpiryInvalid},
		{"ioc with expiry", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceIOC, 0, 0, vecOrderExpiry)}, ErrOrderExpiryInvalid},
		{"limit with trigger", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 5, vecOrderExpiry)}, ErrOrderTriggerPriceInvalid},
		{"zero price", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 0, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrPriceTooLow},
		{"zero size not reduce only", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 0, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrBaseAmountTooLow},
		{"market index 255", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(255, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrInvalidMarketIndex},
		{"stop loss without trigger", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeStopLoss, types.TimeInForceIOC, 0, 0, vecOrderExpiry)}, ErrOrderTriggerPriceInvalid},
		{"twap not gtt", &CreateOrder{Header: head(1, Attributes{}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeTWAP, types.TimeInForceIOC, 0, 0, vecOrderExpiry)}, ErrOrderTimeInForceInvalid},
		{"api key 255 on create", &CreateOrder{Header: Header{AccountIndex: 1, APIKeyIndex: 255, ExpiredAt: vecExpiredAt, Nonce: 1}, Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrAPIKeyIndexTooHigh},
		{"negative nonce", &CancelOrder{Header: Header{AccountIndex: 1, APIKeyIndex: 2, ExpiredAt: vecExpiredAt, Nonce: -1}, MarketIndex: 0, Index: 1}, ErrNonceTooLow},
		{"cancel index zero", &CancelOrder{Header: head(1, Attributes{}), MarketIndex: 0, Index: 0}, ErrOrderIndexTooLow},
		{"scheduled cancel with market scope", &CancelAllOrders{Header: head(1, Attributes{HasCancelAllMarket: true, CancelAllMarketIndex: 3}), TimeInForce: types.CancelAllScheduled, Time: 5}, ErrCancelAllMarketIndexCantBeSchedule},
		{"immediate cancel with time", &CancelAllOrders{Header: head(1, Attributes{}), TimeInForce: types.CancelAllImmediate, Time: 5}, ErrCancelAllTimeIsNotNil},
		{"scheduled cancel without time", &CancelAllOrders{Header: head(1, Attributes{}), TimeInForce: types.CancelAllScheduled, Time: 0}, ErrCancelAllTimeIsNotInRange},
		{"leverage fraction zero", &UpdateLeverage{Header: head(1, Attributes{}), MarketIndex: 0, InitialMarginFraction: 0}, ErrInitialMarginFractionTooLow},
		{"leverage bad mode", &UpdateLeverage{Header: head(1, Attributes{}), MarketIndex: 0, InitialMarginFraction: 100, MarginMode: 2}, ErrInvalidMarginMode},
		{"fees without integrator", &CreateOrder{Header: head(1, Attributes{IntegratorTakerFee: 5}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrIntegratorAccountIndexRequiredForFees},
		{"self trade with fees", &CreateOrder{Header: head(1, Attributes{IntegratorAccountIndex: 3, IntegratorTakerFee: 5, SelfTradeBehaviorMode: 1}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrSelfTradeSpecificationNotAllowedWithFees},
		{"reduce with master equality", &CreateOrder{Header: head(1, Attributes{SelfTradeBehaviorMode: SelfTradeBehaviorReduce, SelfTradeEqualityMode: SelfTradeEqualityMasterAccountIndex}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrReduceModeNotAllowedWithMasterEqualityMode},
		{"too many attributes", &CreateOrder{Header: head(1, Attributes{IntegratorAccountIndex: 3, IntegratorTakerFee: 5, IntegratorMakerFee: 5, SkipNonce: true, OrderVersion: 2}), Order: orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}, ErrTooManyAttributes},
		{"grouped wrong size", &CreateGroupedOrders{Header: head(1, Attributes{}), GroupingType: types.GroupingOneCancelsTheOther, Orders: []OrderInfo{orderInfo(0, 1, 10, 100, 0, types.OrderTypeStopLoss, types.TimeInForceIOC, 1, 5, vecOrderExpiry)}}, ErrOrderGroupSizeInvalid},
		{"grouped market mismatch", &CreateGroupedOrders{Header: head(1, Attributes{}), GroupingType: types.GroupingOneTriggersTheOther, Orders: []OrderInfo{
			orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry),
			orderInfo(1, 2, 0, 90, 1, types.OrderTypeStopLoss, types.TimeInForceIOC, 1, 95, vecOrderExpiry),
		}}, ErrMarketIndexMismatch},
		{"grouped duplicate client index", &CreateGroupedOrders{Header: head(1, Attributes{}), GroupingType: types.GroupingOneTriggersTheOther, Orders: []OrderInfo{
			orderInfo(0, 1, 10, 100, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry),
			orderInfo(0, 1, 0, 90, 1, types.OrderTypeStopLoss, types.TimeInForceIOC, 1, 95, vecOrderExpiry),
		}}, ErrClientOrderIndexDuplicate},
		{"oco not reduce only", &CreateGroupedOrders{Header: head(1, Attributes{}), GroupingType: types.GroupingOneCancelsTheOther, Orders: []OrderInfo{
			orderInfo(0, 1, 10, 90, 1, types.OrderTypeStopLoss, types.TimeInForceIOC, 0, 95, vecOrderExpiry),
			orderInfo(0, 2, 10, 120, 1, types.OrderTypeTakeProfit, types.TimeInForceIOC, 0, 115, vecOrderExpiry),
		}}, ErrOrderReduceOnlyInvalid},
		{"margin zero amount", &UpdateMargin{Header: head(1, Attributes{}), MarketIndex: 0, USDCAmount: 0, Direction: 1}, ErrTransferAmountTooLow},
		{"margin bad direction", &UpdateMargin{Header: head(1, Attributes{}), MarketIndex: 0, USDCAmount: 5, Direction: 2}, ErrInvalidUpdateMarginDirection},
	}
	for _, c := range cases {
		if got := c.tx.Validate(); got != c.want {
			t.Errorf("%s: Validate = %v, want %v", c.name, got, c.want)
		}
	}
	// A cancel-all may be signed with api key index 255.
	var anyKey = &CancelAllOrders{Header: Header{AccountIndex: 1, APIKeyIndex: 255, ExpiredAt: vecExpiredAt, Nonce: 1}, TimeInForce: types.CancelAllImmediate}
	if err := anyKey.Validate(); err != nil {
		t.Fatalf("cancel-all with api key 255: %v", err)
	}
}

func TestAttributesJSONOrder(t *testing.T) {
	var a = Attributes{OrderVersion: 9, SkipNonce: true, IntegratorAccountIndex: 4}
	if got := string(a.appendJSON(nil)); got != `{"1":4,"4":1,"8":9}` {
		t.Fatalf("attributes JSON = %s", got)
	}
	var empty Attributes
	if got := string(empty.appendJSON(nil)); got != "null" || !empty.IsEmpty() {
		t.Fatalf("empty attributes = %s", got)
	}
	// A market-scope of 255 is the nil value: not present.
	var nilMarket = Attributes{HasCancelAllMarket: true, CancelAllMarketIndex: NilMarketIndex}
	if !nilMarket.IsEmpty() {
		t.Fatal("market 255 must be absent")
	}
}

func BenchmarkCreateOrderHash(b *testing.B) {
	var transaction = &CreateOrder{Header: head(7, Attributes{}), Order: orderInfo(0, 1, 1000, 405000, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}
	var hash signing.Hash
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		transaction.Hash(304, &hash)
	}
}

func BenchmarkCreateOrderHashWithAttributes(b *testing.B) {
	var transaction = &CreateOrder{Header: head(7, Attributes{SkipNonce: true, OrderVersion: 3}), Order: orderInfo(0, 1, 1000, 405000, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}
	var hash signing.Hash
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		transaction.Hash(304, &hash)
	}
}

func BenchmarkCreateOrderAppendInfo(b *testing.B) {
	var transaction = &CreateOrder{Header: head(7, Attributes{}), Order: orderInfo(0, 1, 1000, 405000, 0, types.OrderTypeLimit, types.TimeInForceGTT, 0, 0, vecOrderExpiry)}
	var sig signing.Signature
	var buf = make([]byte, 0, 512)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = transaction.AppendInfo(buf[:0], &sig)
	}
}
