package subscription

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"testing"
)

const testURL = "vless://11111111-2222-4333-8444-555555555555@192.0.2.10:443?security=reality&type=raw&sni=www.example.com&fp=edge&pbk=test-public-key&sid=0123456789abcdef#%F0%9F%87%B3%F0%9F%87%B1%20NL"

func TestDecodeBase64Subscription(t *testing.T) {
	payload := testURL + "\n" + testURL + "\n"
	encoded := base64.RawStdEncoding.EncodeToString([]byte(payload))
	nodes, err := Decode([]byte(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("expected duplicate to be removed, got %d nodes", len(nodes))
	}
	node := nodes[0]
	if node.Address != "192.0.2.10" || node.Port != 443 || node.Network != "raw" {
		t.Fatalf("unexpected node: %+v", node)
	}
	if node.Name != "🇳🇱 NL" {
		t.Fatalf("unexpected decoded name %q", node.Name)
	}
	if node.Fingerprint != "edge" || node.ShortID != "0123456789abcdef" {
		t.Fatalf("reality fields were not parsed: %+v", node)
	}
}

func TestFetchErrorDoesNotLeakSubscriptionURL(t *testing.T) {
	secret := "subscription-secret-token"
	client := &Client{http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("synthetic network error")
	})}}
	_, err := client.Fetch(t.Context(), "https://example.invalid/"+secret)
	if err == nil {
		t.Fatal("expected timeout")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked subscription URL: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestParseVLESSRejectsUnsupportedTransport(t *testing.T) {
	_, err := ParseVLESS("vless://id@192.0.2.1:443?security=reality&type=ws&sni=x&pbk=y")
	if err == nil {
		t.Fatal("expected unsupported transport error")
	}
}

func TestNodeIDDoesNotChangeWithDisplayName(t *testing.T) {
	a, err := ParseVLESS(testURL)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseVLESS(testURL[:len(testURL)-len("%F0%9F%87%B3%F0%9F%87%B1%20NL")] + "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("display name changed stable node id: %s != %s", a.ID, b.ID)
	}
}
