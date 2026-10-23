package deployment

import (
	"fmt"
	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// ValidateNativeResponses 校验部署级灰度模式；参数 m 为部署，返回配置错误或 nil。
// 保存和执行共用；缺省 legacy，observe/enabled 只允许登记的原生 Responses 传输，防止配置误启用转换路径。
func ValidateNativeResponses(m config.ModelEntry) error {
	value, exists := m.ModelInfo["native_responses_mode"]
	if !exists {
		return nil
	}
	mode, ok := value.(string)
	if !ok || (mode != "legacy" && mode != "observe" && mode != "enabled") {
		return fmt.Errorf("invalid model_info.native_responses_mode")
	}
	if mode != "legacy" {
		t, ok := Execution(m)
		if !ok || t.Protocol != "openai-responses" {
			return fmt.Errorf("native_responses_mode requires a native Responses transport")
		}
	}
	return nil
}

// PrepareDialogue 在实际候选边界选择原生校验或既有转换解析；参数 m 为候选，d 为请求快照。
// 返回独立的执行对话及错误，候选过滤与出站构造共用；未命中部署开关时严格沿用旧解析，不自动跨协议丢字段。
func PrepareDialogue(m config.ModelEntry, d llmdialogue.Dialogue) (llmdialogue.Dialogue, error) {
	if err := ValidateNativeResponses(m); err != nil {
		return llmdialogue.Dialogue{}, err
	}
	if !d.DeferredResponses {
		return d, nil
	}
	if m.ModelInfo["native_responses_mode"] == "enabled" {
		out, err := llmdialogue.ParseNativeResponses(d.Native)
		out.NativeResponses = err == nil
		return out, err
	}
	return llmdialogue.ParseDialogue("openai-responses", d.Native)
}

// NativeResponsesConfigured 判断目录是否有显式原生灰度部署；参数 models 为只读目录，返回是否需要读取总开关。
// 执行器调用，全部缺省/legacy时不增加配置数据库查询；非法模式不启用，无外部副作用。
func NativeResponsesConfigured(models []config.ModelEntry) bool {
	for _, model := range models {
		if (model.ModelInfo["native_responses_mode"] == "enabled" || model.ModelInfo["native_responses_mode"] == "observe") && ValidateNativeResponses(model) == nil {
			return true
		}
	}
	return false
}
