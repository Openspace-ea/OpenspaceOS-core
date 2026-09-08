package grpc

import (
	"encoding/json"

	"google.golang.org/grpc/encoding"
)

// codecName 是自定义 gRPC 编解码器的名称。
// 客户端通过 CallContentSubtype("aos") 指定使用此编解码器，
// 服务端根据请求的 content-subtype 自动选择对应的编解码器。
const codecName = "aos"

// jsonCodec 基于 JSON 的 gRPC 编解码器实现。
//
// 由于本包中的消息类型为手动编写的 Go 结构体（非 protoc 生成），
// 不实现 proto.Message 接口，因此无法使用默认的 proto 编解码器。
// 此编解码器使用 encoding/json 进行序列化/反序列化。
type jsonCodec struct{}

// Marshal 将消息序列化为 JSON 字节
func (jsonCodec) Marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

// Unmarshal 从 JSON 字节反序列化消息
func (jsonCodec) Unmarshal(data []byte, v any) error {
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

// Name 返回编解码器名称
func (jsonCodec) Name() string {
	return codecName
}

func init() {
	// 注册自定义编解码器，客户端和服务端均通过此名称查找
	encoding.RegisterCodec(jsonCodec{})
}
