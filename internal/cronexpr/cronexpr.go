// Package cronexpr parses cron expressions and computes their fire times in a
// time zone. It is a small purpose-built parser instead of a dependency: the
// scheduler needs exact, documented behaviour around daylight saving changes,
// which general libraries leave unspecified.
//
// Syntax: five fields (minute hour day-of-month month day-of-week) or six with
// a leading seconds field. Each field takes "*" (or "?"), numbers, ranges
// "a-b", steps "*/n" and "a-b/n", names (JAN-DEC, SUN-SAT) and comma lists.
// Day-of-week 7 means Sunday. When both day fields are restricted a day
// matches if either does (Vixie cron).
package cronexpr

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// searchYears bounds the search for the next fire time, so an expression that
// can never match (31 February) ends instead of looping.
const searchYears = 8

// Expr is a parsed cron expression.
type Expr struct {
	sec, min, hour, dom, month, dow uint64
	domAny, dowAny                  bool
}

type fieldSpec struct {
	name     string
	min, max int
	names    []string // names[i] is the name of value min+i
}

var (
	secondSpec = fieldSpec{name: "second", min: 0, max: 59}
	minuteSpec = fieldSpec{name: "minute", min: 0, max: 59}
	hourSpec   = fieldSpec{name: "hour", min: 0, max: 23}
	domSpec    = fieldSpec{name: "day of month", min: 1, max: 31}
	monthSpec  = fieldSpec{name: "month", min: 1, max: 12, names: strings.Fields("JAN FEB MAR APR MAY JUN JUL AUG SEP OCT NOV DEC")}
	dowSpec    = fieldSpec{name: "day of week", min: 0, max: 7, names: strings.Fields("SUN MON TUE WED THU FRI SAT")}
)

// Parse reads a five- or six-field cron expression.
func Parse(expr string) (*Expr, error) {
	fields := strings.Fields(expr)
	switch len(fields) {
	case 5:
		fields = append([]string{"0"}, fields...)
	case 6:
	default:
		return nil, fmt.Errorf("cron expression %q must have 5 or 6 fields, has %d", expr, len(fields))
	}
	specs := []fieldSpec{secondSpec, minuteSpec, hourSpec, domSpec, monthSpec, dowSpec}
	var masks [6]uint64
	var errs []error
	for i, f := range fields {
		m, err := parseField(f, specs[i])
		if err != nil {
			errs = append(errs, err)
		}
		masks[i] = m
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("cron expression %q: %w", expr, err)
	}
	e := &Expr{sec: masks[0], min: masks[1], hour: masks[2], dom: masks[3], month: masks[4], dow: masks[5],
		domAny: isAny(fields[3]), dowAny: isAny(fields[5])}
	if e.dow&(1<<7) != 0 { // 7 is Sunday
		e.dow |= 1
	}
	return e, nil
}

func isAny(f string) bool { return f == "*" || f == "?" }

func parseField(f string, s fieldSpec) (uint64, error) {
	var mask uint64
	for _, part := range strings.Split(f, ",") {
		m, err := parsePart(part, s)
		if err != nil {
			return 0, err
		}
		mask |= m
	}
	return mask, nil
}

func parsePart(part string, s fieldSpec) (uint64, error) {
	rangePart, stepPart, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepPart)
		if err != nil || n < 1 {
			return 0, fmt.Errorf("%s: bad step %q", s.name, stepPart)
		}
		step = n
	}
	lo, hi := s.min, s.max
	if s.name == dowSpec.name {
		hi = 6 // "*" covers 0-6; an explicit 7 is accepted below
	}
	switch {
	case rangePart == "*" || rangePart == "?":
	case strings.Contains(rangePart, "-"):
		a, b, _ := strings.Cut(rangePart, "-")
		var err error
		if lo, err = value(a, s); err != nil {
			return 0, err
		}
		if hi, err = value(b, s); err != nil {
			return 0, err
		}
		if lo > hi {
			return 0, fmt.Errorf("%s: range %q is backwards", s.name, rangePart)
		}
	default:
		v, err := value(rangePart, s)
		if err != nil {
			return 0, err
		}
		lo, hi = v, v
		if hasStep { // "5/15" means 5-max by 15
			hi = s.max
		}
	}
	var mask uint64
	for v := lo; v <= hi; v += step {
		mask |= 1 << uint(v)
	}
	return mask, nil
}

