/*
FILE: internal/ws/protocol.go

DESCRIPTION:
Wire rules of the Lighter WebSocket API (wss://<host>/stream; official
"WebSocket" reference page, fetched 2026-09-22).

CLIENT → SERVER (every message is a JSON object with a "type"):
  {"type":"subscribe","channel":"order_book/0"}
  {"type":"subscribe","channel":"account_all_orders/123","auth":"<token>"}   private channels
  {"type":"unsubscribe","channel":"order_book/0"}
  {"type":"ping"}                                    keepalive (a frame every < 2 min is required)
  {"type":"pong"}                                    answer to a server ping
  {"type":"jsonapi/sendtx","data":{"id":"<id>","tx_type":14,"tx_info":{...}}}
  {"type":"jsonapi/sendtxbatch","data":{"id":"<id>","tx_types":"[14,15]","tx_infos":"[\"{...}\"]"}}

SERVER → CLIENT:
  {"type":"connected"}                               greeting
  {"type":"ping"} / {"type":"pong"}                  keepalive
  {"type":"subscribed/<name>","channel":"<name>:<args>", ...payload}   snapshot on subscription
  {"type":"update/<name>","channel":"<name>:<args>", ...payload}       updates
  {"type":"jsonapi/sendtx", ...}                     reply to a post, correlated by its id
  error frames                                       NOT documented; treated leniently: a frame
                                                     whose type is "error" or that carries a
                                                     "code" and a "message" and no channel.

ROUTING:
The push channel uses ':' where the subscription used '/': "order_book/0" is
answered on "order_book:0". Keys are therefore normalised by mapping ':' to
'/'. Two documented quirks are covered by a prefix fallback: account_orders
answers on "account_orders:{market}" (without the account) and account_market
answers with the subscription form itself.
*/

package ws

// Message types.
const (
	typeSubscribe   string = "subscribe"
	typeUnsubscribe string = "unsubscribe"
	typePing        string = "ping"
	typePong        string = "pong"
	typeConnected   string = "connected"
	typeError       string = "error"
	// PostTypeSendTx — jsonapi/sendtx message type.
	PostTypeSendTx string = "jsonapi/sendtx"
	// PostTypeSendTxBatch — jsonapi/sendtxbatch message type.
	PostTypeSendTxBatch string = "jsonapi/sendtxbatch"
	// prefixSubscribed / prefixUpdate — type prefixes of pushes.
	prefixSubscribed string = "subscribed/"
	prefixUpdate     string = "update/"
	prefixPost       string = "jsonapi/"
)

// pingFrame / pongFrame — keepalive frames.
var (
	pingFrame []byte = []byte(`{"type":"ping"}`)
	pongFrame []byte = []byte(`{"type":"pong"}`)
)

// envelope — the fields of every server frame the router needs.
type envelope struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
}

// RouteKey normalises a channel name: ':' → '/'. Subscription channels are
// already in the '/' form, so the key of a subscription is its channel.
func RouteKey(channel string) string {
	var i int
	for i = 0; i < len(channel); i++ {
		if channel[i] == ':' {
			break
		}
	}
	if i == len(channel) {
		return channel
	}
	var out []byte = make([]byte, len(channel))
	for i = 0; i < len(channel); i++ {
		if channel[i] == ':' {
			out[i] = '/'
		} else {
			out[i] = channel[i]
		}
	}
	return string(out)
}

// hasPrefix reports whether s starts with prefix.
func hasPrefix(s string, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// subscriptionFrame builds {"type":"subscribe"|"unsubscribe","channel":C[,"auth":A]}.
func subscriptionFrame(method string, channel string, auth string) []byte {
	var frame []byte = make([]byte, 0, len(channel)+len(auth)+48)
	frame = append(frame, `{"type":"`...)
	frame = append(frame, method...)
	frame = append(frame, `","channel":"`...)
	frame = append(frame, channel...)
	frame = append(frame, '"')
	if auth != "" {
		frame = append(frame, `,"auth":"`...)
		frame = append(frame, auth...)
		frame = append(frame, '"')
	}
	return append(frame, '}')
}
