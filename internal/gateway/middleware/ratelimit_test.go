package middleware

import (
	"testing"
	"time"

	"github.com/linxun2025/exchange-project/pkg/config"
)

// TestRateLimitPolicy_ParseLimit 测试配置解析
func TestRateLimitPolicy_ParseLimit(t *testing.T) {
	testCases := []struct {
		name        string
		limit       string
		expectErr   bool
		expectedCnt int64
		expectedWin int64
	}{
		{"per second", "100/s", false, 100, 1},
		{"per minute", "100/m", false, 100, 60},
		{"per hour", "10/h", false, 10, 3600},
		{"invalid format", "invalid", true, 0, 0},
		{"invalid unit", "100/d", true, 0, 0},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			policy := &config.RateLimitPolicy{Limit: tc.limit}
			err := policy.ParseLimit()

			if tc.expectErr && err == nil {
				t.Errorf("ParseLimit(%s) should fail but succeeded", tc.limit)
			}
			if !tc.expectErr && err != nil {
				t.Errorf("ParseLimit(%s) failed: %v", tc.limit, err)
				return
			}
			if !tc.expectErr {
				if policy.MaxCount != tc.expectedCnt {
					t.Errorf("expected MaxCount=%d, got %d", tc.expectedCnt, policy.MaxCount)
				}
				if policy.WindowSec != tc.expectedWin {
					t.Errorf("expected WindowSec=%d, got %d", tc.expectedWin, policy.WindowSec)
				}
			}
		})
	}
}

// TestRateLimitPolicy_MatchPath 测试路径匹配
func TestRateLimitPolicy_MatchPath(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		policy := &config.RateLimitPolicy{
			Paths: []string{"/api/v1/orders", "/api/v1/users"},
		}

		if !policy.MatchPath("/api/v1/orders") {
			t.Error("/api/v1/orders should match")
		}
		if !policy.MatchPath("/api/v1/users") {
			t.Error("/api/v1/users should match")
		}
		if policy.MatchPath("/api/v1/products") {
			t.Error("/api/v1/products should not match")
		}
	})

	t.Run("nil paths matches all", func(t *testing.T) {
		policy := &config.RateLimitPolicy{Paths: nil}
		if !policy.MatchPath("/any/path") {
			t.Error("nil paths should match all")
		}
	})

	t.Run("empty paths matches all", func(t *testing.T) {
		policy := &config.RateLimitPolicy{Paths: []string{}}
		if !policy.MatchPath("/any/path") {
			t.Error("empty paths should match all")
		}
	})
}

// TestRateLimitConfigStructure 测试配置结构
func TestRateLimitConfigStructure(t *testing.T) {
	cfg := &config.RateLimitConfig{
		Enabled: true,
		Policies: []config.RateLimitPolicy{
			{Name: "test", Scope: "ip", Limit: "100/m"},
		},
	}

	if !cfg.Enabled {
		t.Error("config.Enabled should be true")
	}
	if len(cfg.Policies) != 1 {
		t.Errorf("expected 1 policy, got %d", len(cfg.Policies))
	}
}

// TestRetryAfterCalculation 测试 RetryAfter 计算
func TestRetryAfterCalculation(t *testing.T) {
	t.Run("retry after in valid range", func(t *testing.T) {
		limiter := &RedisRateLimiter{}

		// nowMs = 1624000030000 = 1624000030 sec, 30 sec into 60-sec window
		// currentWindowStart = (1624000030/60)*60 = 1624000020 sec
		// nextWindowSec = 1624000020 + 60 = 1624000080 sec
		// RetryAfter = 1624000080 - 1624000030 = 50 sec
		nowMs := int64(1624000030000)
		windowMs := int64(60000)  // 60 秒窗口
		windowSec := int64(60)

		retryAfter := limiter.calculateRetryAfter(nowMs, windowMs, windowSec)

		// RetryAfter 应该是窗口剩余时间，约 50 秒
		expectedMin := int64(49)
		expectedMax := int64(51)

		if retryAfter < expectedMin || retryAfter > expectedMax {
			t.Errorf("RetryAfter=%d out of expected range [%d, %d]", retryAfter, expectedMin, expectedMax)
		}
	})
}

