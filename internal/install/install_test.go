package install

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestFetchPublicIPv4(t *testing.T) {
	client := &http.Client{Transport: installRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(" 8.8.8.8\n")),
			Header:     make(http.Header),
		}, nil
	})}

	got, err := fetchPublicIPv4(client, "https://public-ip.invalid")
	if err != nil {
		t.Fatal(err)
	}
	if got != "8.8.8.8" {
		t.Fatalf("got %q, want 8.8.8.8", got)
	}
}

func TestFetchPublicIPv4RejectsPrivateAddress(t *testing.T) {
	client := &http.Client{Transport: installRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader("192.168.1.10")),
			Header:     make(http.Header),
		}, nil
	})}

	if _, err := fetchPublicIPv4(client, "https://public-ip.invalid"); err == nil {
		t.Fatal("expected private address to be rejected")
	}
}

func TestFetchPublicIPv4ReturnsTransportError(t *testing.T) {
	client := &http.Client{Transport: installRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}

	if _, err := fetchPublicIPv4(client, "https://public-ip.invalid"); err == nil {
		t.Fatal("expected transport error")
	}
}

type installRoundTripFunc func(*http.Request) (*http.Response, error)

func (f installRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
