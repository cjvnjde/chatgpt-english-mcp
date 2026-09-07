package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunHTTPServersDrainsActiveRequests(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	finishRequest := func() { releaseOnce.Do(func() { close(release) }) }
	defer finishRequest()
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- runHTTPServers(ctx, []httpEndpoint{{
			name:    "test",
			address: address,
			path:    "/",
			handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				close(started)
				<-release
				fmt.Fprint(response, "request completed")
			}),
		}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	// Wait for the actual listener, including versions that log before binding.
	readyContext, stopWaiting := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopWaiting()
	var connection net.Conn
	for {
		connection, err = (&net.Dialer{}).DialContext(readyContext, "tcp", address)
		if err == nil {
			break
		}
		select {
		case err := <-serverDone:
			t.Fatalf("server exited before accepting a request: %v", err)
		case <-readyContext.Done():
			t.Fatalf("listener did not become ready: %v", err)
		case <-time.After(time.Millisecond):
		}
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(connection, "GET / HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", address); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-readyContext.Done():
		t.Fatal("request handler did not start")
	}

	cancel()
	select {
	case err := <-serverDone:
		t.Fatalf("server returned while a request was still active: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	finishRequest()
	response, err := io.ReadAll(connection)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(response), "request completed") {
		t.Fatalf("active request did not complete: %q", response)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("server shutdown failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not return after the active request completed")
	}
}
