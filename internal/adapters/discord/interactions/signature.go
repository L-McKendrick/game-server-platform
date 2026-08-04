package interactions

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	errInvalidSignature = errors.New("invalid Discord interaction signature")
	errStaleTimestamp   = errors.New("stale Discord interaction timestamp")
)

// ParsePublicKey decodes a hexadecimal Discord Ed25519 public key.
func ParsePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := hex.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("decode Discord public key: %w", err)
	}

	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf(
			"Discord public key must decode to %d bytes; got %d",
			ed25519.PublicKeySize,
			len(decoded),
		)
	}

	return ed25519.PublicKey(decoded), nil
}

func verifySignature(
	publicKey ed25519.PublicKey,
	signatureHex string,
	timestampValue string,
	body []byte,
	now time.Time,
	maxAge time.Duration,
) error {
	if len(publicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("Discord public key is invalid")
	}

	timestampValue = strings.TrimSpace(timestampValue)
	if timestampValue == "" {
		return fmt.Errorf("%w: missing timestamp", errInvalidSignature)
	}

	unixSeconds, err := strconv.ParseInt(timestampValue, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: parse timestamp: %v", errInvalidSignature, err)
	}

	requestTime := time.Unix(unixSeconds, 0).UTC()
	age := now.UTC().Sub(requestTime)
	if age < -maxAge || age > maxAge {
		return fmt.Errorf(
			"%w: request time %s differs from current time by %s",
			errStaleTimestamp,
			requestTime.Format(time.RFC3339),
			age,
		)
	}

	signature, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil {
		return fmt.Errorf("%w: decode signature: %v", errInvalidSignature, err)
	}

	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf(
			"%w: signature must decode to %d bytes",
			errInvalidSignature,
			ed25519.SignatureSize,
		)
	}

	message := make([]byte, 0, len(timestampValue)+len(body))
	message = append(message, timestampValue...)
	message = append(message, body...)

	if !ed25519.Verify(publicKey, message, signature) {
		return errInvalidSignature
	}

	return nil
}
