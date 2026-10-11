package local

import (
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/ornew/hff/internal/core"
)

func temporalMethod(id string) bool {
	return id == "string.timestamp" || id == "string.duration" || strings.HasPrefix(id, "timestamp.") || strings.HasPrefix(id, "duration.")
}

func (e *evaluation) temporal(id string, receiver core.Value, args []core.Value) (result, error) {
	if id == "string.timestamp" {
		text, _ := receiver.AsString()
		return ordinary(parseTimestamp(text)), nil
	}
	if id == "string.duration" {
		text, _ := receiver.AsString()
		// Exact decimal summation uses arbitrary precision. Reserve a conservative
		// quadratic work budget before allocating large integer intermediates.
		n := len(text)
		if n > 0 && n > math.MaxInt/n {
			return result{}, &core.Error{Code: core.LimitError, Message: "duration parsing work count overflow"}
		}
		if err := e.charge(n * n); err != nil {
			return result{}, err
		}
		return ordinary(parseDuration(text)), nil
	}
	if strings.HasPrefix(id, "duration.") {
		d, _ := receiver.AsDuration()
		if id == "duration.nanoseconds" {
			return ordinary(core.Int(int64(d))), nil
		}
		unit := map[string]time.Duration{"duration.hours": time.Hour, "duration.minutes": time.Minute,
			"duration.seconds": time.Second, "duration.milliseconds": time.Millisecond, "duration.microseconds": time.Microsecond}[id]
		return ordinary(core.Float(float64(d) / float64(unit))), nil
	}
	t, _ := receiver.AsTimestamp()
	if id == "timestamp.string" && len(args) == 0 {
		// The compiler requires the timezone argument; keep the private kernel
		// from silently introducing an optional-argument variant.
		return null(), nil
	}
	switch id {
	case "timestamp.before", "timestamp.after":
		other, _ := args[0].AsTimestamp()
		if id == "timestamp.before" {
			return ordinary(core.Bool(t.Before(other))), nil
		}
		return ordinary(core.Bool(t.After(other))), nil
	case "timestamp.add":
		d, _ := args[0].AsDuration()
		value := t.Add(d)
		if value.Year() < 0 || value.Year() > 9999 {
			return null(), nil
		}
		return ordinary(core.Timestamp(value)), nil
	}
	zone := time.UTC
	if len(args) > 0 {
		name, _ := args[0].AsString()
		if err := e.charge(len(name)); err != nil {
			return result{}, err
		}
		var ok bool
		zone, ok = e.timeZones.resolve(name)
		if !ok {
			return null(), nil
		}
	}
	t = t.In(zone)
	if id == "timestamp.string" {
		_, offset := t.Zone()
		// RFC3339 cannot represent offsets with seconds or years outside this range.
		if t.Year() < 0 || t.Year() > 9999 || offset%60 != 0 || offset < -86340 || offset > 86340 {
			return null(), nil
		}
		return ordinary(core.String(t.Format(time.RFC3339Nano))), nil
	}
	var component int
	switch id {
	case "timestamp.year":
		component = t.Year()
	case "timestamp.month":
		component = int(t.Month())
	case "timestamp.dayofmonth":
		component = t.Day()
	case "timestamp.dayofyear":
		component = t.YearDay()
	case "timestamp.hours":
		component = t.Hour()
	case "timestamp.minutes":
		component = t.Minute()
	case "timestamp.seconds":
		component = t.Second()
	case "timestamp.milliseconds":
		component = t.Nanosecond() / 1000000
	}
	return ordinary(core.Int(int64(component))), nil
}

func parseTimestamp(text string) core.Value {
	if len(text) < 20 || text[4] != '-' || text[7] != '-' || text[10] != 'T' || text[13] != ':' || text[16] != ':' {
		return core.Null()
	}
	for _, i := range []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if text[i] < '0' || text[i] > '9' {
			return core.Null()
		}
	}
	if int(text[17]-'0')*10+int(text[18]-'0') > 59 {
		return core.Null()
	}
	i := 19
	if text[i] == '.' {
		i++
		start := i
		for i < len(text) && text[i] >= '0' && text[i] <= '9' {
			i++
		}
		if i-start < 1 || i-start > 9 {
			return core.Null()
		}
	}
	if text[i:] != "Z" {
		if _, ok := fixedZone(text[i:]); !ok {
			return core.Null()
		}
	}
	// Parse uses time.Local to recognize numeric offsets. UTC avoids its lazy
	// environment/filesystem initialization during evaluation.
	t, err := time.ParseInLocation(time.RFC3339Nano, text, time.UTC)
	if err != nil || t.UTC().Year() < 0 || t.UTC().Year() > 9999 {
		return core.Null()
	}
	return core.Timestamp(t)
}

func parseDuration(text string) core.Value {
	negative := false
	if len(text) > 0 && (text[0] == '+' || text[0] == '-') {
		negative = text[0] == '-'
		text = text[1:]
	}
	if text == "0" {
		return core.Duration(0)
	}
	if text == "" {
		return core.Null()
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
			return core.Null()
		}
		amount, ok := new(big.Rat).SetString(text[:i])
		if !ok {
			return core.Null()
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
			return core.Null()
		}
		amount.Mul(amount, new(big.Rat).SetInt64(unit))
		total.Add(total, amount)
	}
	if negative {
		total.Neg(total)
	}
	if !total.IsInt() || !total.Num().IsInt64() {
		return core.Null()
	}
	return core.Duration(time.Duration(total.Num().Int64()))
}