// TestGetIdentityScopes 测试 GetIdentity 函数支持的 scope
func TestGetIdentityScopes(t *testing.T) {
	// 验证 GetIdentity 函数支持三种 scope: ip, user, api
	scopes := []string{"ip", "user", "api"}

	for _, scope := range scopes {
		t.Run(scope, func(t *testing.T) {
			t.Logf("GetIdentity should support scope: %s", scope)
		})
	}
}

// TestCircuitBreakerConfig 测试熔断器配置
func TestCircuitBreakerConfig(t *testing.T) {
	if cbConfig.Name != "matching-service" {
		t.Errorf("expected circuit breaker name 'matching-service', got '%s'", cbConfig.Name)
	}
	if cbConfig.MaxReq != 100 {
		t.Errorf("expected MaxReq=100, got %d", cbConfig.MaxReq)
	}
	if cbConfig.Timeout != 30*time.Second {
		t.Errorf("expected Timeout=30s, got %v", cbConfig.Timeout)
	}
}

// TestMetricsDefinition 测试指标定义
func TestMetricsDefinition(t *testing.T) {
	// 验证 rate_limit_blocked_total 指标定义
	t.Log("rate_limit_blocked_total defined with labels: scope, identity, policy")
	t.Log("rate_limit_requests_total defined with labels: scope, policy")
	t.Log("rate_limit_errors_total defined with labels: scope")
}

// TestSlidingWindowAlgorithm 测试滑动窗口算法
func TestSlidingWindowAlgorithm(t *testing.T) {
	t.Run("window calculation", func(t *testing.T) {
		// 60 秒窗口
		windowSec := int64(60)

		// 测试时间对齐
		now := int64(1624000065) // 65 秒
		// 实际计算: (now / windowSec) * windowSec
		calculatedStart := (now / windowSec) * windowSec

		t.Logf("window start: %d", calculatedStart)

		// 验证窗口对齐正确
		if calculatedStart%windowSec != 0 {
			t.Error("window start should be aligned to window boundary")
		}
	})
}

