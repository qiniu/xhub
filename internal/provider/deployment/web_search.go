package deployment

import (
	"fmt"

	"github.com/sunqirui1987/xhub/internal/config"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
)

// ValidateWebSearch 校验模型级搜索能力/降级配置；参数 m 为部署，返回字段错误或 nil。
// 部署保存及运行时编码调用；缺省保留旧行为，不根据模型名、供应商或域名猜测搜索支持，不修改配置。
func ValidateWebSearch(m config.ModelEntry) error {
	for key, allowed := range map[string][]string{
		"web_search_support":  {"inherit", "supported", "unsupported"},
		"web_search_fallback": {"reject", "drop_optional"},
	} {
		if value, exists := m.ModelInfo[key]; exists && value != nil {
			text, ok := value.(string)
			valid := false
			for _, option := range allowed {
				valid = valid || text == option
			}
			if !ok || !valid {
				return fmt.Errorf("invalid model_info.%s", key)
			}
		}
	}
	if m.ModelInfo["web_search_support"] == "supported" {
		t, ok := Execution(m)
		if !ok || t.Protocol != "openai-responses" {
			return fmt.Errorf("web_search support requires a native Responses upstream transport")
		}
	}
	return nil
}

// EncodeDialogue 应用当前部署搜索策略后复用原有协议编码；参数为部署和不可变请求，返回正文、降级能力列表或错误。
// 候选筛选、预览及真实执行共用，避免预览可选但执行失败；不跨部署共享降级结果，不改凭据或发送请求。
func EncodeDialogue(m config.ModelEntry, d llmdialogue.Dialogue) (map[string]any, []string, error) {
	var err error
	d, err = PrepareDialogue(m, d)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateWebSearch(m); err != nil {
		return nil, nil, err
	}
	t, ok := Execution(m)
	if !ok {
		return nil, nil, fmt.Errorf("explicit upstream execution profile required")
	}
	native := t.Protocol == "openai-responses" && m.ModelInfo["web_search_support"] != "unsupported"
	prepared, dropped, err := llmdialogue.PrepareWebSearch(d, native, m.ModelInfo["web_search_fallback"] == "drop_optional")
	if err != nil {
		return nil, nil, err
	}
	prepared, effort, err := prepareMessagesReasoning(m, prepared)
	if err != nil {
		return nil, nil, err
	}
	payload, err := llmdialogue.EncodeDialogue(prepared, t.Protocol, m.ParamString("model", ""))
	if err != nil {
		return nil, nil, err
	}
	if effort != "" {
		// 输出上限仍由原请求和硬额度链控制；adaptive并不意味着可以擅自扩大max_tokens。
		payload["thinking"] = map[string]any{"type": "adaptive"}
		payload["output_config"] = map[string]any{"effort": effort}
	}
	if dropped {
		return payload, []string{"web_search"}, nil
	}
	return payload, nil, nil
}
