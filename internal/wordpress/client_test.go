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

func TestValidJSONOfTheWrongShapeKeepsItsError(t *testing.T) {
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"wp_version": 6.9}`)
	}))
	defer plugin.Close()
	_, err := New(map[string]string{"acmedev": plugin.URL}, &countingTokens{}, nil).State(context.Background(), "acmedev")
	if err == nil || strings.Contains(err.Error(), "other than JSON") || !strings.Contains(err.Error(), "wp_version") {
		t.Fatalf("err %v", err)
	}
}

// A queued write gets its token when its turn comes, not while it waits.
func TestAQueuedWriteGetsItsTokenWhenItsTurnComes(t *testing.T) {
	var mu sync.Mutex
	var events []string
	release := make(chan struct{})
	plugin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		events = append(events, "request "+r.Header.Get("Authorization"))
		first := len(events) == 2
		mu.Unlock()
		if first {
			<-release
		}
		fmt.Fprint(w, `{"exists": true}`)
	}))
	defer plugin.Close()
	tokens := tokenFunc(func(string) string {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, fmt.Sprintf("token %d", len(events)))
		return fmt.Sprintf("t%d", len(events))
	})
	client := New(map[string]string{"acmedev": plugin.URL}, tokens, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = client.PutOption(context.Background(), "acmedev", "a", "1") }()
	for {
		mu.Lock()
		n := len(events)
		mu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	wg.Add(1)
	go func() { defer wg.Done(); _, _ = client.PutOption(context.Background(), "acmedev", "b", "2") }()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	queued := len(events)
	mu.Unlock()
	close(release)
	wg.Wait()
	if queued != 2 {
		t.Fatalf("the queued write got a token while waiting: %v", events)
	}
}

type tokenFunc func(audience string) string

func (f tokenFunc) Token(_ context.Context, audience string) (string, error) {
	return f(audience), nil
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