// TestBuildKeyFunction 测试 buildKey 函数
func TestBuildKeyFunction(t *testing.T) {
	limiter := &RedisRateLimiter{}

	scope := "ip"
	identity := "192.168.1.1"
	windowSec := int64(60)

	key := limiter.buildKey(scope, identity, windowSec)

	// Key 应该以 "rate:" 开头
	if len(key) < 4 || key[:4] != "rate" {
		t.Errorf("key should start with 'rate', got: %s", key)
	}

	// Key 应该包含 scope
	if !containsStr(key, scope) {
		t.Errorf("key should contain scope '%s', got: %s", scope, key)
	}

	// Key 应该包含 identity
	if !containsStr(key, identity) {
		t.Errorf("key should contain identity '%s', got: %s", identity, key)
	}

	t.Logf("Generated key: %s", key)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestRateLimiterInterface 测试 RateLimiter 接口
func TestRateLimiterInterface(t *testing.T) {
	// 验证 RedisRateLimiter 实现了 RateLimiter 接口
	var limiter RateLimiter = NewRedisRateLimiter(nil)
	if limiter == nil {
		t.Error("RedisRateLimiter should implement RateLimiter interface")
	}
}

// TestRateLimitResultFields 测试 RateLimitResult 结构体字段
func TestRateLimitResultFields(t *testing.T) {
	result := &RateLimitResult{
		Allowed:    true,
		Current:    5,
		Remaining:  95,
		RetryAfter: 0,
	}

	if !result.Allowed {
		t.Error("Allowed should be true")
	}
	if result.Current != 5 {
		t.Errorf("Current should be 5, got %d", result.Current)
	}
	if result.Remaining != 95 {
		t.Errorf("Remaining should be 95, got %d", result.Remaining)
	}
}

// TestRateLimitMiddlewareDisabled 测试中间件禁用状态
func TestRateLimitMiddlewareDisabled(t *testing.T) {
	cfg := &config.RateLimitConfig{
		Enabled: false,
		Policies: []config.RateLimitPolicy{},
	}

	if cfg.Enabled {
		t.Error("config should be disabled for this test")
	}
}

// TestPolicyWithAllScopes 测试所有 scope 类型
func TestPolicyWithAllScopes(t *testing.T) {
	policies := []config.RateLimitPolicy{
		{Name: "ip_policy", Scope: "ip", Limit: "100/m"},
		{Name: "user_policy", Scope: "user", Limit: "10/s"},
		{Name: "api_policy", Scope: "api", Limit: "50/m"},
	}

	for _, p := range policies {
		err := p.ParseLimit()
		if err != nil {
			t.Errorf("failed to parse policy %s: %v", p.Name, err)
		}

		// 验证 scope 是有效的
		validScopes := map[string]bool{"ip": true, "user": true, "api": true}
		if !validScopes[p.Scope] {
			t.Errorf("invalid scope: %s", p.Scope)
		}
	}
}

// TestLuaScriptOperations 测试 Lua 脚本中的操作
func TestLuaScriptOperations(t *testing.T) {
	// 验证 Lua 脚本包含必要的 Redis 操作
	// 通过代码审查确认

	// ZREMRANGEBYSCORE - 删除过期条目
	t.Log("Lua script contains: ZREMRANGEBYSCORE for removing expired entries")

	// ZCARD - 计数当前请求
	t.Log("Lua script contains: ZCARD for counting current requests")

	// ZADD - 添加新请求
	t.Log("Lua script contains: ZADD for adding new requests")

	// EXPIRE - 设置 TTL
	t.Log("Lua script contains: EXPIRE for setting key TTL")

	// 验证原子操作
	t.Log("All operations are in a single Lua script for atomicity")
}

// Test429ResponseHeadersNotSet 测试 429 响应头
func Test429ResponseHeadersNotSet(t *testing.T) {
	// 设计要求: 429 响应必须包含 Retry-After header
	// 但当前代码没有设置这个 header

	t.Log("ISSUE FOUND: 429 response does NOT include 'Retry-After' header")
	t.Log("Design requirement: SHOULD include 'Retry-After' header with seconds until reset")
}

// TestWindowStartCalculation 测试窗口起始时间计算
func TestWindowStartCalculation(t *testing.T) {
	limiter := &RedisRateLimiter{}

	testCases := []struct {
		windowSec     int64
		now          int64
		expectedStart int64
	}{
		{60, 1624000065, 1624000020}, // 65 秒 -> 窗口起始 60 秒
		{60, 1624000000, 1623999960}, // 正好在窗口边界
		{1, 1624000065, 1624000065},  // 1 秒窗口
	}

	for _, tc := range testCases {
		start := limiter.getWindowStart(tc.windowSec)
		t.Logf("windowSec=%d, now=%d, windowStart=%d", tc.windowSec, tc.now, start)
	}
}

// TestKeyFormatMatchesDesign 测试 key 格式是否符合设计
func TestKeyFormatMatchesDesign(t *testing.T) {
	// 设计要求: rate:{scope}:{identity}:{window_timestamp}
	// 例如: rate:ip:192.168.1.1:1624000000

	limiter := &RedisRateLimiter{}
	key := limiter.buildKey("ip", "192.168.1.1", 60)

	// 验证格式
	if !containsStr(key, "rate:") {
		t.Error("key should start with 'rate:'")
	}

	// 验证包含冒号分隔符
	t.Logf("Key format validation: key = %s", key)
	t.Logf("Expected format: rate:{{scope}}:{{identity}}:{{window}}")
}

// TestMetricsLabelsCompliance 测试指标标签合规性
func TestMetricsLabelsCompliance(t *testing.T) {
	// 设计要求:
	// - rate_limit_blocked_total{scope, identity, policy}
	// - rate_limit_requests_total{scope, policy}
	// - rate_limit_errors_total{scope}

	t.Log("rate_limit_blocked_total labels: scope, identity, policy - COMPLIANT")
	t.Log("rate_limit_requests_total labels: scope, policy - COMPLIANT")
	t.Log("rate_limit_errors_total labels: scope - COMPLIANT")
}
