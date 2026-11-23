package config

import (
	"slices"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	for _, key := range []string{"DB_PORT", "METRICS_ADDR", "SYMBOLS", "RATE_LIMIT_ENABLED"} {
		t.Setenv(key, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.Port != 3306 || cfg.Metrics.Addr != ":9100" || !cfg.RateLimit.Enabled {
		t.Fatalf("unexpected defaults: DB_PORT=%d METRICS_ADDR=%q RATE_LIMIT_ENABLED=%t", cfg.Database.Port, cfg.Metrics.Addr, cfg.RateLimit.Enabled)
	}
	if !slices.Equal(cfg.Symbols, []string{"BTC/USDT", "ETH/USDT"}) {
		t.Fatalf("unexpected default symbols %v", cfg.Symbols)
	}
	for name, max := range map[string]int64{"ip_global": 100, "order_write": 10} {
		p, ok := cfg.RateLimit.Policy(name)
		if !ok || p.MaxCount != max || p.WindowSec != 1 {
			t.Errorf("policy %s: %#v, found=%t", name, p, ok)
		}
	}
}

func TestLoadCanonicalizesSymbols(t *testing.T) {
	t.Setenv("SYMBOLS", " btc-usdt, ETH/USDT ")
	t.Setenv("RATE_LIMIT_ENABLED", "false")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Symbols, []string{"BTC/USDT", "ETH/USDT"}) || cfg.RateLimit.Enabled {
		t.Fatalf("symbols=%v rate limit enabled=%t", cfg.Symbols, cfg.RateLimit.Enabled)
	}
}

func TestLoadRejectsDuplicateMarkets(t *testing.T) {
	t.Setenv("SYMBOLS", "BTC/USDT,btc-usdt")
	if _, err := Load(); err == nil {
		t.Fatal("expected configuration error for duplicate canonical market")
	}
}
