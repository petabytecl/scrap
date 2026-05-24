package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/petabytecl/scrap/internal/testutil"
)

func TestMetricsEndpointServesMetricsPath(t *testing.T) {
	endpoint, err := listenMetricsEndpoint("127.0.0.1:0")
	testutil.RequireNoErrorf(t, err, "listen metrics endpoint")
	defer func() { testutil.RequireNoErrorf(t, endpoint.Close(), "close metrics endpoint") }()

	errCh := endpoint.Serve()
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", endpoint.Address())
	testutil.RequireNoErrorf(t, err, "dial metrics endpoint")
	defer func() { testutil.RequireNoErrorf(t, conn.Close(), "close metrics connection") }()
	_, err = fmt.Fprintf(conn, "%s /metrics HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n\r\n", http.MethodGet, endpoint.Address())
	testutil.RequireNoErrorf(t, err, "write metrics request")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	testutil.RequireNoErrorf(t, err, "read metrics response")
	defer func() { testutil.RequireNoErrorf(t, resp.Body.Close(), "close response body") }()
	testutil.RequireEqualf(t, resp.StatusCode, http.StatusOK, "metrics status")
	body, err := io.ReadAll(resp.Body)
	testutil.RequireNoErrorf(t, err, "read metrics body")
	if !strings.Contains(string(body), "scrap_write_latency_seconds") {
		t.Fatalf("metrics body missing write latency metric:\n%s", string(body))
	}

	testutil.RequireNoErrorf(t, endpoint.Close(), "close metrics endpoint")
	testutil.RequireNoErrorf(t, <-errCh, "metrics endpoint stopped")
}
