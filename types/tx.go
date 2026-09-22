/*
FILE: types/tx.go

DESCRIPTION:
Transaction-level results shared by every section (layer 1).

TWO LEVELS OF CONFIRMATION (official "Signing Transactions" page):
  1. TxReceipt — the API server accepted the transaction (code 200). It only
     means the syntax and signature were fine and the transaction was
     forwarded; the nonce is consumed.
  2. TxOutcome — the sequencer executed (or rejected) it. Delivered by the
     account_tx WebSocket channel (Transaction.Status, event_info "ae") and
     mirrored by the order channels. See Stream().WatchTransactions and the
     TxTracker helper of the sections.
*/

package types

// TxReceipt — answer of sendTx: the API server accepted the transaction.
type TxReceipt struct {
	// TxHash — hash of the signed transaction; equals the locally computed
	// hash (a mismatch would have failed the signature check).
	TxHash string
	// TxType — transaction type.
	TxType TxType
	// Nonce / APIKeyIndex — the nonce and key the transaction was signed with.
	Nonce       int64
	APIKeyIndex uint8
	// PredictedExecutionTimeMs — exchange estimate of the execution time.
	PredictedExecutionTimeMs int64
	// VolumeQuotaRemaining — remaining volume quota (Premium accounts).
	VolumeQuotaRemaining int64
}

// BatchReceipt — answer of sendTxBatch.
type BatchReceipt struct {
	// TxHashes — one hash per transaction, in request order.
	TxHashes []string
	// FirstNonce / Count / APIKeyIndex — nonces FirstNonce .. FirstNonce+Count-1
	// of the key were consumed.
	FirstNonce               int64
	Count                    int
	APIKeyIndex              uint8
	PredictedExecutionTimeMs int64
	VolumeQuotaRemaining     int64
}

// Transaction — a transaction as reported by the tx endpoints and the
// account_tx channel.
type Transaction struct {
	Hash string `json:"hash"`
	Type TxType `json:"type"`
	// Info — JSON object as a string: the signed tx_info.
	Info string `json:"info"`
	// EventInfo — JSON object as a string; "ae" carries the application error
	// of a rejected transaction (see AppError).
	EventInfo        string   `json:"event_info"`
	Status           TxStatus `json:"status"`
	TransactionIndex int64    `json:"transaction_index"`
	L1Address        string   `json:"l1_address"`
	AccountIndex     int64    `json:"account_index"`
	Nonce            int64    `json:"nonce"`
	ExpireAtMs       int64    `json:"expire_at"`
	BlockHeight      int64    `json:"block_height"`
	QueuedAtMs       int64    `json:"queued_at"`
	ExecutedAtMs     int64    `json:"executed_at"`
	SequenceIndex    int64    `json:"sequence_index"`
	ParentHash       string   `json:"parent_hash"`
	APIKeyIndex      uint8    `json:"api_key_index"`
	// TransactionTimeUs — sequencer time in microseconds.
	TransactionTimeUs int64 `json:"transaction_time"`
}

// AppError extracts the "ae" field of EventInfo ("" when absent). The value
// is the exchange's application error name of a rejected transaction.
func (t *Transaction) AppError() string {
	return jsonStringField(t.EventInfo, `"ae":"`)
}

// jsonStringField returns the string value that follows key in raw JSON text,
// without decoding the document. Good enough for the flat event_info objects;
// escaped quotes inside the value are not expected there.
func jsonStringField(raw string, key string) string {
	var start int = indexOf(raw, key)
	if start < 0 {
		return ""
	}
	start += len(key)
	var end int = start
	for end < len(raw) && raw[end] != '"' {
		end++
	}
	return raw[start:end]
}

// indexOf is strings.Index without importing strings into the types package.
func indexOf(s string, sub string) int {
	var n int = len(sub)
	var i int
	for i = 0; i+n <= len(s); i++ {
		if s[i:i+n] == sub {
			return i
		}
	}
	return -1
}

// TxOutcome — sequencer-level result of a transaction.
type TxOutcome struct {
	Hash   string
	Status TxStatus
	// AppError — application error name when Status is failed ("" otherwise).
	AppError string
	// ExecutedAtMs — execution time reported by the exchange.
	ExecutedAtMs int64
	// Transaction — the full transaction record.
	Transaction Transaction
}

// Executed reports whether the sequencer executed the transaction.
func (o *TxOutcome) Executed() bool { return o.Status == TxStatusExecuted }

// Failed reports whether the sequencer rejected the transaction.
func (o *TxOutcome) Failed() bool { return o.Status == TxStatusFailed }
