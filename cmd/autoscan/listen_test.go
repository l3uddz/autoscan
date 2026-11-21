package main

import (
	"net"
	"net/http"
	"testing"
)

func TestListenAddr(t *testing.T) {
	var testCases = map[string]string{
		"":                 ":3030",
		"127.0.0.1":        "127.0.0.1:3030",
		"192.168.0.1:5959": "192.168.0.1:5959",
		"localhost":        "localhost:3030",
		"::1":              "[::1]:3030",
		"[::1]":            "[::1]:3030",
		"[::1]:5959":       "[::1]:5959",
		"::":               "[::]:3030",
	}

	for host, want := range testCases {
		if got := listenAddr(host, 3030); got != want {
			t.Errorf("listenAddr(%q, 3030) = %q, want %q", host, got, want)
		}
	}
}

func TestStartServersReturnsListenError(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()

	free := "127.0.0.1:0"
	servers, err := startServers([]string{free, taken.Addr().String()}, 0, http.NotFoundHandler(), func() {})
	if err == nil {
		for _, srv := range servers {
			srv.Close()
		}
		t.Fatal("startServers returned nil error for a port in use")
	}
	if servers != nil {
		t.Errorf("startServers returned servers %v with the error", servers)
	}
}

func TestStartServersServesEachHost(t *testing.T) {
	servers, err := startServers([]string{"127.0.0.1:0", "127.0.0.1:0"}, 0, http.HandlerFunc(healthHandler), func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, srv := range servers {
			srv.Close()
		}
	}()

	for _, srv := range servers {
		res, err := http.Get("http://" + srv.Addr + "/health")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", srv.Addr, res.StatusCode)
		}
	}
}
