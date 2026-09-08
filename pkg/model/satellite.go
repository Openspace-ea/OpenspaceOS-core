package model

import "time"

// OrbitElements 轨道根数结构，参考 TLE 标准。
type OrbitElements struct {
	// NoradID NORAD 编号。
	NoradID string `json:"noradId"`
	// Inclination 倾角 (deg)。
	Inclination float64 `json:"inclination"`
	// RAAN 升交点赤经 (deg)。
	RAAN float64 `json:"raan"`
	// Eccentricity 偏心率。
	Eccentricity float64 `json:"eccentricity"`
	// ArgPerigee 近地点幅角 (deg)。
	ArgPerigee float64 `json:"argPerigee"`
	// MeanAnomaly 平近点角 (deg)。
	MeanAnomaly float64 `json:"meanAnomaly"`
	// MeanMotion 平均运动 (rev/day)。
	MeanMotion float64 `json:"meanMotion"`
	// Period 周期 (min)。
	Period float64 `json:"period"`
	// PerigeeAltitude 近地点高度 (km)。
	PerigeeAltitude float64 `json:"perigeeAltitude"`
	// ApogeeAltitude 远地点高度 (km)。
	ApogeeAltitude float64 `json:"apogeeAltitude"`
	// SemiMajorAxis 半长轴 (km)。
	SemiMajorAxis float64 `json:"semiMajorAxis"`
	// Epoch 历元时刻。
	Epoch time.Time `json:"epoch"`
}

// SatelliteProperties 卫星节点的特有属性。
type SatelliteProperties struct {
	// NoradID NORAD 编号。
	NoradID string `json:"noradId"`
	// Orbit 轨道根数。
	Orbit OrbitElements `json:"orbit"`
	// Owner 所属方。
	Owner string `json:"owner"`
	// Capabilities 能力列表。
	Capabilities []string `json:"capabilities"`
}
