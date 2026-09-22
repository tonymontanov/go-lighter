package lighter

import (
	"testing"
	"time"
)

func TestDefaultsAndTestnetSwitch(t *testing.T) {
	var cfg Config = Config{}.withDefaults()
	if cfg.REST.BaseURL != MainnetRestURL || cfg.WS.URL != MainnetWsURL || cfg.ChainID != MainnetChainID {
		t.Fatalf("mainnet defaults: %+v", cfg)
	}
	if cfg.TxExpiry != 10*time.Minute-time.Second || cfg.AuthTokenLifetime != 7*time.Hour || cfg.MarketRefreshInterval != 5*time.Minute || cfg.UserAgent != "go-lighter/v1" {
		t.Fatalf("defaults: %+v", cfg)
	}
	// DefaultConfig() + Testnet must end up on the testnet (the sibling SDK trap).
	var testnet Config = DefaultConfig()
	testnet.Testnet = true
	testnet = testnet.withDefaults()
	if testnet.REST.BaseURL != TestnetRestURL || testnet.WS.URL != TestnetWsURL || testnet.ChainID != TestnetChainID {
		t.Fatalf("testnet switch: %+v", testnet)
	}
	// An explicit URL is kept.
	var custom Config = Config{Testnet: true}
	custom.REST.BaseURL = "http://127.0.0.1:9"
	custom.WS.URL = "ws://127.0.0.1:9/stream"
	custom = custom.withDefaults()
	if custom.REST.BaseURL != "http://127.0.0.1:9" || custom.WS.URL != "ws://127.0.0.1:9/stream" {
		t.Fatalf("custom URLs overridden: %+v", custom)
	}
	if cfg.validate() != nil {
		t.Fatal("defaults must validate")
	}
}

func TestValidate(t *testing.T) {
	var bad Config = DefaultConfig()
	bad.WS.ReadTimeout = bad.WS.PingInterval
	if bad.withDefaults().validate() == nil {
		t.Fatal("ReadTimeout <= PingInterval must fail")
	}
	bad = DefaultConfig()
	bad.AccountIndex = -5
	if bad.withDefaults().validate() == nil {
		t.Fatal("negative account index must fail")
	}
	bad = DefaultConfig()
	bad.PrivateKey = "0x00"
	bad.APIKeyIndex = 255
	if bad.withDefaults().validate() == nil {
		t.Fatal("api key index 255 must fail")
	}
	bad = DefaultConfig()
	bad.AuthTokenLifetime = 9 * time.Hour
	if bad.withDefaults().validate() == nil {
		t.Fatal("auth token lifetime above 8h must fail")
	}
	bad = DefaultConfig()
	bad.APIKeyIndex = 3
	bad.ExtraKeys = []APIKeyConfig{{Index: 3}}
	if bad.withDefaults().validate() == nil {
		t.Fatal("duplicate key index must fail")
	}
}

func TestNewClientKeys(t *testing.T) {
	var cfg Config = DefaultConfig()
	cfg.AccountIndex = 7
	cfg.APIKeyIndex = 3
	cfg.PrivateKey = "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728"
	cfg.ExtraKeys = []APIKeyConfig{{Index: 4, PrivateKey: "0x" + "ab"[:2] + "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f2021222324252627"}}
	var client, err = NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if !client.CanSign() || client.AccountIndex() != 7 || len(client.APIKeyIndexes()) != 2 || client.APIKeyIndexes()[0] != 3 {
		t.Fatalf("client state: keys=%v", client.APIKeyIndexes())
	}
	if client.Config().PrivateKey != "" || client.Config().ExtraKeys != nil {
		t.Fatal("key material must not stay in the config copy")
	}
	if client.PublicKeyHex(3) == "" || client.PublicKeyHex(9) != "" || client.IsTestnet() {
		t.Fatal("public keys")
	}
	var token, tokenErr = client.AuthToken()
	if tokenErr != nil || token == "" {
		t.Fatalf("auth token: %v", tokenErr)
	}
	if client.RateLimits().Tier != RateLimitTierStandard {
		t.Fatal("default tier")
	}
	if _, err = NewClient(Config{PrivateKey: "zz"}); !IsAuth(err) {
		t.Fatalf("bad key: %v", err)
	}
	var readOnly, roErr = NewClient(Config{Testnet: true})
	if roErr != nil || readOnly.CanSign() || !readOnly.IsTestnet() {
		t.Fatalf("read-only client: %v", roErr)
	}
	_ = readOnly.Close()
}
