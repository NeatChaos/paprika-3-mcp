package paprika

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
)

const loginPath = "/api/v1/account/login"

// tokenServer issues token-1, token-2, ... on successive logins and answers other requests with
// the given handler, which sees the bearer token that was sent.
func tokenServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, token string)) (http.RoundTripper, func() []recordedRequest, *atomic.Int32) {
	var logins atomic.Int32
	client, requests := fakePaprika(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == loginPath {
			n := logins.Add(1)
			_, _ = fmt.Fprintf(w, `{"result": {"token": "token-%d"}}`, n)
			return
		}
		handle(w, r, r.Header.Get("Authorization"))
	})
	return client.Transport, requests, &logins
}

func newTestClient(t *testing.T, base http.RoundTripper) *Client {
	client, err := newClientWithTransport("user@example.com", "hunter2", "test", slog.New(slog.NewTextHandler(io.Discard, nil)), base)
	require.NoError(t, err)
	return client
}

func TestReloginOn401(t *testing.T) {
	base, requests, _ := tokenServer(t, func(w http.ResponseWriter, r *http.Request, token string) {
		if token != "Bearer token-2" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"result": []}`))
	})
	client := newTestClient(t, base)

	_, err := client.ListRecipes(context.Background())
	require.NoError(t, err)

	reqs := requests()
	require.Len(t, reqs, 4)
	assert.Equal(t, loginPath, reqs[0].Path)
	assert.Equal(t, "/api/v2/sync/recipes", reqs[1].Path)
	assert.Equal(t, "Bearer token-1", reqs[1].Header.Get("Authorization"))
	assert.Equal(t, loginPath, reqs[2].Path)
	assert.Empty(t, reqs[2].Header.Get("Authorization"))
	assert.Equal(t, "/api/v2/sync/recipes", reqs[3].Path)
	assert.Equal(t, "Bearer token-2", reqs[3].Header.Get("Authorization"))

	t.Run("a replayed POST sends the original body", func(t *testing.T) {
		base, requests, _ := tokenServer(t, func(w http.ResponseWriter, r *http.Request, token string) {
			if token != "Bearer token-2" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"result": true}`))
		})
		client := newTestClient(t, base)

		require.NoError(t, client.postV2(context.Background(), "https://paprikaapp.com/api/v2/sync/recipe/X/", map[string]string{"name": "Soup"}))

		reqs := requests()
		require.Len(t, reqs, 4)
		assert.NotEmpty(t, reqs[1].Body)
		assert.Equal(t, reqs[1].Body, reqs[3].Body)
		assert.Equal(t, reqs[1].Header.Get("Content-Type"), reqs[3].Header.Get("Content-Type"))
	})
}

func TestReloginOnlyOnce(t *testing.T) {
	base, _, logins := tokenServer(t, func(w http.ResponseWriter, r *http.Request, token string) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	client := newTestClient(t, base)

	_, err := client.ListRecipes(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrUnauthorized)
	assert.Equal(t, int32(2), logins.Load())
}

func TestConcurrent401SingleLogin(t *testing.T) {
	var stale sync.WaitGroup
	stale.Add(3)
	base, _, logins := tokenServer(t, func(w http.ResponseWriter, r *http.Request, token string) {
		if token == "Bearer token-1" {
			stale.Done()
			stale.Wait()
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"result": []}`))
	})
	client := newTestClient(t, base)

	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = client.ListRecipes(context.Background())
		}()
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
	assert.Equal(t, int32(2), logins.Load())
}

func TestRetryAfter(t *testing.T) {
	newTransport := func(retryAfter string, slept *[]time.Duration) (*retryTransport, *int) {
		calls := 0
		return &retryTransport{
			maxRetries: 3,
			baseDelay:  time.Millisecond,
			sleep: func(ctx context.Context, d time.Duration) error {
				*slept = append(*slept, d)
				return nil
			},
			transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					header := http.Header{}
					header.Set("Retry-After", retryAfter)
					return &http.Response{StatusCode: http.StatusTooManyRequests, Header: header, Body: io.NopCloser(bytes.NewReader(nil))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
			}),
		}, &calls
	}

	t.Run("waits the requested seconds, then retries", func(t *testing.T) {
		var slept []time.Duration
		transport, calls := newTransport("2", &slept)
		req, err := http.NewRequest(http.MethodGet, "https://paprikaapp.com/x", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, *calls)
		require.Len(t, slept, 1)
		assert.GreaterOrEqual(t, slept[0], 2*time.Second)
	})

	t.Run("a wait above 30 seconds returns the 429 at once", func(t *testing.T) {
		var slept []time.Duration
		transport, calls := newTransport("31", &slept)
		req, err := http.NewRequest(http.MethodGet, "https://paprikaapp.com/x", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
		assert.Equal(t, 1, *calls)
		assert.Empty(t, slept)
	})

	t.Run("an unparseable value falls back to backoff", func(t *testing.T) {
		var slept []time.Duration
		transport, calls := newTransport("Wed, 21 Oct 2015 07:28:00 GMT", &slept)
		req, err := http.NewRequest(http.MethodGet, "https://paprikaapp.com/x", nil)
		require.NoError(t, err)

		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, *calls)
		assert.Equal(t, []time.Duration{time.Millisecond}, slept)
	})
}

func TestRateLimit(t *testing.T) {
	transport := &rateLimitTransport{
		limiter: rate.NewLimiter(1000, 1),
		transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
		}),
	}

	start := time.Now()
	for range 5 {
		req, err := http.NewRequest(http.MethodGet, "https://paprikaapp.com/x", nil)
		require.NoError(t, err)
		_, err = transport.RoundTrip(req)
		require.NoError(t, err)
	}
	assert.GreaterOrEqual(t, time.Since(start), 4*time.Millisecond)

	t.Run("a cancelled context stops the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://paprikaapp.com/x", nil)
		require.NoError(t, err)
		_, err = transport.RoundTrip(req)
		assert.True(t, errors.Is(err, context.Canceled))
	})
}

func TestLoginErrorOmitsSecrets(t *testing.T) {
	const password = "s3cret-pa55word"
	const marker = "BODY-MARKER-do-not-leak"

	for name, respond := range map[string]func(w http.ResponseWriter){
		"forbidden": func(w http.ResponseWriter) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(marker))
		},
		"ok without a token": func(w http.ResponseWriter) {
			_, _ = w.Write([]byte(`{"result": {}, "detail": "` + marker + `"}`))
		},
	} {
		t.Run(name, func(t *testing.T) {
			client, _ := fakePaprika(t, func(w http.ResponseWriter, r *http.Request) { respond(w) })
			var logs bytes.Buffer
			logger := slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

			_, err := newClientWithTransport("user@example.com", password, "test", logger, client.Transport)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), password)
			assert.NotContains(t, err.Error(), marker)
			assert.NotContains(t, logs.String(), password)
			assert.NotContains(t, logs.String(), marker)
		})
	}
}
