package xray

import (
	"encoding/json"
	"testing"

	"github.com/faddey/xray-monitor/internal/subscription"
)

func TestBuildConfigRoutesEveryInbound(t *testing.T) {
	node := subscription.Node{
		ID: "node-1", Address: "192.0.2.1", Port: 443,
		UUID: "test-id", Network: "raw", Security: "reality",
		ServerName: "www.example.com", Fingerprint: "chrome",
		PublicKey: "public-key", ShortID: "abcd",
	}
	b, err := BuildConfig([]RuntimeNode{{Node: node, LocalPort: 32123}}, "127.0.0.1", "warning")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	inbounds := cfg["inbounds"].([]any)
	if got := inbounds[0].(map[string]any)["listen"]; got != "127.0.0.1" {
		t.Fatalf("SOCKS listener is exposed: %v", got)
	}
	routing := cfg["routing"].(map[string]any)
	rule := routing["rules"].([]any)[0].(map[string]any)
	if rule["outboundTag"] != "monitor-out-0" {
		t.Fatalf("unexpected routing rule: %#v", rule)
	}
	outbounds := cfg["outbounds"].([]any)
	if outbounds[0].(map[string]any)["protocol"] != "blackhole" {
		t.Fatal("the default outbound must fail closed")
	}
	outbound := outbounds[1].(map[string]any)
	stream := outbound["streamSettings"].(map[string]any)
	reality := stream["realitySettings"].(map[string]any)
	if reality["publicKey"] != "public-key" || reality["serverName"] != "www.example.com" {
		t.Fatalf("unexpected reality settings: %#v", reality)
	}
}
