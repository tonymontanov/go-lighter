/*
FILE: scripts/gen-vectors/main.go

DESCRIPTION:
Generates internal/tx/vectors_generated_test.go — byte-for-byte reference
vectors of the transaction hash, the Schnorr signature and the tx_info JSON,
produced with the OFFICIAL Go SDK (github.com/elliottech/lighter-go).

This is a SEPARATE module on purpose: lighter-go depends on go-ethereum
(blst, c-kzg, go-verkle) and none of that must enter the SDK's module graph.

The Schnorr signature of lighter-go is randomised, so the vectors are signed
with SchnorrSignHashedMessage2 and a FIXED scalar k (the same k the parity
test passes to Signer.SignWithNonce). The hash, the tx_info shape and the
signature therefore compare byte for byte.

USAGE:
    make vectors        # from the repository root

The CASES table below is mirrored by buildVectorTxs in
internal/tx/vectors_test.go. Keep both in sync; the parity test fails loudly
when they drift (missing or extra names, different hashes).
*/

package main

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	curve "github.com/elliottech/poseidon_crypto/curve/ecgfp5"
	gFp5 "github.com/elliottech/poseidon_crypto/field/goldilocks_quintic_extension"
	schnorr "github.com/elliottech/poseidon_crypto/signature/schnorr"

	"github.com/elliottech/lighter-go/types"
	"github.com/elliottech/lighter-go/types/txtypes"
)

// testPrivateKeyHex — the fixed test key of the SDK test-suite (no 0x). Not
// registered on any account.
const testPrivateKeyHex string = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728"

// nonceScalar returns the fixed signing scalar k: byte i = i*7 + 3.
func nonceScalar() []byte {
	var k = make([]byte, 40)
	for i := range k {
		k[i] = byte(i*7 + 3)
	}
	return k
}

const (
	accountIndex int64  = 1
	apiKeyIndex  uint8  = 2
	expiredAt    int64  = 1700000000000
	orderExpiry  int64  = 1800000000000
	mainnet      uint32 = 304
	testnet      uint32 = 300
)

func i64(v int64) *int64   { return &v }
func u8(v uint8) *uint8    { return &v }
func u32(v uint32) *uint32 { return &v }
func i16(v int16) *int16   { return &v }
func ops(nonce int64, attrs *types.L2TxAttributes) *types.TransactOpts {
	return &types.TransactOpts{
		FromAccountIndex: i64(accountIndex),
		ApiKeyIndex:      u8(apiKeyIndex),
		ExpiredAt:        expiredAt,
		Nonce:            i64(nonce),
		TxAttributes:     attrs,
	}
}

func order(market int16, cli int64, base int64, price uint32, isAsk uint8, typ uint8, tif uint8, ro uint8, trigger uint32, expiry int64) *types.CreateOrderTxReq {
	return &types.CreateOrderTxReq{MarketIndex: market, ClientOrderIndex: cli, BaseAmount: base, Price: price, IsAsk: isAsk, Type: typ, TimeInForce: tif, ReduceOnly: ro, TriggerPrice: trigger, OrderExpiry: expiry}
}

type vectorCase struct {
	name    string
	chainID uint32
	tx      txtypes.TxInfo
}

