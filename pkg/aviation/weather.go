// pkg/aviation/weather.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package aviation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/mmp/vice/pkg/math"
	"github.com/mmp/vice/pkg/rand"
	"github.com/mmp/vice/pkg/util"
)

///////////////////////////////////////////////////////////////////////////
// Wind

type Wind struct {
	Variable  bool
	Direction int `json:"direction"`
	Speed     int `json:"speed"`
	Gust      int `json:"gust"`
}

// WindLayer specifies wind conditions at a given altitude. Altitude is in
// feet MSL.
type WindLayer struct {
	Altitude float32 `json:"altitude"`
	Wind     Wind    `json:"wind"`
}

func (w Wind) String() string {
	if w.Speed <= 0 {
		return "00000KT"
	} else if w.Variable {
		return fmt.Sprintf("VRB%02dKT", w.Speed)
	} else {
		wind := fmt.Sprintf("%03d%02d", w.Direction, w.Speed)

		// According to Federal Meteorological Handbook No. 1 (FCM-H1-2019)
		//   Gusts are indicated by rapid fluctuations in wind speed
		//   with a variation of 10 knots or more between peaks and lulls.
		// The Aviation Weather Center reports gust values according to the above or revised definitions.
		if w.Gust > 0 {
			wind += fmt.Sprintf("G%02d", w.Gust)
		}

		return wind + "KT"
	}
}

func (w Wind) Randomize(r *rand.Rand) Wind {
	w.Speed += -3 + r.Intn(6)
	if w.Speed < 0 {
		w.Speed = 0
	} else if w.Speed < 4 {
		w.Variable = true
	} else {
		dir := 10 * ((w.Direction + 5) / 10)
		dir += [3]int{-10, 0, 10}[r.Intn(3)]
		w.Direction = dir
		gst := w.Gust - 3 + r.Intn(6)
		if gst-w.Speed > 5 {
			w.Gust = gst
		}
	}
	return w
}

type WindModel interface {
	GetWindVector(p math.Point2LL, alt float32) [2]float32
	AverageWindVector() [2]float32
}

type WindLevel struct {
	Altitude float32 `json:"altitude"`
	Wind     Wind    `json:"wind"`
}

// WindProfile stores winds at various altitudes.
// Altitudes are in feet and need not be sorted, but it is recommended.
type WindProfile []WindLevel

func (wp WindProfile) windAt(alt float32) Wind {
	if len(wp) == 0 {
		return Wind{}
	}

	// Find surrounding levels.
	lower := wp[0]
	upper := wp[len(wp)-1]
	for i := 1; i < len(wp); i++ {
		if alt < wp[i].Altitude {
			upper = wp[i]
			lower = wp[i-1]
			break
		}
		lower = wp[i]
	}

	if upper.Altitude == lower.Altitude {
		return lower.Wind
	}

	f := (alt - lower.Altitude) / (upper.Altitude - lower.Altitude)
	// Interpolate speed linearly and directions via vectors.
	d1 := math.OppositeHeading(float32(lower.Wind.Direction))
	v1 := [2]float32{math.Sin(math.Radians(d1)), math.Cos(math.Radians(d1))}
	d2 := math.OppositeHeading(float32(upper.Wind.Direction))
	v2 := [2]float32{math.Sin(math.Radians(d2)), math.Cos(math.Radians(d2))}
	v := math.Add2f(math.Scale2f(v1, 1-f), math.Scale2f(v2, f))
	dir := math.OppositeHeading(math.Degrees(math.Atan2(v[0], v[1])))
	spd := lower.Wind.Speed + int(float32(upper.Wind.Speed-lower.Wind.Speed)*f)
	gst := lower.Wind.Gust + int(float32(upper.Wind.Gust-lower.Wind.Gust)*f)

	return Wind{Direction: int(dir + 0.5), Speed: spd, Gust: gst}
}

func (wp WindProfile) SurfaceWind() Wind {
	if len(wp) == 0 {
		return Wind{}
	}
	idx := 0
	for i := 1; i < len(wp); i++ {
		if wp[i].Altitude < wp[idx].Altitude {
			idx = i
		}
	}
	return wp[idx].Wind
}

