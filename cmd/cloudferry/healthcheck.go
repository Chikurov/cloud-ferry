package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

// healthcheckPath is the liveness endpoint the probe calls. It stays 200 while
// the web server is up, regardless of rclone state.
const healthcheckPath = "/api/healthz"

// runHealthcheck probes the local HTTP server and returns a process exit code
// for Docker's HEALTHCHECK. Doing it in the binary keeps the image free of
// curl/wget and works when the API is behind basic auth (CF_BASIC_*).
func runHealthcheck(target, user, pass string) int {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	if user != "" {
		req.SetBasicAuth(user, pass)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: %s returned %d\n", target, resp.StatusCode)
		return 1
	}
	return 0
}

// healthcheckTarget builds the probe URL from LISTEN_ADDR. The server may bind
// 0.0.0.0 or [::], so the probe always dials loopback on the same port.
func healthcheckTarget(listen string) string {
	_, port, err := net.SplitHostPort(listen)
	if err != nil || port == "" {
		port = "3000"
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + healthcheckPath
}
