package geo

import "math"

const earthRadiusMeters = 6371000.0

func HaversineDistance(lat1, lon1, lat2, lon2 float64) float64 {
	lat1Rad := toRadians(lat1)
	lat2Rad := toRadians(lat2)
	dLat := toRadians(lat2 - lat1)
	dLon := toRadians(lon2 - lon1)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

func toRadians(deg float64) float64 {
	return deg * math.Pi / 180
}
