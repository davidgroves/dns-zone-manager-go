package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProbeHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := probeHealth(context.Background(), ok.URL); err != nil {
		t.Fatal(err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if err := probeHealth(context.Background(), bad.URL); err == nil {
		t.Fatal("expected error for non-200")
	}
}
