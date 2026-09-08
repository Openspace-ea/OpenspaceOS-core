package auth

// DefaultRoles 是系统内置的默认角色与权限定义。
//
// 包含三个角色：
//   - admin：超级管理员，拥有所有权限
//   - operator：操作员，可读写节点与关系、发布订阅事件、发送指令
//   - viewer：只读用户，仅可查询节点、关系图、订阅与回放事件
var DefaultRoles = map[string]Role{
	"admin": {
		Name: "admin",
		Permissions: []string{
			"node.create", "node.read", "node.update", "node.delete",
			"relationship.create", "relationship.delete", "graph.query",
			"event.publish", "event.subscribe", "event.replay",
			"command.send", "plugin.manage", "user.manage",
		},
	},
	"operator": {
		Name: "operator",
		Permissions: []string{
			"node.create", "node.read", "node.update",
			"relationship.create", "graph.query",
			"event.publish", "event.subscribe", "event.replay",
			"command.send",
		},
	},
	"viewer": {
		Name: "viewer",
		Permissions: []string{
			"node.read", "graph.query",
			"event.subscribe", "event.replay",
		},
	},
}

// RoleAdmin 是 admin 角色名常量。
const RoleAdmin = "admin"

// RoleOperator 是 operator 角色名常量。
const RoleOperator = "operator"

// HasPermission 检查给定角色列表是否拥有指定权限。
//
// 只要其中任意一个角色包含该权限即视为拥有。
// 未知角色会被忽略（其权限为空）。
func HasPermission(roles []string, permission string) bool {
	for _, roleName := range roles {
		role, ok := DefaultRoles[roleName]
		if !ok {
			continue
		}
		for _, p := range role.Permissions {
			if p == permission {
				return true
			}
		}
	}
	return false
}
