package tdms

import (
	"math/bits"
	"time"
)

// tdmsEpochUnix is the Unix time of 1904-01-01 00:00:00 UTC, the TDMS epoch.
// TDMS ignores leap seconds, matching time.Time.
const tdmsEpochUnix int64 = -2082844800

// Timestamp is a TDMS timestamp: signed seconds since 1904-01-01 00:00:00 UTC
// plus a positive fraction of a second in units of 2^-64 s.
//
// The fraction is an absolute offset forward from Seconds, including when
// Seconds is negative. One half-second before the epoch is Seconds == -1 and
// Fraction == 2^63, not Seconds == 0 with a negative fraction.
//
// On disk (little-endian) the fraction is stored first as uint64, then the
// seconds as int64. Big-endian segments store seconds first, then the
// fraction, each field big-endian. That field order matches files readers
// such as npTDMS accept; the NI article does not spell out the big-endian
// field order.
type Timestamp struct {
	Seconds  int64
	Fraction uint64
}

// NewTimestamp converts t to a TDMS timestamp. The location is converted to
// UTC. The fraction is the nearest 2^-64 second to t's nanosecond, chosen so
// Timestamp.Time round-trips the same nanosecond.
func NewTimestamp(t time.Time) Timestamp {
	t = t.UTC()
	sec := t.Unix() - tdmsEpochUnix
	return Timestamp{Seconds: sec, Fraction: nsToFraction(uint64(t.Nanosecond()))}
}

// Time converts ts to UTC with nanosecond resolution. ok is false when the
// second count cannot be represented as a Unix time (only timestamps within a
// few seconds of the int64 limits fail). Sub-nanosecond fraction bits are rounded
// to the nearest nanosecond; Timestamps preserves those bits.
func (ts Timestamp) Time() (time.Time, bool) {
	// Unix seconds = TDMS seconds + (Unix time of the TDMS epoch).
	sec, ok := addSigned(ts.Seconds, tdmsEpochUnix)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(sec, int64(fractionToNs(ts.Fraction))).UTC(), true
}

func addSigned(a, b int64) (int64, bool) {
	c := a + b
	if (b > 0 && c < a) || (b < 0 && c > a) {
		return 0, false
	}
	return c, true
}

// nsToFraction returns the 2^-64 fraction nearest to ns nanoseconds.
// ns must be in 0..999999999. The result converts back through fractionToNs.
func nsToFraction(ns uint64) uint64 {
	if ns == 0 {
		return 0
	}
	if ns > 999_999_999 {
		ns = 999_999_999
	}
	q, r := bits.Div64(ns, 0, 1_000_000_000)
	if r >= 500_000_000 {
		q++
	}
	if fractionToNs(q) == ns {
		return q
	}
	if q > 0 && fractionToNs(q-1) == ns {
		return q - 1
	}
	if q < ^uint64(0) && fractionToNs(q+1) == ns {
		return q + 1
	}
	return q
}

// fractionToNs rounds a 2^-64 fraction to the nearest nanosecond in 0..999999999.
func fractionToNs(frac uint64) uint64 {
	hi, lo := bits.Mul64(frac, 1_000_000_000)
	if lo >= 1<<63 {
		hi++
	}
	if hi > 999_999_999 {
		return 999_999_999
	}
	return hi
}
