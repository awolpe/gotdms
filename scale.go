package tdms

import (
	"fmt"
	"strings"
	"time"
)

// LinearScale is an NI_Scale[*]_Linear_* pair stored as ordinary properties.
// Read does not apply it; call Apply on raw samples when scaled engineering
// units are wanted. NI's worked example uses a 1-based index (NI_Scale[1]_…).
// LinearScale searches indexes 0 through 8 and returns the first scale whose
// type is "Linear".
type LinearScale struct {
	Slope       float64
	Intercept   float64
	InputSource uint32
	Index       int
}

// Apply returns raw*Slope + Intercept.
func (s LinearScale) Apply(raw float64) float64 { return raw*s.Slope + s.Intercept }

// Unit returns the unit_string property, then NI_UnitDescription.
func (c *Channel) Unit() (string, bool) {
	if s, ok := propString(c.props, PropUnitString); ok {
		return s, true
	}
	return propString(c.props, "NI_UnitDescription")
}

// Increment returns wf_increment when it is numeric.
func (c *Channel) Increment() (float64, bool) {
	return propFloat(c.props, PropIncrement)
}

// StartTime returns wf_start_time when it is a timestamp.
func (c *Channel) StartTime() (time.Time, bool) {
	p, ok := findProp(c.props, PropStartTime)
	if !ok {
		return time.Time{}, false
	}
	switch v := p.Value.(type) {
	case Timestamp:
		return v.Time()
	case time.Time:
		return v, true
	default:
		return time.Time{}, false
	}
}

// SampleCount returns the wf_samples property. It is the waveform header's
// sample count, which can differ from Len when segments have been appended.
func (c *Channel) SampleCount() (uint64, bool) {
	return propUint(c.props, PropSamples)
}

// LinearScale returns the first NI linear scale attached to the channel.
func (c *Channel) LinearScale() (LinearScale, bool) {
	for i := 0; i <= 8; i++ {
		typ, ok := propString(c.props, fmt.Sprintf("NI_Scale[%d]_Scale_Type", i))
		if !ok || !strings.EqualFold(typ, "Linear") {
			continue
		}
		slope, okS := propFloat(c.props, fmt.Sprintf("NI_Scale[%d]_Linear_Slope", i))
		icept, okI := propFloat(c.props, fmt.Sprintf("NI_Scale[%d]_Linear_Y_Intercept", i))
		if !okS || !okI {
			continue
		}
		src, _ := propUint(c.props, fmt.Sprintf("NI_Scale[%d]_Linear_Input_Source", i))
		return LinearScale{Slope: slope, Intercept: icept, InputSource: uint32(src), Index: i}, true
	}
	return LinearScale{}, false
}

func propString(props []Property, name string) (string, bool) {
	p, ok := findProp(props, name)
	if !ok {
		return "", false
	}
	s, ok := p.Value.(string)
	return s, ok
}

func propFloat(props []Property, name string) (float64, bool) {
	p, ok := findProp(props, name)
	if !ok {
		return 0, false
	}
	return asFloat64(p.Value)
}

func propUint(props []Property, name string) (uint64, bool) {
	p, ok := findProp(props, name)
	if !ok {
		return 0, false
	}
	if u, ok := asUint64(p.Value); ok {
		return u, true
	}
	if i, ok := asInt64(p.Value); ok && i >= 0 {
		return uint64(i), true
	}
	return 0, false
}
