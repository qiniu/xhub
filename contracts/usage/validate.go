// Package keys 保存管理接口契约；验证工具只用于开发，不注册生产 handler。
package usage

import (
	_ "embed"
	"github.com/sunqirui1987/xhub/contracts/contractcheck"
)

// Document 嵌入唯一契约源，供离线生成类型及真实 HTTP 回归验证。
//
//go:embed openapi.json
var Document []byte

// Validator 复用开发期 HTTP 形状检查；权限与预算仍由真实网关负责。
type Validator = contractcheck.Validator

// NewValidator 加载 raw 契约并校验引用；返回验证器或错误，供测试调用，无网络或生产副作用。
func NewValidator(raw []byte) (*Validator, error) { return contractcheck.NewValidator(raw) }
