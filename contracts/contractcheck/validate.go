// Package contractcheck 提供开发期 HTTP 契约验证；不导入生产网关，也不替代业务权限验证。
package contractcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// Validator 用于回归测试验证 HTTP 形状；router 来自已校验契约，不安装到网关。
type Validator struct{ router routers.Router }

// NewValidator 加载 raw 中的 OpenAPI 并检查引用与结构，供测试调用；非法文档返回错误，无外部网络访问或持久化副作用。
func NewValidator(raw []byte) (*Validator, error) {
	doc, err := openapi3.NewLoader().LoadFromData(raw)
	if err != nil {
		return nil, err
	}
	if err = doc.Validate(context.Background()); err != nil {
		return nil, err
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		return nil, err
	}
	return &Validator{router: router}, nil
}

// input 根据 req 定位已声明的操作，未知路径或方法返回错误；供请求和响应验证共用，无业务权限判断。
func (v *Validator) input(req *http.Request) (*openapi3filter.RequestValidationInput, error) {
	route, params, err := v.router.FindRoute(req)
	if err != nil {
		return nil, fmt.Errorf("contract route: %w", err)
	}
	return &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: &openapi3filter.Options{
		// 鉴权必须由真实网关验证，避免用固定测试密钥模拟管理员成功。这里仅验证 HTTP 数据形状。
		AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
	}}, nil
}

// Request 验证 req 的路径参数与 JSON 形状；供真实 HTTP 前的合法输入断言调用，畸形输入应断言本方法失败后再发送网关。
// 返回契约错误；库会读取并恢复 Body，不执行请求，不替代网关鉴权或业务校验。
func (v *Validator) Request(req *http.Request) error {
	input, err := v.input(req)
	if err != nil {
		return err
	}
	// kin-openapi 默认 JSON 解码器可忽略尾随第二个值；受支持的契约输入只声明单个对象，先防止这类假通过。
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(raw))
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			req.Body = nil
			req.ContentLength = 0
		} else if !json.Valid(raw) || bytes.TrimSpace(raw)[0] != '{' {
			// 这是契约支持范围的检查，不推断旧 handler 对未声明正文的行为；真实错误另由回归验证。
			return fmt.Errorf("contract request: expected a single JSON object")
		}
	}
	return openapi3filter.ValidateRequest(req.Context(), input)
}

// Response 验证 req 对应的实际 status/header/body，供真实 HTTP 返回后调用；不验证请求体，以便检查非法请求的错误响应。
// 未声明状态码、媒体类型或响应形状均返回错误；不把结构合法当作业务成功，调用方还须断言状态与持久化结果。
func (v *Validator) Response(req *http.Request, status int, header http.Header, body []byte) error {
	input, err := v.input(req)
	if err != nil {
		return err
	}
	// 库对 301/304/307/308 提前成功返回，IncludeResponseStatus 无法拦截；先核对操作声明。
	responses := input.Route.Operation.Responses
	if responses.Status(status) == nil && responses.Default() == nil {
		return fmt.Errorf("contract response: undeclared status %d", status)
	}
	if !json.Valid(body) {
		return fmt.Errorf("contract response: invalid or trailing JSON")
	}
	return openapi3filter.ValidateResponse(req.Context(), (&openapi3filter.ResponseValidationInput{
		RequestValidationInput: input, Status: status, Header: header, Options: &openapi3filter.Options{IncludeResponseStatus: true},
	}).SetBodyBytes(body))
}
