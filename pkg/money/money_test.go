package money

import (
	"errors"
	"math"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParsePositive(t *testing.T) {
	for _, text := range []string{"", "0", "-1", "not a number", "NaN", "1e-9", "0.123456789"} {
		t.Run(text, func(t *testing.T) {
			if _, err := ParsePositive(text); !errors.Is(err, ErrInvalidAmount) {
				t.Fatalf("ParsePositive(%q) error = %v, want ErrInvalidAmount", text, err)
			}
		})
	}

	if got, err := ParsePositive("0.12345678"); err != nil || !got.Equal(decimal.New(12345678, -8)) {
		t.Fatalf("ParsePositive with eight fractional digits = %s, %v", got, err)
	}
}

func TestToTicks(t *testing.T) {
	for _, tt := range []struct {
		text string
		want int64
	}{
		{"20000.5", 2000050000000},
		{"0.100000000", 10000000},
		{"92233720368.54775807", math.MaxInt64},
		{"-92233720368.54775808", math.MinInt64},
	} {
		t.Run(tt.text, func(t *testing.T) {
			d := decimal.RequireFromString(tt.text)
			got, err := ToTicks(d)
			if err != nil || got != tt.want {
				t.Fatalf("ToTicks(%s) = %d, %v, want %d", d, got, err, tt.want)
			}
			if back := FromTicks(got); !back.Equal(d) {
				t.Fatalf("FromTicks(%d) = %s, want %s", got, back, d)
			}
		})
	}
}

func TestToTicksRejectsUnrepresentableValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		d    decimal.Decimal
	}{
		{"fraction of a tick", decimal.New(1, -9)},
		{"positive overflow", decimal.RequireFromString("92233720368.54775808")},
		{"negative overflow", decimal.RequireFromString("-92233720368.54775809")},
		{"extremely large exponent", decimal.New(1, math.MaxInt32)},
		{"extremely small exponent", decimal.New(1, math.MinInt32)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ToTicks(tt.d); !errors.Is(err, ErrInvalidAmount) {
				t.Fatalf("ToTicks error = %v, want ErrInvalidAmount", err)
			}
		})
	}
}
