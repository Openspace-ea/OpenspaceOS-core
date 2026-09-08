package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validNode 返回一个所有必填字段均合法的 Node，供测试用例裁剪使用。
func validNode() Node {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	return Node{
		NodeID:           "sat-001",
		NodeType:         NodeTypeSatellite,
		Name:             "测试卫星",
		Status:           "Active",
		OwnerCommunityID: "comm-001",
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// TestNodeValidate_OK 校验完整 Node 通过校验。
func TestNodeValidate_OK(t *testing.T) {
	n := validNode()
	assert.NoError(t, n.Validate())
}

// TestNodeValidate_RequiredFields 校验缺少任一必填字段时返回错误。
func TestNodeValidate_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Node)
		wantErr string
	}{
		{"缺少 nodeId", func(n *Node) { n.NodeID = "" }, "nodeId 不能为空"},
		{"缺少 nodeType", func(n *Node) { n.NodeType = "" }, "nodeType 不能为空"},
		{"未知 nodeType", func(n *Node) { n.NodeType = NodeType("Unknown") }, "未知的 nodeType"},
		{"缺少 name", func(n *Node) { n.Name = "" }, "name 不能为空"},
		{"缺少 status", func(n *Node) { n.Status = "" }, "status 不能为空"},
		{"缺少 ownerCommunityId", func(n *Node) { n.OwnerCommunityID = "" }, "ownerCommunityId 不能为空"},
		{"createdAt 为零值", func(n *Node) { n.CreatedAt = time.Time{} }, "createdAt 不能为零值"},
		{"updatedAt 为零值", func(n *Node) { n.UpdatedAt = time.Time{} }, "updatedAt 不能为零值"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := validNode()
			tc.mutate(&n)
			err := n.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestNodeType_Valid 校验 NodeType 枚举有效性。
func TestNodeType_Valid(t *testing.T) {
	valid := []NodeType{
		NodeTypeSatellite,
		NodeTypeGroundStation,
		NodeTypeTask,
		NodeTypeCommunity,
		NodeTypeMission,
		NodeTypeAntenna,
		NodeTypePayload,
		NodeTypeTelemetry,
		NodeTypeCommand,
		NodeTypeAlarm,
	}
	for _, nt := range valid {
		assert.True(t, nt.Valid(), "%s 应为有效类型", nt)
	}
	assert.False(t, NodeType("Unknown").Valid(), "未知类型应无效")
	assert.False(t, NodeType("").Valid(), "空类型应无效")
}

// TestSatelliteProperties_Serialization 校验 SatelliteProperties 包含 OrbitElements 的序列化/反序列化。
func TestSatelliteProperties_Serialization(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sp := SatelliteProperties{
		NoradID: "25544",
		Orbit: OrbitElements{
			NoradID:         "25544",
			Inclination:     51.6,
			RAAN:            120.5,
			Eccentricity:    0.0006,
			ArgPerigee:      0.0,
			MeanAnomaly:     0.0,
			MeanMotion:      15.5,
			Period:          92.9,
			PerigeeAltitude: 418.0,
			ApogeeAltitude:  423.0,
			SemiMajorAxis:   6786.0,
			Epoch:           epoch,
		},
		Owner:        "ESA",
		Capabilities: []string{"imaging", "comm"},
	}

	data, err := json.Marshal(sp)
	require.NoError(t, err)

	var got SatelliteProperties
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, sp, got)
	assert.Equal(t, 51.6, got.Orbit.Inclination)
	assert.Equal(t, "25544", got.Orbit.NoradID)
	assert.True(t, got.Orbit.Epoch.Equal(epoch))
	assert.Equal(t, []string{"imaging", "comm"}, got.Capabilities)
}

// TestNode_JSONRoundTrip 校验 Node 含 Properties 的 JSON 往返。
func TestNode_JSONRoundTrip(t *testing.T) {
	n := validNode()
	n.Properties = map[string]any{
		"noradId": "25544",
		"owner":   "ESA",
	}
	data, err := json.Marshal(n)
	require.NoError(t, err)

	var got Node
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, n.NodeID, got.NodeID)
	assert.Equal(t, NodeTypeSatellite, got.NodeType)
	assert.Equal(t, "Active", got.Status)
	assert.Equal(t, "ESA", got.Properties["owner"])
}

// TestNode_OmitEmptyFields 校验 omitempty 字段在为零值时不输出。
func TestNode_OmitEmptyFields(t *testing.T) {
	n := validNode()
	data, err := json.Marshal(n)
	require.NoError(t, err)
	// ShardKey 与 FederationID 为空，且带 omitempty，不应出现在 JSON 中
	assert.NotContains(t, string(data), "shardKey")
	assert.NotContains(t, string(data), "federationId")
}