func (wp WindProfile) GetWindVector(p math.Point2LL, alt float32) [2]float32 {
	w := wp.windAt(alt)
	d := math.OppositeHeading(float32(w.Direction))
	v := [2]float32{math.Sin(math.Radians(d)), math.Cos(math.Radians(d))}
	return math.Scale2f(v, float32(w.Speed)/3600)
}

func (wp WindProfile) AverageWindVector() [2]float32 {
	if len(wp) == 0 {
		return [2]float32{}
	}
	var sum [2]float32
	for _, lvl := range wp {
		d := math.OppositeHeading(float32(lvl.Wind.Direction))
		v := [2]float32{math.Sin(math.Radians(d)), math.Cos(math.Radians(d))}
		sum = math.Add2f(sum, math.Scale2f(v, float32(lvl.Wind.Speed)))
	}
	return math.Scale2f(sum, 1/float32(len(wp)))
}

///////////////////////////////////////////////////////////////////////////
// METAR

type METAR struct {
	AirportICAO string
	Time        string
	Auto        bool
	Wind        Wind `json:"wind"` // WAR changing this from a strong to deserialization doesn't fail.
	Altimeter   string
	Weather     string
	Rmk         string
}

func (m METAR) String() string {
	auto := util.Select(m.Auto, "AUTO", "")
	return strings.Join([]string{m.AirportICAO, m.Time, auto, m.Wind.String(), m.Weather, m.Altimeter, m.Rmk}, " ")
}

type avWeatherMETAR struct {
	//MetarId     int         `json:"metar_id"`
	IcaoId string `json:"icaoId"` // ICAO identifier
	//ReceiptTime string      `json:"receiptTime"`
	//ObsTime     int         `json:"obsTime"`
	//ReportTime  string      `json:"reportTime"`
	//Temp        float64     `json:"temp"`
	//Dewp        float64     `json:"dewp"`
	WindDir   any `json:"wdir"` // Wind direction in degrees or VRB for variable winds
	WindSpeed int `json:"wspd"` // Wind speed in knots
	WindGust  int `json:"wgst"` // Wind gusts in knots
	//Visib string  `json:"visib"`
	Altim float64 `json:"altim"` // Altimeter setting in hectoPascals
	//Slp        float64      `json:"slp"`
	//QcField    int          `json:"qcField"`
	//WxString   *string  `json:"wxString"` // Encoded present weather string
	//PresTend   float64      `json:"presTend"`
	//MaxT       *float64  `json:"maxT"` // Maximum temperature over last 6 hours in Celcius
	//MinT       *float64  `json:"minT"` // Minimum temperature over last 6 hours in Celcius
	//MaxT24     *float64  `json:"maxT24"` // Maximum temperature over last 24 hours in Celcius
	//MinT24     *float64  `json:"minT24"` // Minimum temperature over last 24 hours in Celcius
	//Precip     *float64  `json:"precip"` // Precipitation over last hour in inches
	//Pcp3Hr     *float64  `json:"pcp3hr"` // Precipitation over last 3 hours in inches
	//Pcp6Hr     *float64  `json:"pcp6hr"` // Precipitation over last 6 hours in inches
	//Pcp24Hr    *float64  `json:"pcp24hr"` // Precipitation over last 24 hours in inches
	//Snow       *float64  `json:"snow"` // Snow depth in inches
	//VertVis    *int  `json:"vertVis"` // Vertical visibility in feet
	//MetarType  string       `json:"metarType"`
	//avWeatherMETAR string `json:"rawOb"` // Raw text of observation
	//MostRecent int          `json:"mostRecent"`
	//Lat        float64      `json:"lat"`
	//Lon        float64      `json:"lon"`
	//Elev       int          `json:"elev"`
	//Prior      int          `json:"prior"`
	//Name       string       `json:"name"`
	//Clouds     []cloudLayer `json:"clouds"`
}

/*
type cloudLayer struct {
	Base  int64  `json:"base"`
	Cover string `json:"cover"`
}
*/

