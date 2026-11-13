package validatorx

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Date is a JS Date built from ES ISO date strings or timestamps; times without an offset are UTC.
type Date struct {
	time float64
}

const (
	_msPerDay      = 86_400_000
	_maxTime       = 8.64e15
	_daysToEpoch   = 719468
	_daysPerEra    = 146097
	_maxFourDigits = 9999
)

var _isoDateRe = regexp.MustCompile(
	`^([+-]\d{6}|\d{4})(?:-(\d{2})(?:-(\d{2}))?)?` +
		`(?:T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d+))?)?(Z|[+-]\d{2}:\d{2})?)?$`,
)

// NewDate is new Date(value).
func NewDate(v Value) *Date {
	switch x := v.(type) {
	case *Date:
		return &Date{time: x.time}
	case string:
		return &Date{time: parseISODate(x)}
	}

	n := toNumber(v)
	if math.Abs(n) > _maxTime || math.IsNaN(n) {
		return &Date{time: math.NaN()}
	}

	return &Date{time: math.Trunc(n)}
}

// Valid reports whether the time value is a number.
func (d *Date) Valid() bool {
	return !math.IsNaN(d.time)
}

func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}

	n, _ := strconv.Atoi(s)

	return n
}

func parseISODate(text string) float64 {
	m := _isoDateRe.FindStringSubmatch(text)
	if len(m) < 9 || m[1] == "-000000" {
		return math.NaN()
	}

	year := atoiOr(m[1], 0)
	month := atoiOr(m[2], 1)
	day := atoiOr(m[3], 1)
	hour := atoiOr(m[4], 0)
	minute := atoiOr(m[5], 0)
	second := atoiOr(m[6], 0)

	fraction := m[7] + "000"
	millis := atoiOr(fraction[:3], 0)

	if month < 1 || month > 12 || day < 1 || day > 31 || minute >= 60 || second >= 60 {
		return math.NaN()
	}

	if hour > 24 || (hour == 24 && (minute != 0 || second != 0 || millis != 0)) {
		return math.NaN()
	}

	t := float64(daysFromCivil(year, month, day))*_msPerDay +
		float64(((hour*60+minute)*60+second)*1000+millis)

	if offset := m[8]; offset != "" && offset != "Z" {
		sign := 1.0
		if offset[0] == '-' {
			sign = -1
		}

		t -= sign * float64((atoiOr(offset[1:3], 0)*60+atoiOr(offset[4:6], 0))*60_000)
	}

	if math.Abs(t) > _maxTime {
		return math.NaN()
	}

	return t
}

func floorDiv(a, b int) int {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}

	return q
}

func daysFromCivil(year, month, day int) int {
	year += floorDiv(month-1, 12)
	month = (month-1)%12 + 1

	y := year
	if month <= 2 {
		y--
	}

	era := floorDiv(y, 400)
	yoe := y - era*400

	mp := month - 3
	if month <= 2 {
		mp = month + 9
	}

	doy := (153*mp+2)/5 + day - 1
	doe := yoe*365 + yoe/4 - yoe/100 + doy

	return era*_daysPerEra + doe - _daysToEpoch
}

// String is toISOString, or "Invalid Date".
func (d *Date) String() string {
	if !d.Valid() {
		return "Invalid Date"
	}

	ms := int(d.time)
	days := floorDiv(ms, _msPerDay)
	millis := ms - days*_msPerDay

	z := days + _daysToEpoch
	era := floorDiv(z, _daysPerEra)
	doe := z - era*_daysPerEra
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	day := doy - (153*mp+2)/5 + 1

	month := mp - 9
	if mp < 10 {
		month = mp + 3
	}

	year := yoe + era*400
	if month <= 2 {
		year++
	}

	sec, msec := millis/1000, millis%1000
	minutes, s := sec/60, sec%60
	hour, minute := minutes/60, minutes%60

	yearText := fmt.Sprintf("%04d", year)
	if year < 0 || year > _maxFourDigits {
		sign := "+"
		if year < 0 {
			sign = "-"
			year = -year
		}

		yearText = fmt.Sprintf("%s%06d", sign, year)
	}

	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ", yearText, month, day, hour, minute, s, msec)
}
