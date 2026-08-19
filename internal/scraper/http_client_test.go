package scraper

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSafeHTTPClientBlocksPrivateAddress(t *testing.T) {
	client := NewSafeHTTPClient(200 * time.Millisecond)
	_, err := client.Get("http://127.0.0.1:65535/careers")
	if err == nil {
		t.Fatal("safe client request error = nil, want private address rejection")
	}
	if !strings.Contains(err.Error(), "resolved private address blocked") {
		t.Fatalf("safe client error = %q, want private address rejection", err)
	}
}

func TestSafeHTTPClientUsesPrivateTransport(t *testing.T) {
	client := NewSafeHTTPClient(123 * time.Millisecond)
	if client == nil {
		t.Fatal("client is nil")
	}
	if client.Timeout != 123*time.Millisecond {
		t.Fatalf("timeout = %s, want 123ms", client.Timeout)
	}
	if client.Transport == nil || client.Transport == http.DefaultTransport {
		t.Fatalf("transport = %#v, want private guarded transport", client.Transport)
	}
}
