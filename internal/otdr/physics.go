package otdr

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math"
)

// C is the speed of light in vacuum, in m/s.
const C = 299792458.0

// Calibration converts raw acquisition indices to metric distance for one
// OTDR trace. The group index (group refractive index, IOR) is the only
// parameter that changes the index->distance mapping; the pulse width and
// averaging count describe the acquisition but never rescale distance.
type Calibration struct {
	// T0NS is the time offset of sample 0 relative to the launch event, in ns.
	// It accounts for the instrument/front-panel delay so that the launch
	// connector lands at its true metric position.
	T0NS float64 `json:"t0_ns"`
	// DTNS is the sampling interval in nanoseconds of round-trip time.
	DTNS float64 `json:"dt_ns"`
	// GroupIndex is the group refractive index (IOR) used for this calibration.
	GroupIndex float64 `json:"group_index"`
}

// OneWayMeters returns the one-way fiber distance of sample index i.
//
//	distance = (t0 + i*dt) * C / (2 * n)   (dt, t0 expressed in seconds)
//
// Round-trip samples therefore map to one-way fiber length via the factor 2.
func (c Calibration) OneWayMeters(i int) float64 {
	if i < 0 {
		i = 0
	}
	tNS := c.T0NS + float64(i)*c.DTNS
	return (tNS * 1e-9) * C / (2.0 * c.GroupIndex)
}

// IndexAtMeters is the inverse mapping: fractional sample index for a distance.
func (c Calibration) IndexAtMeters(meters float64) float64 {
	tNS := 2.0 * c.GroupIndex * meters / C * 1e9
	return (tNS - c.T0NS) / c.DTNS
}

// WithGroupIndex returns a copy of the calibration rescaled for a new group
// index. Raw samples are untouched; only their metric interpretation changes.
func (c Calibration) WithGroupIndex(n float64) Calibration {
	return Calibration{T0NS: c.T0NS, DTNS: c.DTNS, GroupIndex: n}
}

// SpatialSampleMeters is the metric distance covered by one round-trip sample.
func (c Calibration) SpatialSampleMeters() float64 {
	return c.DTNS * 1e-9 * C / (2.0 * c.GroupIndex)
}

// PulseDeadZoneMeters converts a launch dead zone given in nanoseconds of
// round-trip time to one-way meters under the current group index.
func PulseDeadZoneMeters(deadZoneNS, groupIndex float64) float64 {
	return deadZoneNS * 1e-9 * C / (2.0 * groupIndex)
}

// RawTrace is the immutable acquisition record. Its identity and its sample
// identity never change when the group index is edited: only calibration
// versions change. Power is stored in dB (log power as delivered by the
// instrument, equivalent to 10*log10 of received fraction).
type RawTrace struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	PowerDB      []float64 `json:"-"`
	PulseWidthNS float64   `json:"pulse_width_ns"`
	// DTNS is the acquisition sampling interval (round-trip ns per sample).
	DTNS float64 `json:"dt_ns"`
// T0NS is the time offset of sample 0 (round-trip ns), instrument delay.
	T0NS         float64 `json:"t0_ns"`
	GroupIndex   float64 `json:"group_index"`
	Averages     int     `json:"averages"`
	WavelengthNM int     `json:"wavelength_nm"`
}

// SampleCount returns the number of raw samples.
func (t *RawTrace) SampleCount() int { return len(t.PowerDB) }

// Checksum is a content hash over the raw acquisition parameters and every raw
// sample value. Re-imports and replays must reproduce it byte-for-byte.
func (t *RawTrace) Checksum() string {
	h := sha256.New()
	var buf [8]byte
	put := func(f float64) {
		binary.LittleEndian.PutUint64(buf[:], math.Float64bits(f))
		_, _ = h.Write(buf[:])
	}
	_, _ = h.Write([]byte(t.ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(t.Name))
	_, _ = h.Write([]byte{0})
	put(t.PulseWidthNS)
	put(t.DTNS)
	put(t.T0NS)
	put(t.GroupIndex)
	binary.LittleEndian.PutUint64(buf[:], uint64(t.Averages))
	_, _ = h.Write(buf[:])
	binary.LittleEndian.PutUint64(buf[:], uint64(t.WavelengthNM))
	_, _ = h.Write(buf[:])
	binary.LittleEndian.PutUint64(buf[:], uint64(len(t.PowerDB)))
	_, _ = h.Write(buf[:])
	for _, v := range t.PowerDB {
		put(v)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EncodeSamples stores raw samples as little-endian float64 bytes.
func EncodeSamples(p []float64) []byte {
	b := make([]byte, 8*len(p))
	for i, v := range p {
		binary.LittleEndian.PutUint64(b[i*8:], math.Float64bits(v))
	}
	return b
}

// DecodeSamples restores raw samples from little-endian float64 bytes.
func DecodeSamples(b []byte) []float64 {
	n := len(b) / 8
	p := make([]float64, n)
	for i := 0; i < n; i++ {
		p[i] = math.Float64frombits(binary.LittleEndian.Uint64(b[i*8:]))
	}
	return p
}
