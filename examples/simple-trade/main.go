/*
FILE: examples/simple-trade/main.go

DESCRIPTION:
Live order life-cycle smoke test of the Perpetuals section — REAL FUNDS:
the desk has no testnet credentials, so this runs on MAINNET unless
LIGHTER_TESTNET=1. The order is a tiny post-only BUY placed far below the
market, so it cannot trade; it is modified once and cancelled at the end.

 1. verify the configured API key against the exchange (CheckAPIKey);
 2. subscribe to account_tx (sequencer outcomes) and account orders;
 3. read the book, place a post-only buy at ~10% below the best bid with a
    client order index;
 4. await the sequencer outcome, list active orders;
 5. modify its price (+1 tick), then cancel it and await both outcomes;
 6. print the SDK-side rate-limit accounting.

The example refuses to run unless LIGHTER_ALLOW_LIVE=1.

USAGE:
    cp .env.example .env   # fill in the credentials
    ./scripts/run.sh ./examples/simple-trade

ENVIRONMENT (names match the desk's credential convention):
    LIGHTER_PERPETUALS_API_KEY      account index
    LIGHTER_PERPETUALS_SECRET_KEY   API key private key (40-byte hex)
    LIGHTER_PERPETUALS_PASSPHRASE   API key index (2..254)
    LIGHTER_ALLOW_LIVE              must be "1"
    LIGHTER_TESTNET                 "1" → testnet endpoints and chain id
    LIGHTER_SYMBOL                  default ETH
    LIGHTER_NOTIONAL                order value in USDC, default 12

SECURITY: the private key is read from the environment only; it is never
printed.
*/

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	lighter "github.com/tonymontanov/go-lighter"
	"github.com/tonymontanov/go-lighter/perpetuals"
	"github.com/tonymontanov/go-lighter/types"
)

// errNotAllowed — the safety gate is closed.
var errNotAllowed error = errors.New("refusing to send orders: set LIGHTER_ALLOW_LIVE=1 (this trades REAL funds on mainnet)")

