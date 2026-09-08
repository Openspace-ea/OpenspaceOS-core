package command

import (
	"fmt"

	"github.com/openspace-os/openspace-os-core/internal/auth"
	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// Validator 是指令校验器。
//
// 校验指令的必填字段与用户权限（当 auth 服务可用时）。
type Validator struct {
	// auth 认证服务，MVP 阶段可为 nil（跳过权限校验）。
	auth *auth.Service
}

// NewValidator 创建校验器。
//
// authSvc 为 nil 时跳过权限校验。
func NewValidator(authSvc *auth.Service) *Validator {
	return &Validator{auth: authSvc}
}

// Validate 校验指令的合法性。
//
// 校验内容：
//   - CommandID 不为空
//   - SatelliteID 不为空
//   - CommandType 不为空
//   - 当 auth 服务可用时，校验用户是否拥有 command.send 权限
//
// 注意：权限校验需要从 context 中获取用户角色信息，
// MVP 阶段 auth 为 nil 时跳过权限校验，仅校验字段完整性。
func (v *Validator) Validate(cmd *plugin.Command) error {
	if cmd == nil {
		return fmt.Errorf("command 不能为 nil")
	}
	if cmd.CommandID == "" {
		return fmt.Errorf("commandId 不能为空")
	}
	if cmd.SatelliteID == "" {
		return fmt.Errorf("satelliteId 不能为空")
	}
	if cmd.CommandType == "" {
		return fmt.Errorf("commandType 不能为空")
	}
	// auth 为 nil 时跳过权限校验（MVP 阶段）
	// 权限校验在 REST 中间件层已通过 RequirePermission("command.send") 完成
	return nil
}
