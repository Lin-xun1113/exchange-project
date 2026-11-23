// Package money validates exchange amounts and converts matching quantities to ticks.
package money

import (
	"errors"
	"math"
	"math/big"

	"github.com/shopspring/decimal"
)

const MaxScale = 8

var ErrInvalidAmount = errors.New("invalid amount")

// ParsePositive parses a positive amount with at most MaxScale fractional digits
// that fits the integer part of MySQL DECIMAL(36,18).
func ParsePositive(s string) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(s)
	if err != nil || d.Sign() <= 0 || d.Exponent() < -MaxScale ||
		int64(d.NumDigits())+int64(d.Exponent()) > 18 {
		return decimal.Zero, ErrInvalidAmount
	}
	return d, nil
}

// ToTicks converts a decimal to 1e-8 ticks without rounding or overflowing.
func ToTicks(d decimal.Decimal) (int64, error) {
	if d.IsZero() {
		return 0, nil
	}
	shift := int64(d.Exponent()) + MaxScale
	if shift >= int64(len(powersOfTen)) {
		return 0, ErrInvalidAmount
	}

	n := d.Coefficient()
	if shift < 0 {
		// A nonzero coefficient cannot be divisible by 10^k once k
		// reaches its bit length. Bound the exponent before computing it.
		if -shift >= int64(n.BitLen()) {
			return 0, ErrInvalidAmount
		}
		var divisor, remainder big.Int
		divisor.Exp(big.NewInt(10), big.NewInt(-shift), nil)
		n.QuoRem(n, &divisor, &remainder)
		if remainder.Sign() != 0 {
			return 0, ErrInvalidAmount
		}
		shift = 0
	}
	if !n.IsInt64() {
		return 0, ErrInvalidAmount
	}
	ticks, scale := n.Int64(), powersOfTen[shift]
	if ticks > math.MaxInt64/scale || ticks < math.MinInt64/scale {
		return 0, ErrInvalidAmount
	}
	return ticks * scale, nil
}

var powersOfTen = [...]int64{
	1, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9,
	1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18,
}

// FromTicks converts 1e-8 ticks to an exact decimal.
func FromTicks(t int64) decimal.Decimal { return decimal.New(t, -MaxScale) }
