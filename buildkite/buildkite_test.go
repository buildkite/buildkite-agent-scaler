package buildkite

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHappy(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"organization": {"slug": "llamacorp"}}`)
	}))
	c := NewClient("testtoken")
	c.Endpoint = s.URL
	m, err := c.GetAgentMetrics([]string{"default"})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
		return
	}
	if want, got := "llamacorp", m.OrgSlug; want != got {
		t.Errorf("OrgSlug: wanted %s, got %s", want, got)
	}
}

func TestUnauthorizedResponse(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message": "Eeep! You forgot to pass an agent registration token"}`)
	}))
	c := NewClient("testtoken")
	c.Endpoint = s.URL
	_, err := c.GetAgentMetrics([]string{"default"})
	if err != nil {
		t.Log("(expected error)", err)
	} else {
		t.Error("expected error representing non-200 HTTP status")
	}
}
