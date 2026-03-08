package fetcher

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/blacksd/crd-schema-extractor/internal/source"
)

func TestURLFetcherSuccess(t *testing.T) {
	body := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: foos.example.com
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	f := &URLFetcher{Client: srv.Client()}
	result, err := f.Fetch(nopLog, source.Source{
		Name: "test",
		Type: "url",
		URL:  srv.URL + "/crds.yaml",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if result.Dir != "" {
		t.Error("expected Dir to be empty for URL fetch")
	}
	if string(result.Data) != body {
		t.Errorf("Data = %q, want %q", string(result.Data), body)
	}
}

func TestURLFetcherRetryOn500(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := &URLFetcher{Client: srv.Client()}
	result, err := f.Fetch(nopLog, source.Source{
		Name: "test-retry",
		Type: "url",
		URL:  srv.URL + "/crds.yaml",
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if string(result.Data) != "ok" {
		t.Errorf("Data = %q, want %q", string(result.Data), "ok")
	}
	if attempts.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts.Load())
	}
}

func TestURLFetcherRetryExhaustion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := &URLFetcher{Client: srv.Client()}
	_, err := f.Fetch(nopLog, source.Source{
		Name: "test-exhaust",
		Type: "url",
		URL:  srv.URL + "/crds.yaml",
	})
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
}

func TestURLFetcherConnectionError(t *testing.T) {
	// Use a URL that will fail to connect
	f := &URLFetcher{Client: &http.Client{}}
	_, err := f.Fetch(nopLog, source.Source{
		Name: "test-conn",
		Type: "url",
		URL:  "http://127.0.0.1:1/crds.yaml",
	})
	if err == nil {
		t.Fatal("expected error on connection failure")
	}
}
