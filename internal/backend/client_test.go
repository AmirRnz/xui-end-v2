package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallUsesScopedServiceCredentialAndActor(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-test-token" || r.Header.Get("X-Actor-Telegram-ID") != "12345" {
			t.Fatalf("missing scoped auth headers")
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer s.Close()
	c, err := New(s.URL, "secret-test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err = c.Call(context.Background(), http.MethodGet, "/v1/me", 12345, nil, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal("response was not decoded")
	}
}
