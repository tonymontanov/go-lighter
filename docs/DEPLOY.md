# Deployment notes

## Colocation

The exchange recommends **AWS Tokyo, availability zone `ap-northeast-1a`
(zone id `apne1-az4`)** for the lowest latency to the API servers (official
"Get Started" page). Premium accounts on the top staking tiers can ask support
for direct access that bypasses CloudFront.

Practical checklist for a trading host:

- pin the instance to `apne1-az4` (the zone *id*, not the zone *name*: names
  are shuffled per AWS account);
- keep the HTTP client pool warm (`Config.REST.MaxIdleConnsPerHost`, default
  100) and the WebSocket sockets open (`Client.WarmUpStream`, `WarmUpPost`);
- one trading process per API key: nonces are tracked per key on the exchange
  and the SDK serialises sends per key (`Config.NonceMode` sequential);
  parallel senders use several keys (`Config.ExtraKeys`);
- the SDK is pure Go and builds with `CGO_ENABLED=0`.

## Credentials

The desk's credential convention for the connector:

| credentials.json / env | Meaning |
|---|---|
| `api_key` / `LIGHTER_PERPETUALS_API_KEY` | account index |
| `secret_key` / `LIGHTER_PERPETUALS_SECRET_KEY` | API key private key(s), 40-byte hex, comma-separated |
| `passphrase` / `LIGHTER_PERPETUALS_PASSPHRASE` | API key index(es) 2..254, comma-separated, same order |

Keys are never logged and are zeroed on `Client.Close()`.

## Rate limits

Set `Config.RateLimit.Tier` (`standard` / `premium` / `plus` / `builder`) and
`Config.RateLimit.StakedLIT` to match the account; the SDK reports its own
accounting through `Config.RateLimitEventObserver` and can fail fast locally
(`RejectWhenRateLimited`). Authenticated reads are limited per L1 address
instead of per IP.
