package aviation

import (
	"math"
)

// WindLayer represents wind conditions at a specific altitude.
type WindLayer struct {
	AltitudeFt float64 // altitude in feet
	Direction  float64 // wind direction in degrees from North
	SpeedKts   float64 // wind speed in knots
}

// WindProfile represents multiple wind layers.
type WindProfile struct {
	Layers []WindLayer
}

// WindAtAltitude returns interpolated wind conditions for a given altitude.
func (wp *WindProfile) WindAtAltitude(altitudeFt float64) (direction float64, speed float64) {
	layers := wp.Layers
	if len(layers) == 0 {
		return 0, 0
	}

	if altitudeFt <= layers[0].AltitudeFt {
		return layers[0].Direction, layers[0].SpeedKts
	}

	for i := 1; i < len(layers); i++ {
		lower := layers[i-1]
		upper := layers[i]
		if altitudeFt <= upper.AltitudeFt {
			frac := (altitudeFt - lower.AltitudeFt) / (upper.AltitudeFt - lower.AltitudeFt)
			dir := interpolateAngle(lower.Direction, upper.Direction, frac)
			spd := lower.SpeedKts + frac*(upper.SpeedKts-lower.SpeedKts)
			return dir, spd
		}
	}

	last := layers[len(layers)-1]
	return last.Direction, last.SpeedKts
}

// interpolateAngle smoothly interpolates between two angles.
func interpolateAngle(a, b, fraction float64) float64 {
	diff := math.Mod(b-a+540, 360) - 180
	return math.Mod(a+fraction*diff+360, 360)
}
