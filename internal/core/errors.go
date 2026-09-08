package core

import "errors"

// 事件总线与存储相关的共享错误。
var (
	// errNilEvent 表示传入的事件为 nil。
	errNilEvent = errors.New("事件不能为 nil")
	// errStoreClosed 表示存储已关闭。
	errStoreClosed = errors.New("事件存储已关闭")
	// errBusClosed 表示事件总线已关闭。
	errBusClosed = errors.New("事件总线已关闭")
)