// cases mirrors buildVectorTxs of internal/tx/vectors_test.go.
func cases() []vectorCase {
	return []vectorCase{
		{"createOrder limit gtt", mainnet, types.ConvertCreateOrderTx(order(0, 1, 1000, 405000, 0, 0, 1, 0, 0, orderExpiry), ops(7, nil))},
		{"createOrder market ioc skip nonce", mainnet, types.ConvertCreateOrderTx(order(1, 2, 500, 3000000, 1, 1, 0, 0, 0, 0), ops(1700000000123, &types.L2TxAttributes{SkipNonce: u8(1)}))},
		{"createOrder post-only integrator", mainnet, types.ConvertCreateOrderTx(order(2, 0, 20000, 123, 0, 0, 2, 0, 0, orderExpiry), ops(9, &types.L2TxAttributes{IntegratorAccountIndex: i64(77), IntegratorTakerFee: u32(100), IntegratorMakerFee: u32(50)}))},
		{"createOrder stop loss reduce only testnet", testnet, types.ConvertCreateOrderTx(order(0, 3, 0, 380000, 1, 2, 0, 1, 390000, orderExpiry), ops(10, nil))},
		{"createOrder take profit limit self trade", mainnet, types.ConvertCreateOrderTx(order(3, 4, 10, 250, 0, 5, 1, 0, 240, orderExpiry), ops(11, &types.L2TxAttributes{SelfTradeBehaviorMode: u8(2), SelfTradeEqualityMode: u8(1)}))},
		{"cancelOrder", mainnet, types.ConvertCancelOrderTx(&types.CancelOrderTxReq{MarketIndex: 0, Index: 1}, ops(12, nil))},
		{"cancelOrder by order index skip nonce", mainnet, types.ConvertCancelOrderTx(&types.CancelOrderTxReq{MarketIndex: 5, Index: 281477872907039}, ops(1700000000456, &types.L2TxAttributes{SkipNonce: u8(1)}))},
		{"cancelAll immediate", mainnet, types.ConvertCancelAllOrdersTx(&types.CancelAllOrdersTxReq{TimeInForce: 0, Time: 0}, ops(13, nil))},
		{"cancelAll scheduled", mainnet, types.ConvertCancelAllOrdersTx(&types.CancelAllOrdersTxReq{TimeInForce: 1, Time: 1700000600000}, ops(14, nil))},
		{"cancelAll abort", mainnet, types.ConvertCancelAllOrdersTx(&types.CancelAllOrdersTxReq{TimeInForce: 2, Time: 0}, ops(15, nil))},
		{"cancelAll immediate market scope", mainnet, types.ConvertCancelAllOrdersTx(&types.CancelAllOrdersTxReq{TimeInForce: 0, Time: 0}, ops(16, &types.L2TxAttributes{CancelAllMarketIndex: i16(0)}))},
		{"modifyOrder", mainnet, types.ConvertModifyOrderTx(&types.ModifyOrderTxReq{MarketIndex: 0, Index: 1, BaseAmount: 1100, Price: 410000, TriggerPrice: 0}, ops(17, nil))},
		{"modifyOrder version skip nonce", mainnet, types.ConvertModifyOrderTx(&types.ModifyOrderTxReq{MarketIndex: 0, Index: 281477872907039, BaseAmount: 1200, Price: 420000, TriggerPrice: 0}, ops(1700000000789, &types.L2TxAttributes{SkipNonce: u8(1), OrderVersion: i64(5)}))},
		{"updateLeverage cross", mainnet, types.ConvertUpdateLeverageTx(&types.UpdateLeverageTxReq{MarketIndex: 0, InitialMarginFraction: 500, MarginMode: 0}, ops(18, nil))},
		{"updateLeverage isolated testnet", testnet, types.ConvertUpdateLeverageTx(&types.UpdateLeverageTxReq{MarketIndex: 7, InitialMarginFraction: 10000, MarginMode: 1}, ops(19, nil))},
		{"groupedOrders otoco", mainnet, types.ConvertCreateGroupedOrdersTx(&types.CreateGroupedOrdersTxReq{GroupingType: 3, Orders: []*types.CreateOrderTxReq{
			order(0, 21, 1000, 405000, 0, 0, 1, 0, 0, orderExpiry),
			order(0, 22, 0, 380000, 1, 2, 0, 1, 390000, orderExpiry),
			order(0, 23, 0, 430000, 1, 4, 0, 1, 420000, orderExpiry),
		}}, ops(20, nil))},
		{"groupedOrders oco", mainnet, types.ConvertCreateGroupedOrdersTx(&types.CreateGroupedOrdersTxReq{GroupingType: 2, Orders: []*types.CreateOrderTxReq{
			order(1, 31, 500, 2900000, 1, 3, 1, 1, 2950000, orderExpiry),
			order(1, 32, 500, 3300000, 1, 4, 0, 1, 3250000, orderExpiry),
		}}, ops(21, nil))},
		{"updateMargin add", mainnet, types.ConvertUpdateMarginTx(&types.UpdateMarginTxReq{MarketIndex: 0, USDCAmount: 5000000, Direction: 1}, ops(22, nil))},
		{"updateMargin remove large", mainnet, types.ConvertUpdateMarginTx(&types.UpdateMarginTxReq{MarketIndex: 0, USDCAmount: 1<<40 + 12345, Direction: 0}, ops(23, nil))},
	}
}

