package wordpress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActionsTokensAskGitHubForTheAudience(t *testing.T) {
	var gotAuth, gotAudience string
	github := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAudience = r.Header.Get("Authorization"), r.URL.Query().Get("audience")
		fmt.Fprint(w, `{"value": "oidc-token"}`)
	}))
	defer github.Close()
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", github.URL+"/token?api-version=2.0")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")

	token, err := ActionsTokens(github.Client()).Token(context.Background(), "https://acmedev.wpengine.com")
	if err != nil || token != "oidc-token" || gotAuth != "bearer request-token" || gotAudience != "https://acmedev.wpengine.com" {
		t.Fatalf("token %q, err %v, auth %q, audience %q", token, err, gotAuth, gotAudience)
	}
}

func TestOutsideActionsThereAreNoTokens(t *testing.T) {
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	if ActionsTokens(nil) != nil {
		t.Fatal("tokens outside Actions")
	}
	_, err := New(nil, nil, nil).State(context.Background(), "acmedev")
	if !errors.Is(err, ErrNotInCI) {
		t.Fatalf("err %v", err)
	}
}

type countingTokens struct{ n int }

func (c *countingTokens) Token(_ context.Context, audience string) (string, error) {
	c.n++
	return fmt.Sprintf("token-%d", c.n), nil
}

func TestReadTokensAreReusedAndWritesGetFreshOnes(t *testing.T) {
	var seen []string
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Query().Get("rest_route")+" "+r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"options": {}}`)
	}))
	defer plugin.Close()
	tokens := &countingTokens{}
	client := New(map[string]string{"acmedev": plugin.URL}, tokens, nil)
	ctx := context.Background()

	_, _ = client.State(ctx, "acmedev")
	_, _ = client.State(ctx, "acmedev") // cached: no request
	_, _ = client.PutOption(ctx, "acmedev", "a", "1")
	_, _ = client.PutOption(ctx, "acmedev", "b", "2")
	_, _ = client.State(ctx, "acmedev") // invalidated by the writes: read again, with the cached read token

	want := []string{
		"GET /metatavu-iac/v1/state Bearer token-1",
		"PUT /metatavu-iac/v1/options/a Bearer token-2",
		"PUT /metatavu-iac/v1/options/b Bearer token-3",
		"GET /metatavu-iac/v1/state Bearer token-1",
	}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Fatalf("got %q\nwant %q", seen, want)
	}
}