func main() {
	var err error = run()
	if err != nil {
		fmt.Println("FAILED:", err)
		if errors.Is(err, errNotAllowed) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// step wraps an error with the name of the failed step.
func step(name string, err error) error {
	return fmt.Errorf("at %s: %w", name, err)
}

// credentials reads the desk-style triple from the environment.
func credentials() (int64, uint8, string, error) {
	var account int64
	var err error
	account, err = strconv.ParseInt(strings.TrimSpace(os.Getenv("LIGHTER_PERPETUALS_API_KEY")), 10, 64)
	if err != nil {
		return 0, 0, "", errors.New("LIGHTER_PERPETUALS_API_KEY must be the account index")
	}
	var keyIndexText string = strings.TrimSpace(strings.Split(os.Getenv("LIGHTER_PERPETUALS_PASSPHRASE"), ",")[0])
	var keyIndex int64
	keyIndex, err = strconv.ParseInt(keyIndexText, 10, 64)
	if err != nil || keyIndex < 2 || keyIndex > 254 {
		return 0, 0, "", errors.New("LIGHTER_PERPETUALS_PASSPHRASE must be the API key index (2..254)")
	}
	var privateKey string = strings.TrimSpace(strings.Split(os.Getenv("LIGHTER_PERPETUALS_SECRET_KEY"), ",")[0])
	if privateKey == "" {
		return 0, 0, "", errors.New("LIGHTER_PERPETUALS_SECRET_KEY is empty")
	}
	return account, uint8(keyIndex), privateKey, nil
}

// await waits for the sequencer outcome of a receipt and prints it.
func await(ctx context.Context, tracker *lighter.TxTracker, name string, receipt types.TxReceipt) error {
	var waitCtx context.Context
	var cancel context.CancelFunc
	waitCtx, cancel = context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var outcome types.TxOutcome
	var err error
	outcome, err = tracker.Await(waitCtx, receipt.TxHash)
	if err != nil {
		return fmt.Errorf("%s: no sequencer outcome for %s: %w", name, receipt.TxHash, err)
	}
	fmt.Printf("  %s: tx=%s nonce=%d → %s %s\n", name, receipt.TxHash, receipt.Nonce, outcome.Status, outcome.AppError)
	if outcome.Failed() {
		return fmt.Errorf("%s rejected by the sequencer: %s", name, outcome.AppError)
	}
	return nil
}

// run holds the whole example so that deferred cleanups run before exit.
func run() error {
	if os.Getenv("LIGHTER_ALLOW_LIVE") != "1" {
		return errNotAllowed
	}
	var account int64
	var keyIndex uint8
	var privateKey string
	var err error
	account, keyIndex, privateKey, err = credentials()
	if err != nil {
		return step("credentials", err)
	}
	var symbol string = os.Getenv("LIGHTER_SYMBOL")
	if symbol == "" {
		symbol = "ETH"
	}
	var notional float64 = 12
	if raw := os.Getenv("LIGHTER_NOTIONAL"); raw != "" {
		if parsed, parseErr := strconv.ParseFloat(raw, 64); parseErr == nil && parsed >= 10 {
			notional = parsed
		}
	}

	var cfg lighter.Config = lighter.DefaultConfig()
	cfg.Testnet = os.Getenv("LIGHTER_TESTNET") == "1"
	cfg.AccountIndex = account
	cfg.APIKeyIndex = keyIndex
	cfg.PrivateKey = privateKey
	cfg.RateLimitEventObserver = func(e lighter.RateLimitEvent) {
		fmt.Printf("  [rate] %-14s status=%d used=%d/%d sendTx=%d/%d\n", e.Endpoint, e.HTTPStatus, e.UsedWeight, e.WeightLimit, e.UsedSendTx, e.SendTxLimit)
	}
	privateKey = ""

	var client *lighter.Client
	client, err = lighter.NewClient(cfg)
	if err != nil {
		return step("NewClient", err)
	}
	defer func() { _ = client.Close() }()
	fmt.Printf("network: %s (chain id %d) account=%d key=%d public=%s\n", client.Config().REST.BaseURL, client.Config().ChainID, account, keyIndex, client.PublicKeyHex(keyIndex))

	var perps *perpetuals.Client = perpetuals.NewClient(client)
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 1. Key check.
	if err = perps.Account().CheckAPIKey(ctx, keyIndex); err != nil {
		return step("CheckAPIKey", err)
	}
	fmt.Println("api key matches the exchange")
	var balance types.Balance
	balance, err = perps.Account().GetBalance(ctx)
	if err != nil {
		return step("GetBalance", err)
	}
	fmt.Printf("collateral=%s available=%s\n", balance.Collateral, balance.AvailableBalance)

	// 2. Streams: sequencer outcomes and order updates.
	var tracker *lighter.TxTracker = lighter.NewTxTracker(256)
	var errHandler func(error) = func(streamErr error) { fmt.Println("  [stream error]", streamErr) }
	if err = perps.Stream().WatchTransactions(ctx, tracker.Observe, nil, errHandler); err != nil {
		return step("WatchTransactions", err)
	}
	if err = perps.Stream().WatchOrders(ctx, symbol, func(push *perpetuals.OrdersPush) {
		var i int
		for i = 0; i < len(push.Orders); i++ {
			var o *types.Order = &push.Orders[i]
			fmt.Printf("  [order] idx=%d cli=%d %s %s x %s remaining=%s v=%d\n", o.OrderIndex, o.ClientOrderIndex, o.Status, o.Price, o.InitialBaseAmount, o.RemainingBaseAmnt, o.OrderVersion)
		}
	}, nil, errHandler); err != nil {
		return step("WatchOrders", err)
	}
	if err = perps.Stream().WarmUp(ctx); err != nil {
		return step("WarmUpStream", err)
	}

	// 3. Place a post-only buy ~10% below the best bid.
	var info types.MarketInfo
	info, err = perps.MarketData().GetMarketInfo(ctx, symbol)
	if err != nil {
		return step("GetMarketInfo", err)
	}
	var book types.OrderBook
	book, err = perps.MarketData().GetOrderBook(ctx, symbol, 5)
	if err != nil {
		return step("GetOrderBook", err)
	}
	if book.BestBid() == nil {
		return step("GetOrderBook", errors.New("empty bid side"))
	}
	var price types.Fixed = info.Precision.NormalizePrice(types.Fixed(int64(book.BestBid().Price)*9/10), types.RoundDown)
	var sizeRaw types.Fixed
	sizeRaw, err = types.FixedFromFloat64(notional / price.Float64())
	if err != nil {
		return step("size", err)
	}
	var size types.Fixed = info.Precision.NormalizeSize(sizeRaw, types.RoundUp)
	if size < info.MinBaseAmount {
		size = info.MinBaseAmount
	}
	var clientIndex int64 = time.Now().UnixMilli() % (1 << 40)
	fmt.Printf("placing post-only buy %s x %s (best bid %s), client index %d\n", price, size, book.BestBid().Price, clientIndex)

	var receipt types.TxReceipt
	receipt, err = perps.Trading().CreateOrder(ctx, types.CreateOrderRequest{
		Symbol: symbol, ClientOrderIndex: clientIndex, IsAsk: false, Price: price, Size: size,
		Type: types.OrderTypeLimit, TimeInForce: types.TimeInForcePostOnly,
	}, types.SendOptions{})
	if err != nil {
		return step("CreateOrder", err)
	}
	if err = await(ctx, tracker, "create", receipt); err != nil {
		return err
	}

	// 4. Active orders.
	var orders []types.Order
	orders, err = perps.Trading().GetOpenOrders(ctx, symbol)
	if err != nil {
		return step("GetOpenOrders", err)
	}
	fmt.Printf("active orders on %s: %d\n", symbol, len(orders))

	// 5. Modify, then cancel.
	receipt, err = perps.Trading().ModifyOrder(ctx, types.ModifyOrderRequest{
		Symbol: symbol, OrderIndex: clientIndex, Price: price + info.Precision.TickSize(), Size: size,
	}, types.SendOptions{})
	if err != nil {
		return step("ModifyOrder", err)
	}
	if err = await(ctx, tracker, "modify", receipt); err != nil {
		return err
	}
	receipt, err = perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: symbol, OrderIndex: clientIndex}, types.SendOptions{})
	if err != nil {
		return step("CancelOrder", err)
	}
	if err = await(ctx, tracker, "cancel", receipt); err != nil {
		return err
	}
	// A second cancel of the same order is reported as a missing order.
	_, err = perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: symbol, OrderIndex: clientIndex}, types.SendOptions{})
	fmt.Printf("second cancel: missingOrder=%v err=%v\n", lighter.IsMissingOrder(err), err)

	// 6. Accounting.
	var limits lighter.RateLimitSnapshot = client.RateLimits()
	fmt.Printf("rate limits: tier=%s weight=%d/%d sendTx=%d/%d\n", limits.Tier, limits.UsedWeight, limits.WeightLimit, limits.UsedSendTx, limits.SendTxLimit)
	return nil
}
