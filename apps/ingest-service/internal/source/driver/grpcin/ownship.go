package grpcin

import (
	"math"
	"sort"
	"time"

	"github.com/sentinel/go-common/geo"
)

// ownshipWindow is how much of a mobile emplacement's track is kept, behind
// its newest fix.
const ownshipWindow = 60 * time.Second

// maxFixes bounds a track: 20 Hz over the window.
const maxFixes = 1200

// fix is one of a mobile emplacement's reports of itself.
type fix struct {
	at      time.Time
	pos     geo.LLA
	heading *float64
	vel     *[3]float64 // north, east, up in m/s
}

// track is a mobile emplacement's recent fixes, in time order.
type track struct{ fixes []fix }

// add puts f in time order (a fix at a time already held replaces it) and
// forgets fixes older than the window behind the newest, and the oldest
// past maxFixes.
func (t *track) add(f fix) {
	i := sort.Search(len(t.fixes), func(i int) bool { return !t.fixes[i].at.Before(f.at) })
	switch {
	case i < len(t.fixes) && t.fixes[i].at.Equal(f.at):
		t.fixes[i] = f
	default:
		t.fixes = append(t.fixes, fix{})
		copy(t.fixes[i+1:], t.fixes[i:])
		t.fixes[i] = f
	}
	cut := t.fixes[len(t.fixes)-1].at.Add(-ownshipWindow)
	k := sort.Search(len(t.fixes), func(i int) bool { return !t.fixes[i].at.Before(cut) })
	t.fixes = t.fixes[k:]
	if over := len(t.fixes) - maxFixes; over > 0 {
		t.fixes = append([]fix(nil), t.fixes[over:]...)
	}
}

// at is the platform's place and heading at ts: interpolated between the
// fixes around it, or carried on from the newest by at most tolerance (along
// its velocity when it has one). ok is false before the first fix and past
// the tolerance.
func (t *track) at(ts time.Time, tolerance time.Duration) (origin, bool) {
	n := len(t.fixes)
	if n == 0 || ts.Before(t.fixes[0].at) {
		return origin{}, false
	}
	last := t.fixes[n-1]
	if !ts.Before(last.at) {
		dt := ts.Sub(last.at)
		if dt > tolerance {
			return origin{}, false
		}
		p := last.pos
		if last.vel != nil && dt > 0 {
			s := dt.Seconds()
			p = geo.FromECEF(geo.ECEFOf(last.pos, geo.ENU{E: last.vel[1] * s, N: last.vel[0] * s, U: last.vel[2] * s}))
		}
		return origin{at: p, heading: last.heading}, true
	}
	i := sort.Search(n, func(i int) bool { return t.fixes[i].at.After(ts) })
	a, b := t.fixes[i-1], t.fixes[i]
	f := float64(ts.Sub(a.at)) / float64(b.at.Sub(a.at))
	return origin{
		at: geo.LLA{
			Lat: a.pos.Lat + (b.pos.Lat-a.pos.Lat)*f,
			Lon: wrap180(a.pos.Lon + shortest(a.pos.Lon, b.pos.Lon)*f),
			Alt: a.pos.Alt + (b.pos.Alt-a.pos.Alt)*f,
		},
		heading: lerpHeading(a.heading, b.heading, f),
	}, true
}

// shortest is the signed smallest turn from a to b, in degrees.
func shortest(a, b float64) float64 { return math.Mod(b-a+540, 360) - 180 }

func wrap180(lon float64) float64 { return math.Mod(math.Mod(lon+180, 360)+360, 360) - 180 }

func lerpHeading(a, b *float64, f float64) *float64 {
	switch {
	case a == nil && b == nil:
		return nil
	case a == nil:
		return b
	case b == nil:
		return a
	}
	h := math.Mod(math.Mod(*a+shortest(*a, *b)*f, 360)+360, 360)
	return &h
}
