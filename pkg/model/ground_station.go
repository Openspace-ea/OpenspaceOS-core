package model

// Location 地理位置信息。
type Location struct {
	// Latitude 纬度 (deg)。
	Latitude float64 `json:"latitude"`
	// Longitude 经度 (deg)。
	Longitude float64 `json:"longitude"`
	// Altitude 海拔 (m)。
	Altitude float64 `json:"altitude"`
}

// GroundStationProperties 地面站节点的特有属性。
type GroundStationProperties struct {
	// Location 地理位置信息。
	Location Location `json:"location"`
	// Capabilities 能力列表。
	Capabilities []string `json:"capabilities"`
}