// setSig stores the signature into the concrete transaction struct.
func setSig(tx txtypes.TxInfo, sig []byte) {
	switch typed := tx.(type) {
	case *txtypes.L2CreateOrderTxInfo:
		typed.Sig = sig
	case *txtypes.L2CancelOrderTxInfo:
		typed.Sig = sig
	case *txtypes.L2CancelAllOrdersTxInfo:
		typed.Sig = sig
	case *txtypes.L2ModifyOrderTxInfo:
		typed.Sig = sig
	case *txtypes.L2UpdateLeverageTxInfo:
		typed.Sig = sig
	case *txtypes.L2CreateGroupedOrdersTxInfo:
		typed.Sig = sig
	case *txtypes.L2UpdateMarginTxInfo:
		typed.Sig = sig
	default:
		panic(fmt.Sprintf("setSig: unsupported %T", tx))
	}
}

func main() {
	var keyBytes, err = hex.DecodeString(testPrivateKeyHex)
	if err != nil {
		panic(err)
	}
	var sk = curve.ScalarElementFromLittleEndianBytes(keyBytes)
	var pk = schnorr.SchnorrPkFromSk(sk)
	var k = curve.ScalarElementFromLittleEndianBytes(nonceScalar())

	var out strings.Builder
	out.WriteString("// Code generated by scripts/gen-vectors with github.com/elliottech/lighter-go; DO NOT EDIT.\n")
	out.WriteString("// Regenerate with `make vectors`.\n\n")
	out.WriteString("package tx\n\n")
	fmt.Fprintf(&out, "// generatedPublicKeyHex — public key of the test key, as the apikeys endpoint prints it.\nconst generatedPublicKeyHex string = %q\n\n", hex.EncodeToString(pk.ToLittleEndianBytes()))
	fmt.Fprintf(&out, "// generatedNonceScalarHex — the fixed Schnorr scalar k of every vector.\nconst generatedNonceScalarHex string = %q\n\n", hex.EncodeToString(nonceScalar()))
	out.WriteString("var generatedVectors []generatedVector = []generatedVector{\n")
	for _, c := range cases() {
		if err = c.tx.Validate(); err != nil {
			panic(fmt.Sprintf("%s: reference validation failed: %v", c.name, err))
		}
		var hashBytes []byte
		hashBytes, err = c.tx.Hash(c.chainID)
		if err != nil {
			panic(err)
		}
		var hashElem gFp5.Element
		hashElem, err = gFp5.FromCanonicalLittleEndianBytes(hashBytes)
		if err != nil {
			panic(err)
		}
		var sig = schnorr.SchnorrSignHashedMessage2(hashElem, sk, k).ToBytes()
		if !schnorr.IsSchnorrSignatureValid(pk, hashElem, mustSig(sig)) {
			panic(c.name + ": reference signature does not verify")
		}
		setSig(c.tx, sig)
		var info string
		info, err = c.tx.GetTxInfo()
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&out, "\t{name: %q, chainID: %d, txType: %d, hashHex: %q, sigBase64: %q, info: %q},\n",
			c.name, c.chainID, c.tx.GetTxType(), hex.EncodeToString(hashBytes), base64.StdEncoding.EncodeToString(sig), info)
	}
	out.WriteString("}\n")
	_, _ = os.Stdout.WriteString(out.String())
}

func mustSig(b []byte) schnorr.Signature {
	var s, err = schnorr.SigFromBytes(b)
	if err != nil {
		panic(err)
	}
	return s
}
