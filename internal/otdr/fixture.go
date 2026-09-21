package otdr

import (
	"math"
)

// FixtureSpec describes one fixed acquisition used for demo/replay.
type FixtureSpec struct {
	ID           string
	Name         string
	PulseWidthNS float64
	GroupIndex   float64
	Averages     int
	WavelengthNM int
	// FiberEndM is the true end of measurable fiber; beyond it the trace is
	// instrument noise floor (truncated acquisition -> length lower bound).
	FiberEndM float64
	// TraceEndM is the last sample's metric position at nominal IOR.
	TraceEndM float64
	// StartDB is the backscatter level near launch.
	StartDB float64
	// AttenDBPerKm fiber attenuation.
	AttenDBPerKm float64
	// NoiseSigma is the detector-floor/measurement noise spread in dB.
	NoiseSigma float64
}

// DefaultFixtures contains the two fixed traces. Their real facilities sit at
// identical metric positions (launch 3 m, splice 500 m, connector 600 m,
// truncation 1800 m) but the traces have different IOR, pulse width and
// averaging, so their raw sample indices differ while calibrated distance
// aligns. The connector casts a ghost at twice its range (1200 m).
func DefaultFixtures() []FixtureSpec {
	return []FixtureSpec{
		{
			ID: "trA", Name: "A-100ns-n1.468-64k",
			PulseWidthNS: 100, GroupIndex: 1.4680, Averages: 65536, WavelengthNM: 1550,
			FiberEndM: 1800, TraceEndM: 2100, StartDB: -5.0,
			AttenDBPerKm: 0.20, NoiseSigma: 0.02,
		},
		{
			ID: "trB", Name: "B-30ns-n1.462-16k",
			PulseWidthNS: 30, GroupIndex: 1.4620, Averages: 16384, WavelengthNM: 1550,
			FiberEndM: 1800, TraceEndM: 2100, StartDB: -5.3,
			AttenDBPerKm: 0.21, NoiseSigma: 0.05,
		},
	}
}

// fixedDTNS is the round-trip sampling interval used by the fixed fixtures.
const fixedDTNS = 2.0

// fixedT0NS places sample 0 slightly before launch; the launch connector
// reflection appears at 3 m.
const fixedT0NS = 0.0

// BuildFixture deterministically synthesizes a trace:
//   - launch reflection at 3 m (dead-zone event),
//   - a 0.12 dB non-reflective splice at 500 m,
//   - a strong reflective connector (~13 dB peak, ~0.45 dB loss) at 600 m,
//   - its ghost reflection (~3.0 dB peak, zero loss) at 1200 m,
//   - truncation at 1800 m into a flat noise floor to 2100 m.
func BuildFixture(s FixtureSpec) *RawTrace {
	cal := Calibration{T0NS: fixedT0NS, DTNS: fixedDTNS, GroupIndex: s.GroupIndex}
	n := int(math.Ceil(s.TraceEndM / cal.SpatialSampleMeters())) + 1
	p := make([]float64, n)

	// Facilities are defined in true metric space and sampled per trace, so
	// different IOR yields different raw indices but identical positions.
	const (
		launchM      = 3.0
		spliceM      = 500.0
		connectorM   = 600.0
		ghostM       = 1200.0
		spliceLoss   = 0.12
		connectorLoss = 0.45
		connectorPeak = 13.0
		ghostPeak    = 3.0
		launchPeak   = 11.0
	)

	rng := newRng(hashString(s.ID))
	floorDB := s.StartDB - floorBelowSignalDB

	for k := 0; k < n; k++ {
		d := cal.OneWayMeters(k)
		if d > s.FiberEndM {
			// Truncated acquisition: only noise floor, no fabricated end.
			p[k] = floorDB + rng.Normal()*s.NoiseSigma
			continue
		}

		// Backscatter with attenuation, then discrete drops.
		level := s.StartDB - s.AttenDBPerKm*d/1000.0
		if d >= spliceM {
			level -= spliceLoss
		}
		if d >= connectorM {
			level -= connectorLoss
		}
		p[k] = level

		// Gaussian reflection impulse responses (wider at higher pulse width).
		sigmaM := s.PulseWidthNS * 1e-9 * C / (2.0 * s.GroupIndex)
		addGaussian := func(center, peakDB float64) {
			z := (d - center) / sigmaM
			p[k] += peakDB * math.Exp(-0.5*z*z)
		}
		addGaussian(launchM, launchPeak)
		addGaussian(connectorM, connectorPeak)
		addGaussian(ghostM, ghostPeak)

		p[k] += rng.Normal() * s.NoiseSigma
	}

	return &RawTrace{
		ID:           s.ID,
		Name:         s.Name,
		PowerDB:      p,
		PulseWidthNS: s.PulseWidthNS,
		DTNS:         fixedDTNS,
		T0NS:         fixedT0NS,
		GroupIndex:   s.GroupIndex,
		Averages:     s.Averages,
		WavelengthNM: s.WavelengthNM,
	}
}

// DefaultDeadZoneNS gives each fixture a launch dead zone approximately equal
// to four pulse widths (round trip), covering the launch reflection tail as
// engineers configure on the instrument.
func DefaultDeadZoneNS(t *RawTrace) float64 {
	return t.PulseWidthNS * 4
}

// hashString is FNV-1a 64-bit.
func hashString(s string) uint64 {
	const off, prime = uint64(1469598103934665603), uint64(1099511628211)
	h := off
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

// rng is a tiny deterministic PCG-like generator (xorshift* + Box-Muller).
type rng struct {
	state uint64
	spare float64
	hasSpare bool
}

func newRng(seed uint64) *rng {
	if seed == 0 {
		seed = 1
	}
	return &rng{state: seed}
}

func (r *rng) next() float64 {
	r.state ^= r.state >> 12
	r.state ^= r.state << 25
	r.state ^= r.state >> 27
	v := r.state * uint64(2685821657736338717)
	return float64(v>>11) / float64(uint64(1)<<53)
}

func (r *rng) Normal() float64 {
	if r.hasSpare {
		r.hasSpare = false
		return r.spare
	}
	u1 := r.next()
	u2 := r.next()
	if u1 < 1e-12 {
		u1 = 1e-12
	}
	mag := math.Sqrt(-2.0 * math.Log(u1))
	angle := 2.0 * math.Pi * u2
	r.spare = mag * math.Sin(angle)
	r.hasSpare = true
	return mag * math.Cos(angle)
}
