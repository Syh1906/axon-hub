package budget

import (
	"fmt"
	"time"
)

type Period string
type Mode string

const (
	FiveHour Period = "five_hour"
	Daily    Period = "daily"
	Weekly   Period = "weekly"
	Monthly  Period = "monthly"
	Total    Period = "total"
	Fixed    Mode   = "fixed"
	Rolling  Mode   = "rolling"
)

var Periods = [...]Period{FiveHour, Daily, Weekly, Monthly, Total}

type Rule struct {
	Period   Period `json:"period"`
	Mode     Mode   `json:"mode"`
	Limit    Amount `json:"limit"`
	Timezone string `json:"timezone,omitempty"`
	// Minute is the local minute of day for a fixed daily boundary.
	Minute int `json:"minute"`
}

// Clock separates the accounting reset from the schedule anchor. Keep the
// original calendar anchor (including day 31) when a short month clamps it.
type Clock struct {
	Anchor       *time.Time `json:"anchor,omitempty"`
	ResetAt      *time.Time `json:"resetAt,omitempty"`
	ResetVersion int64      `json:"resetVersion"`
	CycleVersion int64      `json:"cycleVersion"`
}

type Window struct {
	Empty bool       `json:"empty"`
	Start *time.Time `json:"start,omitempty"`
	End   *time.Time `json:"end,omitempty"`
	// Rolling intervals exclude Start; fixed intervals exclude End.
	StartExclusive bool `json:"startExclusive"`
}

func (r Rule) Validate() error {
	if r.Timezone != "" {
		if _, err := time.LoadLocation(r.Timezone); err != nil {
			return fmt.Errorf("invalid rule timezone")
		}
	}
	if r.Limit < 0 {
		return fmt.Errorf("negative limit")
	}
	if r.Minute < 0 || r.Minute >= 1440 {
		return fmt.Errorf("invalid daily reset time")
	}
	switch r.Period {
	case FiveHour, Daily, Weekly, Monthly:
		if r.Mode != Fixed && r.Mode != Rolling {
			return fmt.Errorf("invalid budget mode")
		}
	case Total:
		if r.Mode != Fixed {
			return fmt.Errorf("total budget has no rolling period")
		}
	default:
		return fmt.Errorf("invalid budget period")
	}
	return nil
}

func (r Rule) Duration() time.Duration {
	switch r.Period {
	case FiveHour:
		return 5 * time.Hour
	case Daily:
		return 24 * time.Hour
	case Weekly:
		return 7 * 24 * time.Hour
	case Monthly:
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}

func (r Rule) CanRestartCycle() bool { return r.Mode == Fixed && r.Period != Total }

// Bounds samples no clock itself: callers pass one authoritative time for both
// key and group. The location is the persisted budget timezone, not browser time.
func (r Rule) Bounds(now time.Time, clock Clock, loc *time.Location) (Window, error) {
	if err := r.Validate(); err != nil {
		return Window{}, err
	}
	if loc == nil {
		return Window{}, fmt.Errorf("budget timezone is required")
	}
	now = now.In(loc)
	if r.Period == Total {
		return Window{Start: clock.ResetAt}, nil
	}
	if r.Mode == Rolling {
		start := now.Add(-r.Duration())
		return Window{Start: &start, End: &now, StartExclusive: true}, nil
	}
	if r.Period == FiveHour {
		if clock.Anchor == nil || !now.Before(clock.Anchor.Add(5*time.Hour)) {
			return Window{Empty: true}, nil
		}
		end := clock.Anchor.Add(5 * time.Hour)
		return Window{Start: clock.Anchor, End: &end}, nil
	}
	if clock.Anchor != nil {
		return r.anchoredBounds(now, clock.Anchor.In(loc)), nil
	}
	y, m, d := now.Date()
	start := time.Date(y, m, d, 0, 0, 0, 0, loc)
	var end time.Time
	switch r.Period {
	case Daily:
		start = time.Date(y, m, d, r.Minute/60, r.Minute%60, 0, 0, loc)
		offset := 0
		if now.Before(start) {
			offset = -1
			start = time.Date(y, m, d-1, r.Minute/60, r.Minute%60, 0, 0, loc)
		}
		end = time.Date(y, m, d+offset+1, r.Minute/60, r.Minute%60, 0, 0, loc)
	case Weekly:
		start = start.AddDate(0, 0, -(int(now.Weekday())+6)%7)
		end = start.AddDate(0, 0, 7)
	case Monthly:
		start = time.Date(y, m, 1, 0, 0, 0, 0, loc)
		end = start.AddDate(0, 1, 0)
	}
	return Window{Start: &start, End: &end}, nil
}

func (r Rule) anchoredBounds(now, anchor time.Time) Window {
	var start, end time.Time
	if r.Period == Monthly {
		n := (now.Year()-anchor.Year())*12 + int(now.Month()-anchor.Month())
		start = calendarMonth(anchor, n)
		if now.Before(start) {
			n--
			start = calendarMonth(anchor, n)
		}
		end = calendarMonth(anchor, n+1)
	} else {
		step := 1
		if r.Period == Weekly {
			step = 7
		}
		// Date-only UTC subtraction counts calendar days, including DST changes.
		date := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		base := time.Date(anchor.Year(), anchor.Month(), anchor.Day(), 0, 0, 0, 0, time.UTC)
		n := int(date.Sub(base)/(24*time.Hour)) / step
		start = calendarDay(anchor, n*step)
		if now.Before(start) {
			n--
			start = calendarDay(anchor, n*step)
		}
		end = calendarDay(anchor, (n+1)*step)
	}
	return Window{Start: &start, End: &end}
}

func calendarMonth(anchor time.Time, offset int) time.Time {
	if offset == 0 {
		return anchor
	}
	month := time.Date(anchor.Year(), anchor.Month()+time.Month(offset), 1, 0, 0, 0, 0, anchor.Location())
	last := month.AddDate(0, 1, -1).Day()
	day := anchor.Day()
	if day > last {
		day = last
	}
	return time.Date(month.Year(), month.Month(), day, anchor.Hour(), anchor.Minute(), anchor.Second(), anchor.Nanosecond(), anchor.Location())
}

// Rebuilding a zero-offset date can select the other occurrence of a repeated
// local hour during DST fall-back. Preserve the administrator's exact instant.
func calendarDay(anchor time.Time, offset int) time.Time {
	if offset == 0 {
		return anchor
	}
	return anchor.AddDate(0, 0, offset)
}

// Reset operates on a copy. A persistence layer must commit it together with the
// audit event and reject stale versions; outstanding reservations are untouched.
func (r Rule) Reset(now time.Time, clock Clock, loc *time.Location, restart bool) (Clock, error) {
	window, err := r.Bounds(now, clock, loc)
	if err != nil {
		return clock, err
	}
	if restart && !r.CanRestartCycle() {
		return clock, fmt.Errorf("this budget has no fixed cycle to restart")
	}
	clock.ResetVersion++
	clock.ResetAt = &now
	if restart {
		clock.Anchor = &now
		clock.CycleVersion++
	} else if r.Period == FiveHour && window.End == nil {
		clock.Anchor = nil
	}
	return clock, nil
}

// StartFixedFiveHour is called only after the first positive settlement. A
// reservation alone must never start or extend the five-hour countdown.
func (r Rule) StartFixedFiveHour(now time.Time, clock Clock, cost Amount) Clock {
	if cost > 0 && r.Period == FiveHour && r.Mode == Fixed && (clock.Anchor == nil || !now.Before(clock.Anchor.Add(5*time.Hour))) {
		clock.Anchor = &now
	}
	return clock
}
