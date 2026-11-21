package main

import "testing"

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
