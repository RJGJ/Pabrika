package main

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const healthcheckTimeout = 3 * time.Second

// runHealthcheck is `pabrika healthcheck`: GET http://127.0.0.1:$PORT/healthz with a timeout.
// It reads only PORT (default 8080) so an unrelated bad variable such as BASE_URL cannot make
// the container health check fail, and it prints nothing on success. It needs no shell or curl,
// so it works in the distroless image.
func runHealthcheck(getenv func(string) string, stderr io.Writer, timeout time.Duration) int {
	port := 8080
	if v := getenv("PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			fmt.Fprintf(stderr, "healthcheck: PORT %q is not a port number\n", v)
			return 1
		}
		port = p
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + "/healthz")
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "healthcheck: unhealthy (HTTP %d)\n", resp.StatusCode)
		return 1
	}
	return 0
}
