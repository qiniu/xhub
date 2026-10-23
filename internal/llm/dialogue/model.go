package dialogue

// Turn 是文本、用户图片与函数工具的内部对话项；ID 是工具调用关联 ID，Arguments 保留 JSON 参数字符串。
// 输入、上游与输出分别编码一次，避免按供应商和协议组合维护转换函数。
type Turn struct {
	Role, Text, ID, Name, Arguments string
	Calls                           []Turn
	// Content 仅在有图片时保存有序多模态块；Text 保留文本投影供既有护栏、日志及工具链使用。
	Content []ContentPart `json:",omitempty"`
	// Reasoning 是可公开推理文本；加密项仅通过 Native 在同协议重放。
	Reasoning string
	// Native 保存受身份绑定的原协议历史项；跨协议不能直接重放不透明推理内容。
	Native         map[string]any `json:",omitempty"`
	NativeProtocol string         `json:",omitempty"`
}

// Dialogue 保存统一对话请求；Options 只保存已实现协议可表达的已验证参数。
type Dialogue struct {
	// DeferredResponses 仅由可信灰度选择设置，延迟到候选协议明确后才做转换投影。
	DeferredResponses bool
	// NativeResponses 标记已选中的独立原生执行器，控制响应终态观测；不从请求正文读取。
	NativeResponses bool
	Turns           []Turn
	Tools           []map[string]any
	Options         map[string]any
	Stream          bool
	// Source/Native 是护栏处理后的原协议正文；只在同协议编码时使用。
	Source string
	Native map[string]any
	// BridgeError 只阻止无法无损表示的跨协议候选，不阻止兼容的原协议候选。
	BridgeError string
	// ToolBindings 保存本轮 namespace/custom 到 Chat 函数名的映射，回复时恢复客户端工具身份。
	ToolBindings map[string]ToolBinding
}

// DialogueResult 保存实测回复与用量；ID 来源上游，由网关按该 ID 保存临时续接上下文。
type DialogueResult struct {
	ID, Text, Stop string
	// Refused 表示上游明确拒绝；Refusal 仅保存上游提供的公开说明，空说明不能伪装成空白成功。
	Refused bool
	Refusal string
	Calls   []Turn
	Usage   map[string]any
	// Reasoning 保存公开推理文本，用于 Chat/Responses 桥接，不承载密文。
	Reasoning string
	// 原始回复用于同协议输出，防止推理项、扩展用量和供应商字段被重新编码丢失。
	Source      string
	Native      map[string]any
	BridgeError string
	// ToolBindings 由请求执行链附加；只有跨协议 Responses 输出使用，原生结果保持原样。
	ToolBindings map[string]ToolBinding
}
