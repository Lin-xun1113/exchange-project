// Package smoke provides end-to-end smoke tests for the exchange platform.
package smoke_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDistributedTracingSmoke verifies gateway and order-svc spans share a trace.
func TestDistributedTracingSmoke(t *testing.T) {
	if os.Getenv("OTEL_SMOKE_TEST") != "1" {
		t.Skip("OTEL_SMOKE_TEST=1 not set; skipping smoke test")
	}
	gatewayURL := strings.TrimRight(os.Getenv("GATEWAY_URL"), "/")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:8080"
	}
	jaegerURL := strings.TrimRight(os.Getenv("JAEGER_URL"), "/")
	if jaegerURL == "" {
		jaegerURL = "http://localhost:16686"
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	id := fmt.Sprintf("smoke-%d", time.Now().UnixNano())
	credentials := map[string]string{"username": id, "password": "TestPass123!"}
	post(t, httpClient, gatewayURL+"/api/v1/auth/register", map[string]string{
		"username": id, "password": credentials["password"], "email": id + "@test.local",
	}, nil)

	var login struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(post(t, httpClient, gatewayURL+"/api/v1/auth/login", credentials, nil), &login))
	require.NotEmpty(t, login.Token)

	post(t, httpClient, gatewayURL+"/api/v1/account/deposit", map[string]string{"asset": "USDT", "amount": "10000"}, map[string]string{
		"Authorization": "Bearer " + login.Token, "Idempotency-Key": id + "-deposit",
	})
	var created struct {
		Order struct {
			ID   string `json:"order_id"`
			Type string `json:"type"`
		} `json:"order"`
	}
	orderData := post(t, httpClient, gatewayURL+"/api/v1/orders", map[string]string{
		"symbol": "BTC/USDT", "side": "buy", "type": "ioc", "price": "0.00000001", "quantity": "1",
	}, map[string]string{
		"Authorization": "Bearer " + login.Token, "Idempotency-Key": id + "-order", "X-Request-ID": id,
	})
	require.NoError(t, json.Unmarshal(orderData, &created))
	require.NotEmpty(t, created.Order.ID)
	require.Equal(t, "ioc", created.Order.Type)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		found, err := orderTrace(ctx, httpClient, jaegerURL, id)
		if err == nil && found {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Jaeger did not report gateway and order-svc spans for request.id=%s: last query error: %v", id, err)
		case <-ticker.C:
		}
	}
}

func post(t *testing.T, client *http.Client, url string, body, headers map[string]string) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, bytes.NewReader(payload))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%s: %s", url, data)
	var result struct {
		Code *int            `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	require.NotNil(t, result.Code, "%s: missing or null code in %s", url, data)
	require.Zero(t, *result.Code, "%s: %s", url, data)
	return result.Data
}

func orderTrace(ctx context.Context, client *http.Client, jaegerURL, requestID string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jaegerURL+"/api/traces", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	query := req.URL.Query()
	query.Set("service", "gateway")
	query.Set("tag", "request.id:"+requestID)
	req.URL.RawQuery = query.Encode()
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("Jaeger returned %s", resp.Status)
	}
	var result struct {
		Data []struct {
			Processes map[string]struct {
				ServiceName string `json:"serviceName"`
			} `json:"processes"`
			Spans []struct {
				ProcessID string `json:"processID"`
				Tags      []struct {
					Key   string `json:"key"`
					Value any    `json:"value"`
				} `json:"tags"`
			} `json:"spans"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}
	for _, trace := range result.Data {
		var gateway, order bool
		for _, span := range trace.Spans {
			switch trace.Processes[span.ProcessID].ServiceName {
			case "gateway":
				for _, tag := range span.Tags {
					if tag.Key == "request.id" && tag.Value == requestID {
						gateway = true
						break
					}
				}
			case "order-svc":
				order = true
			}
		}
		if gateway && order {
			return true, nil
		}
	}
	return false, nil
}
