package paprika

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ErrUnauthorized means Paprika rejected the request even after a fresh login.
var ErrUnauthorized = errors.New("paprika rejected the credentials")

const (
	requestsPerSecond = 4
	requestBurst      = 3
	maxRetryAfter     = 30 * time.Second
)

// authTransport adds the bearer token and, on a 401, logs in again once and replays the request.
type authTransport struct {
	transport http.RoundTripper
	login     func(ctx context.Context) (string, error)
	logger    *slog.Logger

	mu    sync.Mutex
	token string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	token := t.currentToken()
	resp, err := t.send(req, req.Body, token)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	discard(resp)

	body := req.Body
	if req.GetBody != nil {
		if body, err = req.GetBody(); err != nil {
			return nil, fmt.Errorf("failed to replay request body: %w", err)
		}
	} else if req.Body != nil && req.Body != http.NoBody {
		return nil, fmt.Errorf("%w: request body cannot be replayed", ErrUnauthorized)
	}

	fresh, err := t.refresh(req.Context(), token)
	if err != nil {
		return nil, fmt.Errorf("%w: login failed: %w", ErrUnauthorized, err)
	}

	resp, err = t.send(req, body, fresh)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		discard(resp)
		return nil, ErrUnauthorized
	}
	return resp, nil
}

func (t *authTransport) send(req *http.Request, body io.ReadCloser, token string) (*http.Response, error) {
	out := req.Clone(req.Context())
	out.Body = body
	out.Header.Set("Authorization", "Bearer "+token)
	return t.transport.RoundTrip(out)
}

func (t *authTransport) currentToken() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.token
}

// refresh logs in again unless another request already replaced the rejected token.
func (t *authTransport) refresh(ctx context.Context, rejected string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.token != rejected {
		return t.token, nil
	}
	t.logger.Info("paprika token rejected, logging in again")
	token, err := t.login(ctx)
	if err != nil {
		return "", err
	}
	t.token = token
	return token, nil
}

type rateLimitTransport struct {
	transport http.RoundTripper
	limiter   *rate.Limiter
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.transport.RoundTrip(req)
}

// retryAfter returns the server's requested delay in seconds, if it sent one.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}
	seconds, err := strconv.Atoi(resp.Header.Get("Retry-After"))
	if err != nil || seconds < 0 {
		return 0, false
	}
	return time.Duration(seconds) * time.Second, true
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// newClientWithTransport logs in and builds a client whose requests go, outermost first, through
// re-login on 401, retries, the rate limit and the default headers before reaching base.
func newClientWithTransport(username, password, version string, logger *slog.Logger, base http.RoundTripper) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}

	retrying := &retryTransport{
		maxRetries: 3,
		baseDelay:  500 * time.Millisecond,
		transport: &rateLimitTransport{
			limiter: rate.NewLimiter(requestsPerSecond, requestBurst),
			transport: &roundTripper{
				transport: base,
				headers: map[string]string{
					"Accept":     "*/*",
					"Connection": "keep-alive",
					"User-Agent": userAgent(version),
				},
			},
		},
	}
	loginClient := http.Client{Transport: retrying, Timeout: 30 * time.Second}
	auth := &authTransport{
		transport: retrying,
		logger:    logger,
		login: func(ctx context.Context) (string, error) {
			return login(ctx, loginClient, username, password)
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	token, err := auth.login(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to login: %w", err)
	}
	auth.token = token

	return &Client{
		client: &http.Client{Transport: auth, Timeout: 30 * time.Second},
		logger: logger,
	}, nil
}
