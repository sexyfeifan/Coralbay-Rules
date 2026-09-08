package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const mrsTextMaxBytes = 32 << 20

// The image pins the decoder by digest. It exports the original set, without
// substituting the independently maintained geo lists used by older links.
func decodeLegacyMRS(ctx context.Context, behavior string, raw []byte) ([]string, error) {
	if (behavior != "domain" && behavior != "ipcidr") || len(raw) == 0 || len(raw) > legacyResourceMaxBytes {
		return nil, fmt.Errorf("MRS 格式或大小无效")
	}
	dir, err := os.MkdirTemp("", "coralbay-mrs-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	input, output := filepath.Join(dir, "input.mrs"), filepath.Join(dir, "output.txt")
	if err = os.WriteFile(input, raw, 0600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/local/bin/coralbay-probe-core", "convert-ruleset", behavior, "mrs", input, output)
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("原始 MRS 解码失败，请确认镜像内核可用：%w", err)
	}
	data, err := routingReadBoundedFile(output, mrsTextMaxBytes)
	if err != nil {
		return nil, err
	}
	entries := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(entries) == 0 || entries[0] == "" {
		return nil, fmt.Errorf("原始 MRS 解码结果为空")
	}
	return entries, nil
}

type decodedMRSCache struct {
	InputSHA string `json:"input_sha256"`
	TextSHA  string `json:"text_sha256"`
	Behavior string `json:"behavior"`
	Text     string `json:"text"`
}

func (s *server) decodedMRS(ctx context.Context, behavior string, raw []byte) ([]string, error) {
	s.mrsDecodeMu.Lock()
	defer s.mrsDecodeMu.Unlock()
	sha := routingSHA256(raw)
	path := filepath.Join(s.dataDir, "rule-projections", "mrs-text-v1", behavior+"-"+sha+".json")
	var cached decodedMRSCache
	if data, err := routingReadBoundedFile(path, 2*mrsTextMaxBytes); err == nil && json.Unmarshal(data, &cached) == nil && cached.InputSHA == sha && cached.Behavior == behavior && len(cached.Text) > 0 && len(cached.Text) <= mrsTextMaxBytes && cached.TextSHA == routingSHA256([]byte(cached.Text)) {
		return strings.Split(cached.Text, "\n"), nil
	}
	decoder := s.mrsDecoder
	if decoder == nil {
		decoder = decodeLegacyMRS
	}
	entries, err := decoder(ctx, behavior, raw)
	if err != nil {
		return nil, err
	}
	joined := strings.Join(entries, "\n")
	if len(joined) == 0 || len(joined) > mrsTextMaxBytes {
		return nil, fmt.Errorf("解码条目为空或超过上限")
	}
	data, err := json.Marshal(decodedMRSCache{InputSHA: sha, TextSHA: routingSHA256([]byte(joined)), Behavior: behavior, Text: joined})
	if err != nil {
		return nil, err
	}
	if err = routingRulesAtomicWrite(path, data); err != nil {
		return nil, err
	}
	return entries, nil
}
