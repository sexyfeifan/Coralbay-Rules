package main

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Render the actual VLESS branch, including its conditional template logic.
// Fixtures contain no subscription credentials and require no network access.
func TestStashTemplateVLESSTransportSecurity(t *testing.T) {
	data, err := os.ReadFile("templates/clients/perfect-panel/stash.gotmpl")
	if err != nil {
		t.Fatal(err)
	}
	_, branch, ok := strings.Cut(string(data), `{{- else if eq $proxy.Type "vless" }}`)
	if !ok {
		t.Fatal("VLESS branch missing")
	}
	branch, _, ok = strings.Cut(branch, `{{- else if eq $proxy.Type "trojan" }}`)
	if !ok {
		t.Fatal("VLESS branch boundary missing")
	}
	funcs := template.FuncMap{
		"quote": func(v any) string { return strconv.Quote(fmt.Sprint(v)) },
		"default": func(fallback, value any) any {
			if value == nil || reflect.ValueOf(value).IsZero() {
				return fallback
			}
			return value
		},
	}
	prefix := `{{ $proxy := . }}{{ $server := .Server }}{{ $password := "fixture-uuid" }}{{ $common := "udp: true" }}{{ $SkipVerify := false }}`
	tpl, err := template.New("vless").Funcs(funcs).Parse(prefix + "\nproxies:\n" + branch)
	if err != nil {
		t.Fatal(err)
	}
	for _, security := range []string{"reality", "tls", "none", ""} {
		for _, transport := range []string{"tcp", "ws", "grpc"} {
			t.Run(security+"/"+transport, func(t *testing.T) {
				fixture := map[string]any{"Name": "fixture", "Type": "vless", "Server": "example.com", "Port": 443, "Security": security, "Transport": transport, "SNI": "example.net", "RealityPublicKey": "fixture-public-key", "RealityShortId": "abcd", "Flow": "xtls-rprx-vision"}
				var output bytes.Buffer
				if err := tpl.Execute(&output, fixture); err != nil {
					t.Fatal(err)
				}
				var config struct {
					Proxies []map[string]any `yaml:"proxies"`
				}
				if err := yaml.Unmarshal(output.Bytes(), &config); err != nil {
					t.Fatal(err)
				}
				if len(config.Proxies) != 1 {
					t.Fatal("expected one proxy")
				}
				proxy := config.Proxies[0]
				if got := proxy["tls"] == true; got != (security == "tls" || security == "reality") {
					t.Fatalf("security=%q: tls=%v", security, proxy["tls"])
				}
				_, hasReality := proxy["reality-opts"]
				if hasReality != (security == "reality") || proxy["servername"] != "example.net" || proxy["uuid"] != "fixture-uuid" || proxy["flow"] != "xtls-rprx-vision" {
					t.Fatal("unrelated VLESS parameters changed")
				}
			})
		}
	}
}
