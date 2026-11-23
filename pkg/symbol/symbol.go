// Package symbol defines canonical trading pairs.
package symbol

import (
	"fmt"
	"strings"
)

// Pair identifies the base and quote assets of a market.
type Pair struct {
	Base  string
	Quote string
}

// Parse accepts BASE/QUOTE and BASE-QUOTE, and canonicalizes ASCII asset names.
func Parse(s string) (Pair, error) {
	// The database symbol column holds at most 32 characters.
	sep := strings.IndexAny(s, "/-")
	if len(s) > 32 || sep < 0 || !validAsset(s[:sep]) || !validAsset(s[sep+1:]) {
		return Pair{}, fmt.Errorf("invalid symbol %q", s)
	}
	p := Pair{Base: strings.ToUpper(s[:sep]), Quote: strings.ToUpper(s[sep+1:])}
	if p.Base == p.Quote {
		return Pair{}, fmt.Errorf("symbol %q has the same base and quote asset", s)
	}
	return p, nil
}

func validAsset(s string) bool {
	if len(s) < 2 || len(s) > 16 {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// String returns the canonical BASE/QUOTE representation.
func (p Pair) String() string { return p.Base + "/" + p.Quote }

// Allowed returns a membership function for the valid pairs in list.
func Allowed(list []string) func(Pair) bool {
	pairs := make(map[Pair]struct{}, len(list))
	for _, s := range list {
		if p, err := Parse(s); err == nil {
			pairs[p] = struct{}{}
		}
	}
	return func(p Pair) bool {
		_, ok := pairs[p]
		return ok
	}
}
