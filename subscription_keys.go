package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type subscriptionKeys struct {
	Key         []byte    `json:"key"`
	Legacy      []byte    `json:"legacy"`
	LegacyUntil time.Time `json:"legacy_until"`
}

func (s *server) loadSubscriptionKeys() (subscriptionKeys, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var keys subscriptionKeys
	path := filepath.Join(s.dataDir, "settings", "subscription-keys.json")
	data, err := os.ReadFile(path)
	if err == nil {
		err = json.Unmarshal(data, &keys)
		if err == nil && len(keys.Key) != 32 {
			err = fmt.Errorf("invalid signing key")
		}
		return keys, err
	}
	if !os.IsNotExist(err) {
		return keys, err
	}
	keys.Key = make([]byte, 32)
	if _, err = rand.Read(keys.Key); err != nil {
		return keys, err
	}
	keys.Legacy = s.sessionKey()
	keys.LegacyUntil = time.Now().UTC().Add(90 * 24 * time.Hour)
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return keys, err
	}
	data, err = json.Marshal(keys)
	if err != nil {
		return keys, err
	}
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return keys, err
	}
	err = os.Rename(path+".tmp", path)
	return keys, err
}
func signatureWith(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *server) subscriptionLink(p url.Values) (string, error) {
	keys, err := s.loadSubscriptionKeys()
	if err != nil {
		return "", err
	}
	canonical := p.Encode()
	return "https://" + s.domain + "/sub?" + canonical + "&sig=" + url.QueryEscape("v2."+signatureWith(keys.Key, canonical)), nil
}
func (s *server) verifySubscriptionSignature(sig, canonical string) bool {
	keys, err := s.loadSubscriptionKeys()
	if err != nil {
		return false
	}
	if strings.HasPrefix(sig, "v2.") {
		return secureEqual(sig, "v2."+signatureWith(keys.Key, canonical))
	}
	return time.Now().Before(keys.LegacyUntil) && secureEqual(sig, signatureWith(keys.Legacy, canonical))
}
