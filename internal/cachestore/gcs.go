package cachestore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// DefaultEndpoint is the Cloud Storage JSON API
const DefaultEndpoint = "https://storage.googleapis.com"

const metadataTokenURL = "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"

// tokenExpiryMargin renews a metadata token this long before it expires
const tokenExpiryMargin = 60 * time.Second

// GCS stores the cache object in Cloud Storage through the JSON API
type GCS struct {
	bucket   string
	object   string
	hc       *http.Client
	endpoint string
	token    func(context.Context) (string, error)
}

func NewGCS(bucket, object string, hc *http.Client, endpoint string, token func(context.Context) (string, error)) *GCS {
	return &GCS{bucket: bucket, object: object, hc: hc, endpoint: endpoint, token: token}
}

func (g *GCS) Load(ctx context.Context) ([]byte, int64, error) {
	u := fmt.Sprintf("%s/storage/v1/b/%s/o/%s?alt=media", g.endpoint, url.PathEscape(g.bucket), url.PathEscape(g.object))
	resp, err := g.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, 0, ErrNotFound
	default:
		return nil, 0, fmt.Errorf("loading gs://%s/%s: status %d", g.bucket, g.object, resp.StatusCode)
	}

	generation, err := strconv.ParseInt(resp.Header.Get("x-goog-generation"), 10, 64)
	if err != nil {
		return nil, 0, fmt.Errorf("loading gs://%s/%s: bad generation header: %w", g.bucket, g.object, err)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("loading gs://%s/%s: %w", g.bucket, g.object, err)
	}
	return data, generation, nil
}

func (g *GCS) Save(ctx context.Context, data []byte, ifGeneration int64) (int64, error) {
	query := url.Values{
		"uploadType":        {"media"},
		"name":              {g.object},
		"ifGenerationMatch": {strconv.FormatInt(ifGeneration, 10)},
	}
	u := fmt.Sprintf("%s/upload/storage/v1/b/%s/o?%s", g.endpoint, url.PathEscape(g.bucket), query.Encode())
	resp, err := g.do(ctx, http.MethodPost, u, data)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusPreconditionFailed:
		return 0, ErrPrecondition
	default:
		return 0, fmt.Errorf("saving gs://%s/%s: status %d", g.bucket, g.object, resp.StatusCode)
	}

	var meta struct {
		Generation int64 `json:"generation,string"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return 0, fmt.Errorf("saving gs://%s/%s: bad response: %w", g.bucket, g.object, err)
	}
	return meta.Generation, nil
}

func (g *GCS) do(ctx context.Context, method, u string, body []byte) (*http.Response, error) {
	token, err := g.token(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting storage token: %w", err)
	}
	var reader io.Reader
	if method == http.MethodPost {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if reader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return g.hc.Do(req)
}

// MetadataToken returns a token source backed by the GCE/Cloud Run metadata server for the
// instance's default service account. Tokens are reused until shortly before they expire.
func MetadataToken(hc *http.Client) func(context.Context) (string, error) {
	var mu sync.Mutex
	var token string
	var expires time.Time

	return func(ctx context.Context) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		if token != "" && time.Now().Before(expires) {
			return token, nil
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataTokenURL, nil)
		if err != nil {
			return "", err
		}
		req.Header.Set("Metadata-Flavor", "Google")
		resp, err := hc.Do(req)
		if err != nil {
			return "", fmt.Errorf("metadata token: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("metadata token: status %d", resp.StatusCode)
		}

		var body struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int64  `json:"expires_in"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return "", fmt.Errorf("metadata token: bad response: %w", err)
		}
		if body.AccessToken == "" {
			return "", errors.New("metadata token: empty access_token")
		}
		token = body.AccessToken
		expires = time.Now().Add(time.Duration(body.ExpiresIn)*time.Second - tokenExpiryMargin)
		return token, nil
	}
}
