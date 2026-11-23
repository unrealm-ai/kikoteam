package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"
)

const mailTestReceiptLifetime = 10 * time.Minute
const mailTestReceiptPayloadSize = 8 + sha256.Size

// Only expiry and an authenticated tag travel to the browser. The effective
// secret is part of the tag's keyed input but never appears in the receipt.
func (s *MailSettingsService) signMailTestReceipt(actor string, in MailSettingsDraft, deadline uint64) (string, error) {
	if len(s.Secret) < 32 {
		return "", errors.New("SMTP test receipt signing unavailable")
	}
	mac := hmac.New(sha256.New, s.Secret)
	msg := binary.BigEndian.AppendUint64(make([]byte, 0, mailTestReceiptPayloadSize), deadline)
	_, _ = mac.Write([]byte("kiko:platform:smtp-test-receipt:v1:"))
	_, _ = mac.Write(msg)
	var version [8]byte
	binary.BigEndian.PutUint64(version[:], uint64(in.Version))
	_, _ = mac.Write(version[:])
	tlsMode := byte(0)
	if in.ImplicitTLS {
		tlsMode = 1
	}
	_, _ = mac.Write([]byte{tlsMode})
	for _, value := range []string{actor, in.Address, strings.ToLower(in.Host), in.Username, in.From, in.Password} {
		var size [binary.MaxVarintLen64]byte
		n := binary.PutUvarint(size[:], uint64(len(value)))
		_, _ = mac.Write(size[:n])
		_, _ = mac.Write([]byte(value))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(msg)), nil
}

func (s *MailSettingsService) issueMailTestReceipt(actor string, in MailSettingsDraft, now time.Time) (string, error) {
	return s.signMailTestReceipt(actor, in, uint64(now.Add(mailTestReceiptLifetime).Unix()))
}

func (s *MailSettingsService) validMailTestReceipt(actor string, in MailSettingsDraft, now time.Time) bool {
	raw := in.TestReceipt
	if len(raw) != base64.RawURLEncoding.EncodedLen(mailTestReceiptPayloadSize) {
		return false
	}
	msg, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(msg) != mailTestReceiptPayloadSize {
		return false
	}
	deadline := binary.BigEndian.Uint64(msg)
	if deadline <= uint64(now.Unix()) {
		return false
	}
	expected, err := s.signMailTestReceipt(actor, in, deadline)
	return err == nil && hmac.Equal([]byte(raw), []byte(expected))
}
