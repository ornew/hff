package core

import (
	"math/big"
	"strings"
	"time"
)

// ParseDuration sums decimal components exactly and returns null unless the
// complete signed result fits an integral int64 nanosecond value.
func ParseDuration(text string) Value {
	negative := false
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		negative = text[0] == '-'
		text = text[1:]
	}
	if text == "0" {
		return Duration(0)
	}
	if text == "" {
		return Null()
	}
	total := new(big.Rat)
	for len(text) > 0 {
		i, digits := 0, 0
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
			digits++
		}
		if i < len(text) && text[i] == '.' {
			i++
			for i < len(text) && text[i] >= '0' && text[i] <= '9' {
				i++
				digits++
			}
		}
		if digits == 0 {
			return Null()
		}
		amount, ok := new(big.Rat).SetString(text[:i])
		if !ok {
			return Null()
		}
		text = text[i:]
		unit := int64(0)
		for _, u := range []struct {
			name string
			ns   int64
		}{
			{"ms", 1000000}, {"us", 1000}, {"ns", 1}, {"h", 3600000000000}, {"m", 60000000000}, {"s", 1000000000},
		} {
			if strings.HasPrefix(text, u.name) {
				unit = u.ns
				text = text[len(u.name):]
				break
			}
		}
		if unit == 0 {
			return Null()
		}
		amount.Mul(amount, new(big.Rat).SetInt64(unit))
		total.Add(total, amount)
	}
	if negative {
		total.Neg(total)
	}
	if !total.IsInt() || !total.Num().IsInt64() {
		return Null()
	}
	return Duration(time.Duration(total.Num().Int64()))
}
