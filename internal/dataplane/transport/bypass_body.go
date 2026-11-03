// 本文件解析 JSON/multipart 的路由字段并保留原始载荷，重建时只替换模型及去除网关字段。
// 调用关系与边界：原生出站请求载体、正文/URL 处理和 HTTP 转发；具体参数、返回及副作用见各方法说明。
package transport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
)

// bypassPart 保存 multipart 一个原始分段的头、名称及字节内容。
// ParseBypassBody 按原顺序收集，Payload 重建时仅替换模型和去除网关控制字段，文件内容不做文本转换。
// 只在当前请求内使用，Data 和 Header 不能作为跨请求共享的可变配置。
type bypassPart struct {
	// 原始分段 MIME 头，重建时保留文件名称、类型及扩展头，不包含整个 HTTP 请求头。
	Header textproto.MIMEHeader
	// 表单字段名，用于识别 model 和网关控制字段；文件字段也保留名称。
	Name string
	// 原始分段字节；文件不做文本转换，session_id 控制字段不转发给上游。
	Data []byte
}

// BypassBody 同时保存原生请求的完整字节和可用于路由、会话及计价的字段投影。
// ParseBypassBody 产生，原生执行器消费；JSON 保留原始精度，multipart 保留边界、文件及分段顺序。
// Payload 构建上游载荷时消费网关控制字段，不改写 Raw；字段投影不能替代原始正文进行无损转发。
// 由 ParseBypassBody 构造后在当前请求内只读传递；Raw、Fields、Parts 不得原地修改或跨请求共享。
type BypassBody struct {
	// 客户端原始正文；模型替换或控制字段过滤时构建新的正文，不原地改写观测内容。
	Raw []byte
	// 路由、会话识别与用量检查的字段投影；JSON 数字沿用 float64，精确出站值以 Raw 为准；multipart 非文件字段为字符串。
	Fields map[string]any
	// 解析后的媒体类型，支持 JSON 和 multipart；空 Content-Type 按 JSON 处理。
	Media string
	// multipart 原始边界，重建分段时复用；JSON 时为空。
	Boundary string
	// 按原始顺序保存的分段；网关字段可移除，其余字段和文件保持原协议。
	Parts []bypassPart
}

// ParseBypassBody 提取原生载荷的路由/计价字段，同时保留完整原字节和 multipart 分段。
// 参数 raw 为入口已限制大小的载荷，contentType 为原始媒体类型；返回 BypassBody 或解析错误。
// ServeBypass 创建与转发测量调用；缺少媒体类型按 JSON，JSON 必须为对象，multipart 必须有合法 boundary。
// 重复模型字段拒绝，避免路由投影与真实发送字段不一致；文件内容、头、顺序和边界保留。
// 其他媒体类型只保留原文，无法安全模型重写时由 Payload 报错；纯解析，不关闭请求或访问上游。
func ParseBypassBody(raw []byte, contentType string) (BypassBody, error) {
	b := BypassBody{Raw: raw, Fields: map[string]any{}}
	b.Media, _, _ = mime.ParseMediaType(contentType)
	if b.Media == "" {
		b.Media = "application/json"
	}
	switch b.Media {
	case "application/json":
		if len(bytes.TrimSpace(raw)) > 0 && (json.Unmarshal(raw, &b.Fields) != nil || b.Fields == nil) {
			return b, fmt.Errorf("JSON object required")
		}
	case "multipart/form-data":
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil || params["boundary"] == "" {
			return b, fmt.Errorf("multipart boundary required")
		}
		b.Boundary = params["boundary"]
		reader := multipart.NewReader(bytes.NewReader(raw), b.Boundary)
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return b, fmt.Errorf("invalid multipart body")
			}
			data, err := io.ReadAll(part)
			if err != nil {
				return b, err
			}
			if part.FileName() == "" && part.FormName() != "" {
				if _, exists := b.Fields[part.FormName()]; exists && part.FormName() == "model" {
					return b, fmt.Errorf("duplicate model field")
				}
				b.Fields[part.FormName()] = string(data)
			}
			b.Parts = append(b.Parts, bypassPart{part.Header, part.FormName(), data})
		}
	}
	return b, nil
}

// Payload 构造原生上游正文，替换路由模型并去掉 session_id、disable_fallbacks 等网关控制字段。
// 参数 field 为登记模型字段，model 为实际上游 ID，pathModel 表示模型已编码在路径；返回字节或重建错误。
// serveBypassCreate 在选定部署后调用；路径模型移除正文模型，避免供应商收到两个互相冲突的模型信号。
// 无需改写时直接保留 Raw；JSON 使用 RawMessage 保留大整数与未知扩展，multipart 保留边界、文件字节、头及顺序。
// 未知媒体类型不能安全改写时返回错误；不发送请求，也不把投影 Fields 的简化值替代原始文件或数字。
func (b BypassBody) Payload(field, model string, pathModel bool) ([]byte, error) {
	if field == "" && !pathModel && b.Fields["disable_fallbacks"] == nil && b.Fields["session_id"] == nil {
		return b.Raw, nil
	}
	if pathModel {
		field = "model"
	}
	if b.Media == "application/json" {
		// 用 RawMessage 只替换路由字段，避免 float64 解码后损失大整数精度。
		fields := map[string]json.RawMessage{}
		if len(bytes.TrimSpace(b.Raw)) > 0 {
			if err := json.Unmarshal(b.Raw, &fields); err != nil {
				return nil, err
			}
		}
		// 回退开关属于网关，不能泄露为供应商的未知请求字段。
		delete(fields, "disable_fallbacks")
		delete(fields, "session_id")
		if pathModel {
			delete(fields, field)
		} else if field != "" {
			fields[field], _ = json.Marshal(model)
		}
		return json.Marshal(fields)
	}
	if b.Media == "multipart/form-data" {
		if b.Fields[field] == model && !pathModel && b.Fields["disable_fallbacks"] == nil && b.Fields["session_id"] == nil {
			return b.Raw, nil
		}
		var out bytes.Buffer
		writer := multipart.NewWriter(&out)
		if err := writer.SetBoundary(b.Boundary); err != nil {
			return nil, err
		}
		for _, part := range b.Parts {
			if part.Name == "disable_fallbacks" || part.Name == "session_id" {
				continue
			}
			data := part.Data
			if field != "" && part.Name == field {
				if pathModel {
					continue
				}
				data = []byte(model)
			}
			target, err := writer.CreatePart(part.Header)
			if err != nil {
				return nil, err
			}
			if _, err = target.Write(data); err != nil {
				return nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	return nil, fmt.Errorf("model field requires JSON or multipart body")
}
