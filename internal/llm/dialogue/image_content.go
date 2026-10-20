package dialogue

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// ContentPart 保存跨协议消息中有序的文本或图片；URL 为远程地址或原始 data URL，FileID 仅能由 Responses 原生通道解释。
// 解析器生成、编码器读取；不下载图片，不保存文件，也不把图片字节伪装成文本或推断模型视觉能力。
type ContentPart struct {
	Type, Text, URL, FileID, Detail string
}

// imageDetail 校验 OpenAI 图片精度；参数为可省略字段，返回原值或明确类型/枚举错误，供图片解析器调用。
func imageDetail(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	s, ok := value.(string)
	if !ok || (s != "auto" && s != "low" && s != "high" && s != "original") {
		return "", fmt.Errorf("invalid image detail")
	}
	return s, nil
}

// validateImageURL 校验 URL 或 Base64 data URL；参数为原始地址，返回输入错误，不发起网络请求或解码图片像素。
// 支持 PNG/JPEG/GIF/WebP；远程内容和模型能力由上游验证，拒绝本机文件等无法安全转发的引用。
func validateImageURL(value string) error {
	if strings.HasPrefix(value, "data:") {
		header, data, ok := strings.Cut(value, ",")
		if !ok || !strings.HasSuffix(header, ";base64") {
			return fmt.Errorf("image data URL requires base64")
		}
		mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
		if !imageMediaType(mime) {
			return fmt.Errorf("unsupported image media type")
		}
		decoded, err := base64.StdEncoding.Strict().DecodeString(data)
		if err != nil || len(decoded) == 0 {
			return fmt.Errorf("invalid image base64 data")
		}
		return nil
	}
	u, err := url.ParseRequestURI(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil {
		return fmt.Errorf("image URL must be HTTP(S) or a base64 data URL")
	}
	return nil
}

// imageMediaType 判断图片 MIME 是否能在三个目标协议之间表达；参数为 MIME，返回许可布尔值，无外部副作用。
func imageMediaType(mime string) bool {
	return mime == "image/png" || mime == "image/jpeg" || mime == "image/gif" || mime == "image/webp"
}

// parseImagePart 将当前协议图片块映射成有序图片项；参数为对象、协议和角色，返回图片或明确输入错误。
// 只接受用户图片；工具输出、文件引用跨协议、音视频均不冒充已经支持，原生 Responses 的 file_id 保留到编码时决定能力。
func parseImagePart(block map[string]any, protocol, role string) (ContentPart, error) {
	part := ContentPart{Type: "image"}
	if role != "user" {
		return part, fmt.Errorf("image content requires user role")
	}
	var err error
	switch protocol {
	case "openai-responses":
		if block["type"] != "input_image" {
			return part, fmt.Errorf("unsupported image content for Responses")
		}
		if err = dialogueFields(block, "type", "image_url", "file_id", "detail"); err != nil {
			return part, err
		}
		part.Detail, err = imageDetail(block["detail"])
		if err != nil {
			return part, err
		}
		if v := block["file_id"]; v != nil {
			var ok bool
			part.FileID, ok = v.(string)
			if !ok || part.FileID == "" {
				return part, fmt.Errorf("image file_id must be nonempty string")
			}
		}
		if v := block["image_url"]; v != nil {
			var ok bool
			part.URL, ok = v.(string)
			if !ok || part.URL == "" {
				return part, fmt.Errorf("image_url must be nonempty string")
			}
		}
		if (part.URL == "") == (part.FileID == "") {
			return part, fmt.Errorf("image requires exactly one image_url or file_id")
		}
	case "openai-chat":
		if block["type"] != "image_url" {
			return part, fmt.Errorf("unsupported image content for Chat")
		}
		if err = dialogueFields(block, "type", "image_url"); err != nil {
			return part, err
		}
		image := dialogueMap(block["image_url"])
		if err = dialogueFields(image, "url", "detail"); err != nil {
			return part, err
		}
		var ok bool
		part.URL, ok = image["url"].(string)
		if !ok || part.URL == "" {
			return part, fmt.Errorf("image_url.url must be nonempty string")
		}
		part.Detail, err = imageDetail(image["detail"])
		if err != nil {
			return part, err
		}
	case "anthropic-messages":
		if block["type"] != "image" {
			return part, fmt.Errorf("unsupported image content for Messages")
		}
		if err = dialogueFields(block, "type", "source"); err != nil {
			return part, err
		}
		source := dialogueMap(block["source"])
		switch source["type"] {
		case "base64":
			if err = dialogueFields(source, "type", "media_type", "data"); err != nil {
				return part, err
			}
			mime, ok := source["media_type"].(string)
			if !ok || !imageMediaType(mime) {
				return part, fmt.Errorf("unsupported image media type")
			}
			data, ok := source["data"].(string)
			if !ok {
				return part, fmt.Errorf("image data must be string")
			}
			part.URL = "data:" + mime + ";base64," + data
		case "url":
			if err = dialogueFields(source, "type", "url"); err != nil {
				return part, err
			}
			var ok bool
			part.URL, ok = source["url"].(string)
			if !ok || !strings.HasPrefix(part.URL, "http") {
				return part, fmt.Errorf("image source URL must be HTTP(S)")
			}
		default:
			return part, fmt.Errorf("unsupported image source type")
		}
	default:
		return part, fmt.Errorf("unsupported image protocol %s", protocol)
	}
	if part.URL != "" {
		err = validateImageURL(part.URL)
	}
	return part, err
}

// messageContent 解析消息的有序文本/图片；参数为内容、协议、角色，返回文本投影与必要的多模态块，纯文本仍沿用既有结构。
// OpenAI 消息和 Messages 图片分支调用；不识别的类型继续报错，不丢弃图片，也不修改原始正文。
func messageContent(value any, protocol, role string) (string, []ContentPart, error) {
	parts, ok := value.([]any)
	if !ok {
		text, err := textContent(value)
		return text, nil, err
	}
	var text strings.Builder
	var content []ContentPart
	hasImage := false
	for _, value := range parts {
		block := dialogueMap(value)
		switch block["type"] {
		case "text", "input_text", "output_text":
			value, ok := block["text"].(string)
			if !ok {
				return "", nil, fmt.Errorf("content text must be string")
			}
			text.WriteString(value)
			content = append(content, ContentPart{Type: "text", Text: value})
		case "image_url", "input_image", "image":
			image, err := parseImagePart(block, protocol, role)
			if err != nil {
				return "", nil, err
			}
			hasImage = true
			content = append(content, image)
		default:
			return "", nil, fmt.Errorf("unsupported content capability %v", block["type"])
		}
	}
	if !hasImage {
		content = nil
	}
	return text.String(), content, nil
}

// encodeImageContent 编码包含图片的有序内容；参数为共享内容与目标协议，返回协议数组或不可表达错误。
// EncodeDialogue 调用；保留 URL/Base64、顺序和 OpenAI detail，不拉取远程地址，不静默丢弃文件引用或精度参数。
func encodeImageContent(content []ContentPart, protocol string) ([]any, error) {
	var out []any
	for _, part := range content {
		if part.Type == "text" {
			kind := "text"
			if protocol == "openai-responses" {
				kind = "input_text"
			}
			out = append(out, map[string]any{"type": kind, "text": part.Text})
			continue
		}
		if part.Type != "image" {
			return nil, fmt.Errorf("unsupported content part %s", part.Type)
		}
		if protocol != "openai-chat" && protocol != "openai-responses" && protocol != "anthropic-messages" {
			return nil, fmt.Errorf("image content requires an implemented image protocol")
		}
		if part.FileID != "" && protocol != "openai-responses" {
			return nil, fmt.Errorf("image file_id requires native Responses")
		}
		switch protocol {
		case "openai-chat":
			image := map[string]any{"url": part.URL}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			out = append(out, map[string]any{"type": "image_url", "image_url": image})
		case "openai-responses":
			image := map[string]any{"type": "input_image"}
			if part.FileID != "" {
				image["file_id"] = part.FileID
			} else {
				image["image_url"] = part.URL
			}
			if part.Detail != "" {
				image["detail"] = part.Detail
			}
			out = append(out, image)
		case "anthropic-messages":
			if part.Detail != "" && part.Detail != "auto" {
				return nil, fmt.Errorf("image detail cannot be expressed by Messages")
			}
			source := map[string]any{"type": "url", "url": part.URL}
			if strings.HasPrefix(part.URL, "data:") {
				header, data, _ := strings.Cut(part.URL, ",")
				source = map[string]any{"type": "base64", "media_type": strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64"), "data": data}
			}
			out = append(out, map[string]any{"type": "image", "source": source})
		}
	}
	return out, nil
}

// ValidateBudgetInputImage复用三协议图片解析供严格预算准入；参数为实际图片块和出站协议，返回格式错误。
// 缓存提示由预算层另行校验；此处只检查图片本身，不访问URL、不修改正文，拒绝服务器文件引用。
func ValidateBudgetInputImage(block map[string]any, protocol string) error {
	copy := make(map[string]any, len(block))
	for key, value := range block {
		if key != "cache_control" {
			copy[key] = value
		}
	}
	part, err := parseImagePart(copy, protocol, "user")
	if err != nil {
		return err
	}
	if part.FileID != "" {
		return fmt.Errorf("strict budget requires explicit image data or URL")
	}
	return nil
}
