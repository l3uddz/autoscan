package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestShutdownFinishesOpenRequests(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)

	requestErr := make(chan error, 1)
	go func() {
		res, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			res.Body.Close()
			if res.StatusCode != http.StatusOK {
				err = fmt.Errorf("status %d", res.StatusCode)
			}
		}
		requestErr <- err
	}()
	<-started

	processorDone := make(chan struct{})
	close(processorDone)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan struct{})
	go func() {
		shutdown(ctx, []*http.Server{srv}, processorDone)
		close(shutdownDone)
	}()

	select {
	case <-shutdownDone:
		t.Fatal("shutdown returned before the open request finished")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	if err := <-requestErr; err != nil {
		t.Errorf("Open request failed: %v", err)
	}
	<-shutdownDone
}

func TestShutdownStopsWaitingForProcessorAtDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		shutdown(ctx, nil, make(chan struct{}))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown waited past its deadline for the processor")
	}
}