func value(tok string, s fieldSpec) (int, error) {
	if n, err := strconv.Atoi(tok); err == nil {
		if n < s.min || n > s.max {
			return 0, fmt.Errorf("%s: %d is outside %d-%d", s.name, n, s.min, s.max)
		}
		return n, nil
	}
	for i, name := range s.names {
		if strings.EqualFold(tok, name) {
			return s.min + i, nil
		}
	}
	return 0, fmt.Errorf("%s: unknown value %q", s.name, tok)
}

// dayMatches applies the day-of-month / day-of-week rule to a date.
func (e *Expr) dayMatches(d time.Time) bool {
	domOK := e.dom&(1<<uint(d.Day())) != 0
	dowOK := e.dow&(1<<uint(d.Weekday())) != 0
	switch {
	case e.domAny && e.dowAny:
		return true
	case e.domAny:
		return dowOK
	case e.dowAny:
		return domOK
	}
	return domOK || dowOK
}

// Next returns the first fire time strictly after the given instant, evaluating
// the expression in loc, or the zero time when none exists within eight years.
//
// Daylight saving: a wall-clock time that does not exist (the clock jumped over
// it) fires at the instant of the jump; a wall-clock time that occurs twice
// fires only at its first occurrence.
func (e *Expr) Next(after time.Time, loc *time.Location) time.Time {
	w := after.In(loc)
	// Search over "naive" wall-clock times held in UTC so that adding durations
	// never crosses a daylight saving change.
	cur := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, time.UTC).Add(time.Second)
	limit := cur.AddDate(searchYears, 0, 0)
	for cur.Before(limit) {
		switch {
		case e.month&(1<<uint(cur.Month())) == 0:
			cur = time.Date(cur.Year(), cur.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !e.dayMatches(cur):
			cur = time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, time.UTC)
		case e.hour&(1<<uint(cur.Hour())) == 0:
			cur = time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour()+1, 0, 0, 0, time.UTC)
		case e.min&(1<<uint(cur.Minute())) == 0:
			cur = time.Date(cur.Year(), cur.Month(), cur.Day(), cur.Hour(), cur.Minute()+1, 0, 0, time.UTC)
		case e.sec&(1<<uint(cur.Second())) == 0:
			cur = cur.Add(time.Second)
		default:
			if at := instant(cur, loc); at.After(after) {
				return at
			}
			cur = cur.Add(time.Second)
		}
	}
	return time.Time{}
}

// instant maps a naive wall-clock time to an instant in loc. A wall time inside
// a daylight saving gap maps to the instant the clock jumps; an ambiguous one
// maps to its first occurrence (time.Date's behaviour).
func instant(naive time.Time, loc *time.Location) time.Time {
	t := time.Date(naive.Year(), naive.Month(), naive.Day(), naive.Hour(), naive.Minute(), naive.Second(), 0, loc)
	if t.Hour() == naive.Hour() && t.Minute() == naive.Minute() {
		return t
	}
	// Gap: time.Date picked an offset on one side of the jump, so t lies near
	// it. Find the jump by bisecting the day around t for the first second that
	// has the later offset.
	lo, hi := t.Add(-24*time.Hour).Unix(), t.Add(24*time.Hour).Unix()
	_, later := time.Unix(hi, 0).In(loc).Zone()
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		if _, off := time.Unix(mid, 0).In(loc).Zone(); off == later {
			hi = mid
		} else {
			lo = mid
		}
	}
	return time.Unix(hi, 0).In(loc)
}
