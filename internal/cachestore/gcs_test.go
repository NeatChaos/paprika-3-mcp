package cachestore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func staticToken(context.Context) (string, error) { return "tok", nil }

func TestGCSLoad(t *testing.T) {
	var gotPath, gotQuery, gotAuth, gotMethod string
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery, gotAuth = r.Method, r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("Authorization")
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("x-goog-generation", "7")
		_, _ = w.Write([]byte(`{"version":1}`))
	}))
	defer ts.Close()
	store := NewGCS("bkt", "recipe-cache.json", ts.Client(), ts.URL, staticToken)

	data, gen, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, `{"version":1}`, string(data))
	assert.Equal(t, int64(7), gen)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/storage/v1/b/bkt/o/recipe-cache.json", gotPath)
	assert.Equal(t, "alt=media", gotQuery)
	assert.Equal(t, "Bearer tok", gotAuth)

	status = http.StatusNotFound
	_, _, err = store.Load(context.Background())
	assert.ErrorIs(t, err, ErrNotFound)

	status = http.StatusForbidden
	_, _, err = store.Load(context.Background())
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestGCSSave(t *testing.T) {
	var gotPath, gotAuth, gotMethod, gotBody, gotType string
	var gotQuery map[string][]string
	status := http.StatusOK
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotMethod, gotPath, gotQuery, gotAuth, gotBody, gotType = r.Method, r.URL.Path, r.URL.Query(), r.Header.Get("Authorization"), string(body), r.Header.Get("Content-Type")
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write([]byte(`{"name":"recipe-cache.json","generation":"8"}`))
	}))
	defer ts.Close()
	store := NewGCS("bkt", "recipe-cache.json", ts.Client(), ts.URL, staticToken)

	gen, err := store.Save(context.Background(), []byte(`{"version":1}`), 7)
	require.NoError(t, err)
	assert.Equal(t, int64(8), gen)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/upload/storage/v1/b/bkt/o", gotPath)
	assert.Equal(t, map[string][]string{"uploadType": {"media"}, "name": {"recipe-cache.json"}, "ifGenerationMatch": {"7"}}, gotQuery)
	assert.Equal(t, "Bearer tok", gotAuth)
	assert.Equal(t, "application/json", gotType)
	assert.Equal(t, `{"version":1}`, gotBody)

	_, err = store.Save(context.Background(), []byte(`{}`), 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"0"}, gotQuery["ifGenerationMatch"])

	status = http.StatusPreconditionFailed
	_, err = store.Save(context.Background(), []byte(`{}`), 7)
	assert.ErrorIs(t, err, ErrPrecondition)

	status = http.StatusInternalServerError
	_, err = store.Save(context.Background(), []byte(`{}`), 7)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, ErrPrecondition)
}

func TestGCSTokenError(t *testing.T) {
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer ts.Close()
	store := NewGCS("bkt", "o", ts.Client(), ts.URL, func(context.Context) (string, error) { return "", assert.AnError })

	_, _, err := store.Load(context.Background())
	assert.ErrorIs(t, err, assert.AnError)
	_, err = store.Save(context.Background(), nil, 0)
	assert.ErrorIs(t, err, assert.AnError)
	assert.Zero(t, hits.Load())
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func metadataClient(expiresIn string, hits *atomic.Int32, gotURL, gotFlavor *string) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		hits.Add(1)
		*gotURL, *gotFlavor = r.URL.String(), r.Header.Get("Metadata-Flavor")
		body := `{"access_token":"abc","expires_in":` + expiresIn + `,"token_type":"Bearer"}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}
}

func TestMetadataTokenCaches(t *testing.T) {
	var hits atomic.Int32
	var gotURL, gotFlavor string
	token := MetadataToken(metadataClient("3599", &hits, &gotURL, &gotFlavor))

	for range 2 {
		tok, err := token(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "abc", tok)
	}
	assert.Equal(t, int32(1), hits.Load())
	assert.Equal(t, "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", gotURL)
	assert.Equal(t, "Google", gotFlavor)
}

func TestMetadataTokenRefreshesNearExpiry(t *testing.T) {
	var hits atomic.Int32
	var gotURL, gotFlavor string
	token := MetadataToken(metadataClient("30", &hits, &gotURL, &gotFlavor))

	for range 2 {
		_, err := token(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, int32(2), hits.Load())
}

func TestMetadataTokenErrors(t *testing.T) {
	for name, resp := range map[string]*http.Response{
		"status":      {StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}},
		"malformed":   {StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{nope")), Header: http.Header{}},
		"empty token": {StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"expires_in":3599}`)), Header: http.Header{}},
	} {
		t.Run(name, func(t *testing.T) {
			hc := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return resp, nil })}
			_, err := MetadataToken(hc)(context.Background())
			assert.Error(t, err)
		})
	}
}
