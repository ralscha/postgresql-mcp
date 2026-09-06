package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBearerAuthentication(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		called = true
		response.WriteHeader(http.StatusNoContent)
	})
	handler := bearerAuthentication("secret", next)

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if unauthorized.Code != http.StatusUnauthorized || called {
		t.Fatalf("unauthorized response = %d, called = %t", unauthorized.Code, called)
	}
	if unauthorized.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatal("unauthorized response should advertise bearer authentication")
	}

	authorized := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(authorized, request)
	if authorized.Code != http.StatusNoContent || !called {
		t.Fatalf("authorized response = %d, called = %t", authorized.Code, called)
	}
}

func TestBearerAuthenticationCanBeDisabled(t *testing.T) {
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	response := httptest.NewRecorder()
	bearerAuthentication("", next).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("response = %d", response.Code)
	}
}

func TestServeHTTPShutsDownWhenContextIsCanceled(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.NewServeMux(), ReadHeaderTimeout: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveHTTP(ctx, server, listener)
	}()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP() = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP did not shut down after context cancellation")
	}
}
