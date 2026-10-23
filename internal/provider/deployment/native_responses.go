package deployment

import (
	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// PrepareDialogue 在真实候选边界按协议选择执行路径；m 为部署，d 为可信入口标记的请求。
// 返回独立对话及解析错误；Responses 同协议走原生，跨协议走既有转换，未命中账号灰度保持原行为。
func PrepareDialogue(m config.ModelEntry, d llmdialogue.Dialogue) (llmdialogue.Dialogue, error) {
	if !d.DeferredResponses {
		return d, nil
	}
	transport, ok := Execution(m)
	if ok && transport.Protocol == "openai-responses" {
		out, err := llmdialogue.ParseNativeResponses(d.Native)
		out.NativeResponses = err == nil
		return out, err
	}
	return llmdialogue.ParseDialogue("openai-responses", d.Native)
}

// NativeResponsesConfigured 判断目录是否有原生 Responses 传输；models 为目录快照。
// 返回是否需要读取账号灰度配置；空目录或仅跨协议部署不额外查询，无外部副作用。
func NativeResponsesConfigured(models []config.ModelEntry) bool {
	for _, model := range models {
		if transport, ok := Execution(model); ok && transport.Protocol == "openai-responses" {
			return true
		}
	}
	return false
}
