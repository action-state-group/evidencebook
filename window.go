// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// WindowPeriod is the length of a windowed shelf's windows: a calendar
// period in UTC (evidencebook#689, docs/design/689-window-books.md §1).
type WindowPeriod string

// The window periods.
const (
	WindowDay     WindowPeriod = "day"
	WindowWeek    WindowPeriod = "week" // ISO 8601 weeks, from Monday
	WindowMonth   WindowPeriod = "month"
	WindowQuarter WindowPeriod = "quarter"
	WindowYear    WindowPeriod = "year"
)

// WindowPolicy splits a shelf's records into one book per time window. It is
// fixed when the shelf's file is created and can never change afterwards:
// records are never moved between books.
type WindowPolicy struct {
	Period WindowPeriod
	// Base is the start of every window book's log id: "<base>/<label>".
	Base string
	// Since is the first window's start. It must be the start of a period,
	// so the first window is an ordinary one.
	Since time.Time
}

// Window is one window of a policy: its label and its half-open bounds
// [Start, End), in UTC.
type Window struct {
	Label string    `json:"label"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// LogID is the window book's log id under base.
func (w Window) LogID(base string) string { return base + "/" + w.Label }

// Contains reports whether t falls in the window.
func (w Window) Contains(t time.Time) bool {
	return !t.Before(w.Start) && t.Before(w.End)
}

func (p WindowPeriod) valid() bool {
	switch p {
	case WindowDay, WindowWeek, WindowMonth, WindowQuarter, WindowYear:
		return true
	}
	return false
}

// start is the start of the period holding t.
func (p WindowPeriod) start(t time.Time) time.Time {
	t = t.UTC()
	y, m, d := t.Date()
	switch p {
	case WindowDay:
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	case WindowWeek:
		day := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		offset := (int(day.Weekday()) + 6) % 7 // Monday is 0
		return day.AddDate(0, 0, -offset)
	case WindowMonth:
		return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
	case WindowQuarter:
		return time.Date(y, time.Month((int(m)-1)/3*3+1), 1, 0, 0, 0, 0, time.UTC)
	default: // WindowYear
		return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
	}
}

// next is the start of the period after the one starting at start.
func (p WindowPeriod) next(start time.Time) time.Time {
	switch p {
	case WindowDay:
		return start.AddDate(0, 0, 1)
	case WindowWeek:
		return start.AddDate(0, 0, 7)
	case WindowMonth:
		return start.AddDate(0, 1, 0)
	case WindowQuarter:
		return start.AddDate(0, 3, 0)
	default:
		return start.AddDate(1, 0, 0)
	}
}

// label names the period starting at start.
func (p WindowPeriod) label(start time.Time) string {
	switch p {
	case WindowDay:
		return start.Format("2006-01-02")
	case WindowWeek:
		y, w := start.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", y, w)
	case WindowMonth:
		return start.Format("2006-01")
	case WindowQuarter:
		return fmt.Sprintf("%04d-Q%d", start.Year(), (int(start.Month())-1)/3+1)
	default:
		return fmt.Sprintf("%04d", start.Year())
	}
}

// WindowAt is the window of period p holding t.
func (p WindowPeriod) WindowAt(t time.Time) Window {
	start := p.start(t)
	return Window{Label: p.label(start), Start: start, End: p.next(start)}
}

// Next is the window after w.
func (p WindowPeriod) Next(w Window) Window {
	return Window{Label: p.label(w.End), Start: w.End, End: p.next(w.End)}
}

var windowLabels = map[WindowPeriod]*regexp.Regexp{
	WindowDay:     regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`),
	WindowWeek:    regexp.MustCompile(`^(\d{4})-W(\d{2})$`),
	WindowMonth:   regexp.MustCompile(`^\d{4}-\d{2}$`),
	WindowQuarter: regexp.MustCompile(`^(\d{4})-Q([1-4])$`),
	WindowYear:    regexp.MustCompile(`^\d{4}$`),
}

// ParseWindow is the window of period p a label names. A label alone gives
// its bounds, so a reader recomputes them rather than trusting stated ones;
// a label that is not the canonical name of a period is refused.
func (p WindowPeriod) ParseWindow(label string) (Window, error) {
	pattern, ok := windowLabels[p]
	if !ok {
		return Window{}, fmt.Errorf("%w: window period %q is not one of day, week, month, quarter, year", ErrInvalid, p)
	}
	match := pattern.FindStringSubmatch(label)
	if match == nil {
		return Window{}, fmt.Errorf("%w: %q is not a %s label", ErrInvalid, label, p)
	}
	var start time.Time
	var err error
	switch p {
	case WindowDay:
		start, err = time.Parse("2006-01-02", label)
	case WindowMonth:
		start, err = time.Parse("2006-01", label)
	case WindowYear:
		start, err = time.Parse("2006", label)
	case WindowQuarter:
		year, _ := strconv.Atoi(match[1])
		quarter, _ := strconv.Atoi(match[2])
		start = time.Date(year, time.Month((quarter-1)*3+1), 1, 0, 0, 0, 0, time.UTC)
	case WindowWeek:
		year, _ := strconv.Atoi(match[1])
		week, _ := strconv.Atoi(match[2])
		// ISO week 1 holds January 4th.
		start = WindowWeek.start(time.Date(year, 1, 4, 0, 0, 0, 0, time.UTC)).AddDate(0, 0, 7*(week-1))
	}
	if err != nil {
		return Window{}, fmt.Errorf("%w: %q is not a %s label", ErrInvalid, label, p)
	}
	w := p.WindowAt(start)
	if w.Label != label {
		return Window{}, fmt.Errorf("%w: %q is not a %s label", ErrInvalid, label, p)
	}
	return w, nil
}

// validate checks a policy as a shelf is created with it.
func (wp WindowPolicy) validate() error {
	if !wp.Period.valid() {
		return fmt.Errorf("%w: window period %q is not one of day, week, month, quarter, year", ErrInvalid, wp.Period)
	}
	if wp.Base == "" || strings.ContainsAny(wp.Base, "\x00") {
		return fmt.Errorf("%w: a window policy names its base log id", ErrInvalid)
	}
	if wp.Since.IsZero() || !wp.Since.Equal(wp.Period.start(wp.Since)) {
		return fmt.Errorf("%w: the window policy's since (%s) is not the start of a %s", ErrInvalid, wp.Since.UTC().Format(time.RFC3339), wp.Period)
	}
	return nil
}

// first is the policy's first window.
func (wp WindowPolicy) first() Window { return wp.Period.WindowAt(wp.Since) }
