// Package time provides one machine-local source for application timestamps.
package time

import stdtime "time"

// Time is an immutable snapshot of a timestamp in the machine's local
// timezone. NewTime without an argument captures the current machine time;
// passing an existing timestamp is useful when rendering persisted values in
// the same timezone.
type Time struct {
	value stdtime.Time
}

// NewTime returns a machine-local time snapshot.
//
// An optional value can be supplied to normalize an existing timestamp to the
// machine's local timezone while keeping the same instant. More than one
// value is ignored so callers always have one unambiguous timestamp.
func NewTime(values ...stdtime.Time) Time {
	value := stdtime.Now()
	if len(values) > 0 {
		value = values[0]
	}
	return Time{value: value.In(stdtime.Local)}
}

// Time returns the timestamp snapshot as a standard-library time.Time.
func (t Time) Time() stdtime.Time {
	return t.value
}

// Epoch returns the Unix timestamp in seconds.
func (t Time) Epoch() int64 {
	return t.value.Unix()
}

// EpochMilli returns the Unix timestamp in milliseconds.
func (t Time) EpochMilli() int64 {
	return t.value.UnixMilli()
}

// EpochMicro returns the Unix timestamp in microseconds.
func (t Time) EpochMicro() int64 {
	return t.value.UnixMicro()
}

// EpochNano returns the Unix timestamp in nanoseconds.
func (t Time) EpochNano() int64 {
	return t.value.UnixNano()
}

// Since returns the elapsed duration from value to this timestamp.
func (t Time) Since(value stdtime.Time) stdtime.Duration {
	return t.value.Sub(value)
}

// Until returns the duration from this timestamp until value.
func (t Time) Until(value stdtime.Time) stdtime.Duration {
	return value.Sub(t.value)
}

// DMY returns the date as day/month/year.
func (t Time) DMY() string {
	return t.value.Format("02/01/2006")
}

// Format renders the timestamp with a standard-library layout.
func (t Time) Format(layout string) string {
	return t.value.Format(layout)
}

// RFC3339 renders the timestamp using the RFC3339 layout.
func (t Time) RFC3339() string {
	return t.value.Format(stdtime.RFC3339)
}

// RFC3339Nano renders the timestamp using the RFC3339Nano layout.
func (t Time) RFC3339Nano() string {
	return t.value.Format(stdtime.RFC3339Nano)
}
