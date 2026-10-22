package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExitCodesBeforeConnecting(t *testing.T) {
	env := func(string) string { return "" }
	cases := []struct {
		name string
		args []string
		code int
		out  string
	}{
		{"help", []string{"--help"}, 0, "-addr"},
		{"unknown flag", []string{"--bogus"}, 1, "--help"},
		{"missing addr and token", []string{"--insecure", "--name", "x", "--lat", "1", "--lon", "1"}, 1, "addr is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, env, &stdout, &stderr); code != tc.code {
				t.Fatalf("exit %d, want %d; stderr: %s", code, tc.code, stderr.String())
			}
			if out := stdout.String() + stderr.String(); !strings.Contains(out, tc.out) {
				t.Fatalf("output lacks %q:\n%s", tc.out, out)
			}
		})
	}
}

func TestBadCAExitsFatal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"--addr", "localhost:1", "--token", "si_secretsecretsecret", "--tls", "--ca", "/nonexistent/ca.pem",
		"--name", "x", "--lat", "1", "--lon", "1"}
	if code := run(args, func(string) string { return "" }, &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1; stderr: %s", code, stderr.String())
	}
	if strings.Contains(stderr.String(), "si_secretsecretsecret") {
		t.Fatal("token logged")
	}
}
