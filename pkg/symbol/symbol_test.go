package symbol

import "testing"

func TestParse(t *testing.T) {
	for _, input := range []string{"btc/usdt", "BTC-USDT"} {
		p, err := Parse(input)
		if err != nil || p != (Pair{Base: "BTC", Quote: "USDT"}) || p.String() != "BTC/USDT" {
			t.Errorf("Parse(%q) = %#v, %v", input, p, err)
		}
	}
}

func TestParseRejectsInvalidPairs(t *testing.T) {
	for _, input := range []string{"BTC", "BTC--USDT", "BTC/USDT/ETH", "BTC/BTC", "BTC/USD T", "BTC/UＳDT", "B/USDT", "ABCDEFGHIJKLMNOP/QRSTUVWXYZ123456"} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) succeeded", input)
		}
	}
}

func TestAllowedCanonicalizesConfiguredPairs(t *testing.T) {
	allowed := Allowed([]string{"btc-usdt", "ETH/USDT"})
	if !allowed(Pair{Base: "BTC", Quote: "USDT"}) || !allowed(Pair{Base: "ETH", Quote: "USDT"}) {
		t.Fatal("valid configured pairs are missing")
	}
	if allowed(Pair{Base: "BTC", Quote: "ETH"}) {
		t.Fatal("accepted an unconfigured pair")
	}
}
