/*
FILE: internal/rest/encode.go

DESCRIPTION:
Allocation-conscious builders of query strings and form bodies.

  Query — "market_id=0&limit=100" for GET endpoints (values are integers,
          enum words or symbols; strings are percent-encoded).
  Form  — application/x-www-form-urlencoded body of sendTx / sendTxBatch:
          tx_type=14&tx_info=%7B...%7D&price_protection=true
          tx_types=%5B14%2C15%5D&tx_infos=%5B%22%7B...%7D%22%2C...%5D
          The batch fields are JSON: an array of integers and an array of
          STRINGS (each element is the tx_info JSON text, escaped), exactly
          what the official Python SDK sends (json.dumps of the lists).

Percent-encoding follows net/url.QueryEscape: unreserved characters
[A-Za-z0-9-_.~] stay, space becomes "+", everything else is %XX.
*/

package rest

import "strconv"

// upperHex — hex alphabet of percent-encoding.
const upperHex string = "0123456789ABCDEF"

// shouldEscape reports whether c must be percent-encoded in a query value.
func shouldEscape(c byte) bool {
	if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
		return false
	}
	switch c {
	case '-', '_', '.', '~':
		return false
	}
	return true
}

// AppendEscaped appends s percent-encoded (QueryEscape rules) to b.
func AppendEscaped(b []byte, s []byte) []byte {
	var i int
	for i = 0; i < len(s); i++ {
		var c byte = s[i]
		switch {
		case c == ' ':
			b = append(b, '+')
		case shouldEscape(c):
			b = append(b, '%', upperHex[c>>4], upperHex[c&15])
		default:
			b = append(b, c)
		}
	}
	return b
}

// AppendEscapedString — AppendEscaped for a string.
func AppendEscapedString(b []byte, s string) []byte {
	var i int
	for i = 0; i < len(s); i++ {
		var c byte = s[i]
		switch {
		case c == ' ':
			b = append(b, '+')
		case shouldEscape(c):
			b = append(b, '%', upperHex[c>>4], upperHex[c&15])
		default:
			b = append(b, c)
		}
	}
	return b
}

// Query — query-string builder. The zero value is ready to use.
type Query struct {
	buf []byte
}

// sep appends "&" between parameters.
func (q *Query) sep() {
	if len(q.buf) > 0 {
		q.buf = append(q.buf, '&')
	}
}

// Int adds an integer parameter.
func (q *Query) Int(key string, v int64) *Query {
	q.sep()
	q.buf = append(q.buf, key...)
	q.buf = append(q.buf, '=')
	q.buf = strconv.AppendInt(q.buf, v, 10)
	return q
}

// Bool adds a boolean parameter.
func (q *Query) Bool(key string, v bool) *Query {
	q.sep()
	q.buf = append(q.buf, key...)
	if v {
		q.buf = append(q.buf, "=true"...)
	} else {
		q.buf = append(q.buf, "=false"...)
	}
	return q
}

// Str adds a string parameter (percent-encoded).
func (q *Query) Str(key string, v string) *Query {
	q.sep()
	q.buf = append(q.buf, key...)
	q.buf = append(q.buf, '=')
	q.buf = AppendEscapedString(q.buf, v)
	return q
}

// String returns the encoded query (without "?").
func (q *Query) String() string { return string(q.buf) }

// AppendSendTxForm appends the sendTx form body to b.
func AppendSendTxForm(b []byte, txType uint8, txInfo []byte, priceProtection bool) []byte {
	b = append(b, "tx_type="...)
	b = strconv.AppendUint(b, uint64(txType), 10)
	b = append(b, "&tx_info="...)
	b = AppendEscaped(b, txInfo)
	if priceProtection {
		b = append(b, "&price_protection=true"...)
	} else {
		b = append(b, "&price_protection=false"...)
	}
	return b
}

// appendJSONQuoted appends s as a JSON string literal (quotes and backslashes
// escaped; tx_info never contains control characters).
func appendJSONQuoted(b []byte, s []byte) []byte {
	b = append(b, '"')
	var i int
	for i = 0; i < len(s); i++ {
		var c byte = s[i]
		if c == '"' || c == '\\' {
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	return append(b, '"')
}

// AppendSendTxBatchForm appends the sendTxBatch form body to b. scratch is a
// reusable buffer for the intermediate JSON (may be nil).
func AppendSendTxBatchForm(b []byte, scratch []byte, txTypes []uint8, txInfos [][]byte) ([]byte, []byte) {
	scratch = scratch[:0]
	scratch = append(scratch, '[')
	var i int
	for i = 0; i < len(txTypes); i++ {
		if i > 0 {
			scratch = append(scratch, ',')
		}
		scratch = strconv.AppendUint(scratch, uint64(txTypes[i]), 10)
	}
	scratch = append(scratch, ']')
	b = append(b, "tx_types="...)
	b = AppendEscaped(b, scratch)

	scratch = scratch[:0]
	scratch = append(scratch, '[')
	for i = 0; i < len(txInfos); i++ {
		if i > 0 {
			scratch = append(scratch, ',')
		}
		scratch = appendJSONQuoted(scratch, txInfos[i])
	}
	scratch = append(scratch, ']')
	b = append(b, "&tx_infos="...)
	b = AppendEscaped(b, scratch)
	return b, scratch
}

// AppendJSONTxTypes appends the JSON array of tx types ("[14,15]") — the
// tx_types value of the WebSocket batch message.
func AppendJSONTxTypes(b []byte, txTypes []uint8) []byte {
	b = append(b, '[')
	var i int
	for i = 0; i < len(txTypes); i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendUint(b, uint64(txTypes[i]), 10)
	}
	return append(b, ']')
}

// AppendJSONTxInfos appends the JSON array of quoted tx_info strings — the
// tx_infos value of the WebSocket batch message.
func AppendJSONTxInfos(b []byte, txInfos [][]byte) []byte {
	b = append(b, '[')
	var i int
	for i = 0; i < len(txInfos); i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = appendJSONQuoted(b, txInfos[i])
	}
	return append(b, ']')
}
