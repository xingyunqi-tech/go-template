package worker

import "time"

// daily fires every day at hour:minute in loc.
type daily struct {
	hour, minute int
	loc          *time.Location
}

// Next implements river.PeriodicSchedule.
func (d daily) Next(current time.Time) time.Time {
	t := current.In(d.loc)
	next := time.Date(t.Year(), t.Month(), t.Day(), d.hour, d.minute, 0, 0, d.loc)

	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}

	return next
}

// hourly fires every hour at minute in loc.
type hourly struct {
	minute int
	loc    *time.Location
}

// Next implements river.PeriodicSchedule.
func (h hourly) Next(current time.Time) time.Time {
	t := current.In(h.loc)
	next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), h.minute, 0, 0, h.loc)

	if !next.After(t) {
		next = next.Add(time.Hour)
	}

	return next
}
