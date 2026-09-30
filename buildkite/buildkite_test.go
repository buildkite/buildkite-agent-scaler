package buildkite

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHappy(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"organization": {"slug": "llamacorp"}}`)
	}))
	c := NewClient("testtoken", "https://agent.buildkite.com/v3")
	c.Endpoint = s.URL
	m, err := c.GetAgentMetrics(context.Background(), "default")
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
	c := NewClient("testtoken", "https://agent.buildkite.com/v3")
	c.Endpoint = s.URL
	c.AgentTokenSource = `SSM parameter "/my/param"`
	_, err := c.GetAgentMetrics(context.Background(), "default")
	if err == nil {
		t.Fatal("expected error representing non-200 HTTP status")
	}
	t.Log("(expected error)", err)

	// The error should read like a title, a description of what went
	// wrong (naming the endpoint hit, where the rejected token came from,
	// and the API's own response body), and a recovery suggestion
	// pointing the user at how to fix it.
	wantParts := []string{
		"couldn't retrieve Buildkite metrics for the `default` queue",
		"from " + s.URL,
		`the Buildkite Agent token retrieved from the SSM parameter "/my/param" was rejected`,
		"Eeep! You forgot to pass an agent registration token",
		"https://buildkite.com/organizations/-/agents",
		`update the SSM parameter "/my/param" with the new value`,
	}
	for _, want := range wantParts {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing expected part %q, got: %s", want, err.Error())
		}
	}
}

func TestUnauthorizedResponseWithEnvVarTokenSource(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"message": "Eeep! You forgot to pass an agent registration token"}`)
	}))
	c := NewClient("testtoken", "https://agent.buildkite.com/v3")
	c.Endpoint = s.URL
	c.AgentTokenSource = "BUILDKITE_AGENT_TOKEN environment variable"
	_, err := c.GetAgentMetrics(context.Background(), "default")
	if err == nil {
		t.Fatal("expected error representing non-200 HTTP status")
	}
	t.Log("(expected error)", err)

	wantParts := []string{
		"couldn't retrieve Buildkite metrics for the `default` queue",
		"from " + s.URL,
		"the Buildkite Agent token retrieved from the BUILDKITE_AGENT_TOKEN environment variable was rejected",
		"Eeep! You forgot to pass an agent registration token",
		"https://buildkite.com/organizations/-/agents",
		"update the BUILDKITE_AGENT_TOKEN environment variable with the new value",
	}
	for _, want := range wantParts {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing expected part %q, got: %s", want, err.Error())
		}
	}
}
