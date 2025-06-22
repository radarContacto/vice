package weather

import (
	"encoding/json"
	"os"
	"path/filepath"

	av "github.com/mmp/vice/pkg/aviation"
	"github.com/mmp/vice/pkg/math"
)

type WindGrid struct {
	Points []windPoint `json:"points"`
}

type windPoint struct {
	Lat    float64     `json:"lat"`
	Lon    float64     `json:"lon"`
	Levels []windLevel `json:"levels"`
}

type windLevel struct {
	Mb float64 `json:"mb"`
	U  float64 `json:"u"`
	V  float64 `json:"v"`
	T  float64 `json:"t"`
	H  float64 `json:"h"`
}

func LoadWindData() (*WindGrid, error) {
	path := filepath.Join("vice", "data", "weather", "wind_data.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var wg WindGrid
	if err := json.Unmarshal(b, &wg); err != nil {
		return nil, err
	}
	return &wg, nil
}

const mpsToNMPerSec = 1.0 / 1852.0
const mpsToKnots = 3600.0 / 1852.0

func (wg *WindGrid) nearest(p math.Point2LL) *windPoint {
	if wg == nil || len(wg.Points) == 0 {
		return nil
	}
	var best *windPoint
	bestDist := 1e9
	for i := range wg.Points {
		dlat := float64(p[1]) - wg.Points[i].Lat
		dlon := float64(p[0]) - wg.Points[i].Lon
		dist := dlat*dlat + dlon*dlon
		if dist < bestDist {
			bestDist = dist
			best = &wg.Points[i]
		}
	}
	return best
}

func windAt(levels []windLevel, altM float64) (float64, float64) {
	if len(levels) == 0 {
		return 0, 0
	}
	lower, upper := levels[0], levels[len(levels)-1]
	for i := 1; i < len(levels); i++ {
		if altM < levels[i].H {
			upper = levels[i]
			lower = levels[i-1]
			break
		}
		lower = levels[i]
	}
	if upper.H == lower.H {
		return lower.U, lower.V
	}
	f := (altM - lower.H) / (upper.H - lower.H)
	u := lower.U + (upper.U-lower.U)*f
	v := lower.V + (upper.V-lower.V)*f
	return u, v
}

func (wg *WindGrid) GetWindVector(p math.Point2LL, alt float32) [2]float32 {
	pt := wg.nearest(p)
	if pt == nil {
		return [2]float32{}
	}
	u, v := windAt(pt.Levels, float64(alt)*0.3048)
	return [2]float32{float32(u * mpsToNMPerSec), float32(v * mpsToNMPerSec)}
}

func (wg *WindGrid) AverageWindVector() [2]float32 {
	if wg == nil || len(wg.Points) == 0 {
		return [2]float32{}
	}
	var su, sv float64
	n := 0
	for _, pt := range wg.Points {
		if len(pt.Levels) == 0 {
			continue
		}
		lvl := pt.Levels[len(pt.Levels)-1]
		su += lvl.U
		sv += lvl.V
		n++
	}
	if n == 0 {
		return [2]float32{}
	}
	return [2]float32{float32(su / float64(n) * mpsToKnots), float32(sv / float64(n) * mpsToKnots)}
}

func (wg *WindGrid) SurfaceWind() av.Wind {
	if wg == nil {
		return av.Wind{}
	}
	avv := wg.AverageWindVector()
	dir := math.NormalizeHeading(math.Degrees(math.Atan2(-avv[0], -avv[1])))
	spd := int(math.Length2f(avv) + 0.5)
	return av.Wind{Direction: int(dir + 0.5), Speed: spd}
}