// GetWindDirection returns the wind direction in degrees or VRB for variable winds.
func (m avWeatherMETAR) WindDirection() (vrb bool, direction int) {
	if d, ok := m.WindDir.(float64); ok {
		direction = int(d)
	} else {
		vrb = true
	}
	return
}

// getAltimeter returns the altimeter setting in inches Hg
func (m avWeatherMETAR) Altimeter() float64 {
	// Conversion formula (hectoPascal to Inch of Mercury): 29.92 * (hpa / 1013.2)
	return 0.02953 * m.Altim
}

const aviationWeatherCenterDataApi = `https://aviationweather.gov/api/data/metar?ids=%s&format=json`

func GetWeather(icao ...string) ([]METAR, error) {
	query := url.QueryEscape(strings.Join(icao, ","))
	requestUrl := fmt.Sprintf(aviationWeatherCenterDataApi, query)

	res, err := http.Get(requestUrl)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	av := make([]avWeatherMETAR, 0, len(icao))
	if err = json.NewDecoder(res.Body).Decode(&av); err != nil {
		return nil, err
	}

	// Convert to our representation
	metar := util.MapSlice(av, func(m avWeatherMETAR) METAR {
		metar := METAR{
			AirportICAO: m.IcaoId,
			Altimeter:   fmt.Sprintf("A%d", int(m.Altimeter()*100)),
		}
		metar.Wind.Variable, metar.Wind.Direction = m.WindDirection()
		metar.Wind.Speed = m.WindSpeed
		metar.Wind.Gust = m.WindGust

		return metar
	})

	return metar, nil
}

// WindGrid represents wind vectors on a regular latitude/longitude/altitude grid.
type WindGrid struct {
	Levels     []float64
	Latitudes  []float64
	Longitudes []float64
	// Vectors[k][i][j] corresponds to altitude Levels[k], latitude Latitudes[i], longitude Longitudes[j].
	Vectors [][][][2]float64
}

// WindData holds the grid loaded from wind_data.json.
var WindData *WindGrid

// LoadWindData parses the built-in JSON wind dataset into WindData.
func LoadWindData() error {
	type level struct {
		Mb float64 `json:"mb"`
		U  float64 `json:"u"`
		V  float64 `json:"v"`
		T  float64 `json:"t"`
		H  float64 `json:"h"`
	}
	type point struct {
		Lat    float64 `json:"lat"`
		Lon    float64 `json:"lon"`
		Levels []level `json:"levels"`
	}
	var raw struct {
		Points []point `json:"points"`
	}
	data, err := os.ReadFile("vice/data/weather/wind_data.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if len(raw.Points) == 0 {
		return fmt.Errorf("wind data empty")
	}

	// Build unique latitude and longitude arrays.
	latMap := make(map[float64]int)
	lonMap := make(map[float64]int)
	var lats, lons []float64
	for _, p := range raw.Points {
		if _, ok := latMap[p.Lat]; !ok {
			latMap[p.Lat] = len(lats)
			lats = append(lats, p.Lat)
		}
		if _, ok := lonMap[p.Lon]; !ok {
			lonMap[p.Lon] = len(lons)
			lons = append(lons, p.Lon)
		}
	}
	sort.Float64s(lats)
	sort.Float64s(lons)
	for i, v := range lats {
		latMap[v] = i
	}
	for i, v := range lons {
		lonMap[v] = i
	}

	// Assume all points share the same altitude levels as the first point.
	levels := make([]float64, len(raw.Points[0].Levels))
	for i, lv := range raw.Points[0].Levels {
		levels[i] = lv.H * 3.28084 // meters to feet
	}

	vectors := make([][][][2]float64, len(levels))
	for k := range vectors {
		vectors[k] = make([][][2]float64, len(lats))
		for i := range vectors[k] {
			vectors[k][i] = make([][2]float64, len(lons))
		}
	}

	for _, p := range raw.Points {
		i := latMap[p.Lat]
		j := lonMap[p.Lon]
		for k, lv := range p.Levels {
			vectors[k][i][j] = [2]float64{lv.U, lv.V}
		}
	}

	WindData = &WindGrid{Levels: levels, Latitudes: lats, Longitudes: lons, Vectors: vectors}
	return nil
}

