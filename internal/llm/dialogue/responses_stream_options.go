package dialogue

import (
	"fmt"
	"strings"
)

// projectResponsesStreamOptions 将原生流选项从共享Chat投影中隔离，返回不可桥接原因或字段类型错误。
// 参数 native 是原生正文、projected 是可修改的共享投影；ParseDialogue调用，不修改native或访问外部服务。
// 原生扩展由上游判定能力，不套用Chat字段白名单；未知选项也保留，跨协议禁止静默删除语义。
func projectResponsesStreamOptions(native, projected map[string]any) (string, error) {
	delete(projected, "stream_options")
	value := native["stream_options"]
	if value == nil {
		return "", nil
	}
	options, ok := value.(map[string]any)
	if !ok {
		return "", fmt.Errorf("stream_options must be an object")
	}
	boundary := ""
	for key, value := range options {
		switch key {
		case "include_usage":
			if _, ok := value.(bool); !ok {
				return "", fmt.Errorf("stream_options.include_usage must be boolean")
			}
		case "reasoning_summary_delivery":
			if s, ok := value.(string); !ok || strings.TrimSpace(s) == "" {
				return "", fmt.Errorf("stream_options.reasoning_summary_delivery must be a nonempty string")
			}
			boundary = "Responses stream_options require native Responses"
		default:
			boundary = "Responses stream_options require native Responses"
		}
	}
	return boundary, nil
}
