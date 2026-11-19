// Package slack implements the Slack bot: signed event intake, ack-then-process
// answering through a bounded worker pool, and message updates (spec AC7).
package slack

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ErrBadSignature means the request is not signed with the app's signing secret.
var ErrBadSignature = errors.New("slack: bad signature")

// ErrStaleTimestamp means the request is outside Slack's replay window.
var ErrStaleTimestamp = errors.New("slack: stale timestamp")

// maxSkew is Slack's documented replay window.
const maxSkew = 5 * time.Minute

// Verify checks Slack's v0 signature: HMAC-SHA256 over "v0:<timestamp>:<body>",
// sent as "v0=<hex>", within the replay window around now.
func Verify(signingSecret string, body []byte, timestamp, signature string, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: timestamp", ErrBadSignature)
	}
	if d := now.Sub(time.Unix(ts, 0)); d > maxSkew || d < -maxSkew {
		return ErrStaleTimestamp
	}
	hexSig, ok := strings.CutPrefix(signature, "v0=")
	if !ok {
		return ErrBadSignature
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(signingSecret))
	mac.Write([]byte("v0:" + timestamp + ":"))
	mac.Write(body)
	if !hmac.Equal(got, mac.Sum(nil)) {
		return ErrBadSignature
	}
	return nil
}