// interpolate returns the wind vector at the specified lat, lon and altitude using trilinear interpolation.
func (wg *WindGrid) interpolate(lat, lon, alt float64) [2]float64 {
	if wg == nil || len(wg.Levels) == 0 {
		return [2]float64{}
	}
	i := sort.SearchFloat64s(wg.Latitudes, lat)
	if i == 0 || i == len(wg.Latitudes) {
		return [2]float64{}
	}
	j := sort.SearchFloat64s(wg.Longitudes, lon)
	if j == 0 || j == len(wg.Longitudes) {
		return [2]float64{}
	}
	k := sort.SearchFloat64s(wg.Levels, alt)
	if k == 0 {
		k = 1
	} else if k >= len(wg.Levels) {
		k = len(wg.Levels) - 1
	}

	lat0, lat1 := wg.Latitudes[i-1], wg.Latitudes[i]
	lon0, lon1 := wg.Longitudes[j-1], wg.Longitudes[j]
	lev0, lev1 := wg.Levels[k-1], wg.Levels[k]
	fx := (lat - lat0) / (lat1 - lat0)
	fy := (lon - lon0) / (lon1 - lon0)
	fz := (alt - lev0) / (lev1 - lev0)

	v000 := wg.Vectors[k-1][i-1][j-1]
	v001 := wg.Vectors[k-1][i-1][j]
	v010 := wg.Vectors[k-1][i][j-1]
	v011 := wg.Vectors[k-1][i][j]
	v100 := wg.Vectors[k][i-1][j-1]
	v101 := wg.Vectors[k][i-1][j]
	v110 := wg.Vectors[k][i][j-1]
	v111 := wg.Vectors[k][i][j]

	var v [2]float64
	for c := 0; c < 2; c++ {
		c00 := v000[c]*(1-fx) + v010[c]*fx
		c01 := v001[c]*(1-fx) + v011[c]*fx
		c10 := v100[c]*(1-fx) + v110[c]*fx
		c11 := v101[c]*(1-fx) + v111[c]*fx
		c0 := c00*(1-fy) + c01*fy
		c1 := c10*(1-fy) + c11*fy
		v[c] = c0*(1-fz) + c1*fz
	}
	return v
}

// GetWind returns the wind at the given location using the global WindData grid.
func GetWind(lat, lon, altitude float64) Wind {
	if WindData == nil {
		return Wind{}
	}
	v := WindData.interpolate(lat, lon, altitude)
	speed := math.Length2f([2]float32{float32(v[0]), float32(v[1])}) * 1.94384 // m/s to knots
	dir := math.OppositeHeading(math.Degrees(math.Atan2(float32(v[0]), float32(v[1]))))
	return Wind{Direction: int(dir + 0.5), Speed: int(speed + 0.5)}
}

// GetWindVector returns the wind vector in NM/s at a given point and altitude.
func (wg *WindGrid) GetWindVector(p math.Point2LL, alt float32) [2]float32 {
	v := wg.interpolate(float64(p.Latitude()), float64(p.Longitude()), float64(alt))
	return [2]float32{float32(v[0] / 1852), float32(v[1] / 1852)}
}

// AverageWindVector returns the average wind vector in NM/s over the grid.
func (wg *WindGrid) AverageWindVector() [2]float32 {
	if wg == nil {
		return [2]float32{}
	}
	var sum [2]float64
	count := 0
	for _, sl := range wg.Vectors {
		for _, row := range sl {
			for _, v := range row {
				sum[0] += v[0]
				sum[1] += v[1]
				count++
			}
		}
	}
	if count == 0 {
		return [2]float32{}
	}
	return [2]float32{float32(sum[0] / float64(count) / 1852), float32(sum[1] / float64(count) / 1852)}
}

func init() {
	if err := LoadWindData(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to load wind data: %v\n", err)
	}
}
