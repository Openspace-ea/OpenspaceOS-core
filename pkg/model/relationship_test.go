package model

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validRelationship 返回一个所有必填字段均合法的 Relationship。
func validRelationship() Relationship {
	return Relationship{
		RelID:      "rel-001",
		FromNodeID: "sat-001",
		ToNodeID:   "comm-001",
		RelType:    RelBelongsTo,
		CreatedAt:  time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC),
	}
}

// TestRelationshipType_Valid 校验 RelationshipType 枚举有效性。
func TestRelationshipType_Valid(t *testing.T) {
	valid := []RelationshipType{
		RelBelongsTo,
		RelControlledBy,
		RelOwns,
		RelProvidesServiceTo,
		RelExecutedBy,
		RelUsesResource,
		RelMemberOf,
		RelContains,
		RelTargets,
	}
	for _, rt := range valid {
		assert.True(t, rt.Valid(), "%s 应为有效关系类型", rt)
	}
	assert.False(t, RelationshipType("Unknown").Valid(), "未知关系类型应无效")
	assert.False(t, RelationshipType("").Valid(), "空关系类型应无效")
}

// TestRelationshipValidate_OK 校验完整 Relationship 通过校验。
func TestRelationshipValidate_OK(t *testing.T) {
	r := validRelationship()
	assert.NoError(t, r.Validate())
}

// TestRelationshipValidate_RequiredFields 校验缺少必填字段时返回错误。
func TestRelationshipValidate_RequiredFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Relationship)
		wantErr string
	}{
		{"缺少 relId", func(r *Relationship) { r.RelID = "" }, "relId 不能为空"},
		{"缺少 fromNodeId", func(r *Relationship) { r.FromNodeID = "" }, "fromNodeId 不能为空"},
		{"缺少 toNodeId", func(r *Relationship) { r.ToNodeID = "" }, "toNodeId 不能为空"},
		{"未知 relType", func(r *Relationship) { r.RelType = RelationshipType("Unknown") }, "未知的 relType"},
		{"createdAt 为零值", func(r *Relationship) { r.CreatedAt = time.Time{} }, "createdAt 不能为零值"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := validRelationship()
			tc.mutate(&r)
			err := r.Validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestRelationship_JSONRoundTrip 校验 Relationship 的 JSON 序列化/反序列化往返。
func TestRelationship_JSONRoundTrip(t *testing.T) {
	r := validRelationship()
	r.Properties = map[string]any{"weight": 0.8}
	data, err := json.Marshal(r)
	require.NoError(t, err)

	var got Relationship
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, r.RelID, got.RelID)
	assert.Equal(t, r.FromNodeID, got.FromNodeID)
	assert.Equal(t, r.ToNodeID, got.ToNodeID)
	assert.Equal(t, RelBelongsTo, got.RelType)
	assert.Equal(t, 0.8, got.Properties["weight"])
}
