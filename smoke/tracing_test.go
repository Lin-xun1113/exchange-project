// Package smoke provides end-to-end smoke tests for the exchange platform.
package smoke_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDistributedTracingSmoke verifies that distributed tracing produces spans
// across service boundaries when a request flows through gateway → order-svc → matching-svc.
func TestDistributedTracingSmoke(t *testing.T) {
	// Skip unless OTEL_SMOKE_TEST=1 is set.
	// This test requires docker-compose up and all services healthy.
	if os.Getenv("OTEL_SMOKE_TEST") != "1" {
		t.Skip("OTEL_SMOKE_TEST=1 not set; skipping smoke test")
	}

	gatewayURL := os.Getenv("GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:8080"
	}
	jaegerURL := os.Getenv("JAEGER_URL")
	if jaegerURL == "" {
		jaegerURL = "http://localhost:16686"
	}

	// Step 1: Register a test user and obtain a JWT token.
	registerReq := map[string]string{
		"username": fmt.Sprintf("smoke-%d", time.Now().Unix()),
		"email":    fmt.Sprintf("smoke-%d@test.local", time.Now().Unix()),
		"password": "TestPass123!",
	}
	body, err := json.Marshal(registerReq)
	require.NoError(t, err)

	resp, err := http.Post(gatewayURL+"/api/v1/auth/register", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "registration should succeed")

	// Step 2: Login to get a JWT.
	loginReq := map[string]string{
		"username": registerReq["username"],
		"password": registerReq["password"],
	}
	body, err = json.Marshal(loginReq)
	require.NoError(t, err)

	resp, err = http.Post(gatewayURL+"/api/v1/auth/login", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "login should succeed")

	var loginResp struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&loginResp)
	require.NoError(t, err)
	require.NotEmpty(t, loginResp.Token, "token should not be empty")

	// Step 3: Create an order with the JWT. Use x-request-id so we can search by it.
	requestID := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", gatewayURL+"/api/v1/orders", strings.NewReader(`{"symbol":"BTC/USDT","side":"buy","type":"limit","price":"50000","quantity":"0.1"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", requestID)

	httpResp, err := client.Do(req)
	require.NoError(t, err)
	defer httpResp.Body.Close()
	// We don't assert 200 because the user may not have balance set up;
	// the gRPC call still produces spans regardless of business logic outcome.
	_ = httpResp.StatusCode

	// Step 4: Wait for traces to appear in Jaeger.
	jaegerAPI := jaegerURL + "/api/traces"
	var traces []map[string]interface{}
	pollInterval := 2 * time.Second
	maxWait := 30 * time.Second
	deadline := time.Now().Add(maxWait)

	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", jaegerAPI, nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")
		q := req.URL.Query()
		q.Set("service", "gateway")
		q.Set("tag", "request.id="+requestID)
		req.URL.RawQuery = q.Encode()

		httpResp, err := client.Do(req)
		if err == nil {
			defer httpResp.Body.Close()
			if httpResp.StatusCode == http.StatusOK {
				var result struct {
					Data []map[string]interface{} `json:"data"`
				}
				if err := json.NewDecoder(httpResp.Body).Decode(&result); err == nil {
					traces = result.Data
					if len(traces) > 0 {
						break
					}
				}
			}
		}
		time.Sleep(pollInterval)
	}

	// Step 5: Assert that at least one trace was found.
	assert.NotEmpty(t, traces, "expected at least one trace in Jaeger for request.id=%s within %v", requestID, maxWait)

	// Step 6: Verify at least one span has the request.id tag matching the request ID.
	if len(traces) > 0 {
		trace := traces[0]
		foundRequestIDTag := false
		if spans, ok := trace["spans"].([]interface{}); ok {
			for _, s := range spans {
				span, ok := s.(map[string]interface{})
				if !ok {
					continue
				}
				tags, ok := span["tags"].([]interface{})
				if !ok {
					continue
				}
				for _, tag := range tags {
					t2, ok := tag.(map[string]interface{})
					if !ok {
						continue
					}
					key, _ := t2["key"].(string)
					value, _ := t2["value"].(string)
					if key == "request.id" && value == requestID {
						foundRequestIDTag = true
						break
					}
				}
				if foundRequestIDTag {
					break
				}
			}
		}
		assert.Truef(t, foundRequestIDTag,
			"expected at least one span with tag request.id=%s, but none found in trace %v",
			requestID, trace)
	}

	// Step 7: Verify the trace has spans from multiple services (indicating context propagation).
	if len(traces) > 0 {
		trace := traces[0]
		spans, ok := trace["spans"].([]interface{})
		if ok && len(spans) > 0 {
			services := make(map[string]bool)
			for _, s := range spans {
				if span, ok := s.(map[string]interface{}); ok {
					if procName, ok := span["processName"].(string); ok {
						services[procName] = true
					}
				}
			}
			assert.Contains(t, services, "gateway", "trace should contain a gateway span")
			backendFound := false
			for svc := range services {
				if svc == "order-svc" || svc == "matching-svc" {
					backendFound = true
					break
				}
			}
			assert.Truef(t, backendFound,
				"expected trace to contain at least one backend span (order-svc or matching-svc), got: %v", services)
			t.Logf("services observed in trace: %v", services)
		}
	}
}
