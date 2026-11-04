package wordpress

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

	// Without an HTTP client, as the provider calls it.
	token, err := ActionsTokens(nil).Token(context.Background(), "https://acmedev.wpengine.com")
	if err != nil || token != "oidc-token" || gotAuth != "bearer request-token" || gotAudience != "https://acmedev.wpengine.com" {
		t.Fatalf("token %q, err %v, auth %q, audience %q", token, err, gotAuth, gotAudience)
	}
}

func TestAResponseThatIsNotJSONIsExplained(t *testing.T) {
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<div id="error"><p class="wpdberror">WordPress database error</p></div>{"exists": true}`)
	}))
	defer plugin.Close()
	_, err := New(map[string]string{"acmedev": plugin.URL}, &countingTokens{}, nil).PutOption(context.Background(), "acmedev", "a", "1")
	if err == nil || !strings.Contains(err.Error(), "something other than JSON") || !strings.Contains(err.Error(), "wpdberror") {
		t.Fatalf("err %v", err)
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

type countingTokens struct {
	mu sync.Mutex
	n  int
}

func (c *countingTokens) Token(_ context.Context, audience string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return fmt.Sprintf("token-%d", c.n), nil
}

func TestConcurrentReadsShareOneRequest(t *testing.T) {
	var reads atomic.Int32
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, `{"options": {}}`)
	}))
	defer plugin.Close()
	client := New(map[string]string{"acmedev": plugin.URL}, &countingTokens{}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = client.State(context.Background(), "acmedev")
		}()
	}
	wg.Wait()
	if reads.Load() != 1 {
		t.Fatalf("%d reads", reads.Load())
	}
}

func TestWritesToAnInstallAreOneAtATime(t *testing.T) {
	var inFlight, most atomic.Int32
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := inFlight.Add(1)
		for {
			seen := most.Load()
			if now <= seen || most.CompareAndSwap(seen, now) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		fmt.Fprint(w, `{"exists": true}`)
	}))
	defer plugin.Close()
	client := New(map[string]string{"acmedev": plugin.URL}, &countingTokens{}, nil)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = client.PutOption(context.Background(), "acmedev", fmt.Sprint("option", i), "1")
		}(i)
	}
	wg.Wait()
	if most.Load() != 1 {
		t.Fatalf("%d writes at once", most.Load())
	}
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
