package command

import (
	"math/rand"
	"sync"
	"time"

	"github.com/openspace-os/openspace-os-core/internal/plugin"
)

// MockAdapter 是用于测试和开发的模拟指令适配器。
//
// 模拟发送延迟和成功率，便于在没有真实卫星链路时进行端到端测试。
type MockAdapter struct {
	delay       time.Duration // 模拟发送延迟
	successRate float64       // 成功率（0-1）
	mu          sync.Mutex
}

// NewMockAdapter 创建 Mock 适配器。
//
// delay 为模拟发送延迟，<=0 时使用默认值 100ms。
// successRate 为成功率（0-1），<0 时使用默认值 1.0。
func NewMockAdapter(delay time.Duration, successRate float64) *MockAdapter {
	if delay <= 0 {
		delay = 100 * time.Millisecond
	}
	if successRate < 0 {
		successRate = 1.0
	}
	return &MockAdapter{
		delay:       delay,
		successRate: successRate,
	}
}

// Name 返回适配器名称。
func (a *MockAdapter) Name() string {
	return "mock"
}

// Send 模拟发送指令：延迟后按成功率返回成功或失败。
func (a *MockAdapter) Send(cmd plugin.Command) (plugin.Ack, error) {
	a.mu.Lock()
	delay := a.delay
	rate := a.successRate
	a.mu.Unlock()

	// 模拟发送延迟
	if delay > 0 {
		time.Sleep(delay)
	}

	// 按成功率决定返回成功或失败
	if rate >= 1.0 || rand.Float64() < rate {
		return plugin.Ack{
			Success: true,
			Message: "mock 适配器发送成功",
		}, nil
	}

	return plugin.Ack{
		Success: false,
		Message: "mock 适配器发送失败（模拟失败）",
	}, nil
}

// SetDelay 设置模拟发送延迟。
func (a *MockAdapter) SetDelay(delay time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.delay = delay
}

// SetSuccessRate 设置成功率（0-1）。
func (a *MockAdapter) SetSuccessRate(rate float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.successRate = rate
}

// 编译期断言：MockAdapter 实现 CommandAdapter 接口。
var _ plugin.CommandAdapter = (*MockAdapter)(nil)
