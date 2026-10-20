// 本文件负责统一对话正文、Responses 授权历史恢复、注册请求构造和管理面真实连接探测。
// 调用关系与边界：统一对话请求构造与流协议转换；具体参数、返回及副作用见各方法说明。
package dialogue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sunqirui1987/xhub/internal/config"
	dptransport "github.com/sunqirui1987/xhub/internal/dataplane/transport"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	providercapability "github.com/sunqirui1987/xhub/internal/provider/capability"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	providerdeployment "github.com/sunqirui1987/xhub/internal/provider/deployment"
)

// ErrDialogueCredentials 区分连接配置缺失与协议编码错误，供数据面返回上游鉴权错误。
var ErrDialogueCredentials = errors.New("upstream address and credentials required")

// DialogueProbeErrorKind 表示管理面连接探测的安全失败类别。
// 供 ProbeDialogue 与 HTTP 层约定固定诊断文案；类别不携带地址、密钥、响应正文或原始网络错误。
type DialogueProbeErrorKind string

const (
	// 部署未登记可供统一对话使用的执行协议，不应发起探测。
	DialogueProbeUnsupported DialogueProbeErrorKind = "unsupported"
	// 登记执行配置、请求编码或服务端凭据不完整，探测无法安全构建。
	DialogueProbeConfiguration DialogueProbeErrorKind = "configuration"
	// HTTP 客户端缺失、拨号或正文读取失败，尚不能确认协议可用。
	DialogueProbeUnavailable DialogueProbeErrorKind = "unavailable"
	// 收到成功 HTTP 状态，但正文无法按登记协议解析为有效对话结果。
	DialogueProbeProtocol DialogueProbeErrorKind = "protocol"
	// 上游返回非 2xx 状态，HTTPStatus 保存原状态而不暴露原正文。
	DialogueProbeHTTP DialogueProbeErrorKind = "http"
	// 响应超过管理面探测的 1 MiB 限制；与正常推理正文上限相互独立。
	DialogueProbeOversize DialogueProbeErrorKind = "oversize"
)

// DialogueProbeError 是可安全记录的探测失败对象，按 Kind 区分配置、传输和响应校验失败。
// HTTPStatus 仅在上游非成功状态时设置；Error 返回固定分类文本而不暴露原始错误或凭据。
// 管理面应按 Kind 映射对外提示，不能把底层网络异常直接拼进用户响应。
type DialogueProbeError struct {
	// 固定安全失败类别，供管理面映射提示。
	Kind DialogueProbeErrorKind
	// 上游非成功状态码；非 HTTP 状态失败时为零。
	HTTPStatus int
}

// Error 将连接探测失败转换成固定安全文本。
// 参数为接收者 e，无显式参数；返回分类文案或 HTTP 状态说明，由 error 接口、管理面日志调用。
// 不包含目标地址、密钥、响应正文或底层网络错误；外部界面应按 Kind 映射国际化文案，不直接展示供应商错误。
func (e *DialogueProbeError) Error() string {
	if e != nil && e.Kind == DialogueProbeHTTP && e.HTTPStatus > 0 {
		return fmt.Sprintf("upstream returned HTTP %d", e.HTTPStatus)
	}
	if e == nil {
		return "dialogue probe failed"
	}
	switch e.Kind {
	case DialogueProbeUnsupported:
		return "dialogue connection test is not supported for this deployment"
	case DialogueProbeConfiguration:
		return "deployment credentials or execution profile are invalid"
	case DialogueProbeProtocol:
		return "upstream response did not match the configured protocol"
	case DialogueProbeOversize:
		return "upstream response exceeded the probe limit"
	default:
		return "upstream request failed"
	}
}

// ProbeDialogue 对已补齐服务端凭据的对话部署发起最小真实请求，验证连接与协议正文。
// 参数 ctx 控制取消，client 提供 HTTP 传输，dep 必须来自服务端目录；返回 nil 表示可用，否则返回安全分类错误。
// 管理面的连接测试调用；发送英文 Reply with OK. 并限制输出，响应最多读取 1 MiB。
// 它不走身份预算、路由、冷却或用量链，但会在供应商侧产生真实请求及可能的费用；不能用它代替完整推理验收。
// 非成功状态、网络/读取错误、超限及协议损坏均脱敏返回；正文由本方法关闭，不接受客户端任意地址或密钥。
func ProbeDialogue(ctx context.Context, client *http.Client, dep config.ModelEntry) error {
	transport, ok := providerdeployment.Execution(dep)
	if !ok || !providercapability.DialogueProtocol(transport.Protocol) {
		return &DialogueProbeError{Kind: DialogueProbeUnsupported}
	}
	upstream, err := BuildDialogue(dep, llmdialogue.Dialogue{
		Turns:   []llmdialogue.Turn{{Role: "user", Text: "Reply with OK."}},
		Options: map[string]any{"max_tokens": 1},
	})
	if err != nil {
		return &DialogueProbeError{Kind: DialogueProbeConfiguration}
	}
	if client == nil {
		return &DialogueProbeError{Kind: DialogueProbeUnavailable}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, upstream.URL, bytes.NewReader(upstream.Body))
	if err != nil {
		return &DialogueProbeError{Kind: DialogueProbeConfiguration}
	}
	req.Header = upstream.Header.Clone()
	res, err := client.Do(req)
	if err != nil {
		return &DialogueProbeError{Kind: DialogueProbeUnavailable}
	}
	defer res.Body.Close()
	const responseLimit = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(res.Body, responseLimit+1))
	if err != nil {
		return &DialogueProbeError{Kind: DialogueProbeUnavailable}
	}
	if len(raw) > responseLimit {
		return &DialogueProbeError{Kind: DialogueProbeOversize}
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return &DialogueProbeError{Kind: DialogueProbeHTTP, HTTPStatus: res.StatusCode}
	}
	if _, err := llmdialogue.ParseDialogueResult(transport.Protocol, raw); err != nil {
		return &DialogueProbeError{Kind: DialogueProbeProtocol}
	}
	return nil
}

// ResponsesRequestBody 将已授权的临时历史与本轮 Responses 输入组合成统一对话正文。
// 参数 body 为当前请求，history 为按身份/模型/入口校验过的历史；返回独立浅拷贝正文或能力/输入错误。
// Serve 在预算与护栏之前调用，使恢复的历史同样接受限额与内容检查；先验证当前输入，避免历史掩盖非法字段。
// previous_response_id、store 在网关消费，不发给上游；instructions 仅属于当前请求，不继承上一轮指令。
// 保留护栏、跟踪及回退开关供执行链继续消费；嵌套映射并非深拷贝，不持久化历史或自行授权。
func ResponsesRequestBody(body map[string]any, history []llmdialogue.Turn) (map[string]any, error) {
	out := DialogueRequestBody(body)
	if value, ok := out["previous_response_id"]; ok && value != nil {
		if id, valid := value.(string); !valid || strings.TrimSpace(id) == "" {
			return nil, fmt.Errorf("previous_response_id must be a nonempty string")
		}
	}
	if value, ok := out["store"]; ok {
		if _, valid := value.(bool); !valid {
			return nil, fmt.Errorf("store must be boolean")
		}
	}
	delete(out, "previous_response_id")
	delete(out, "store")
	// 先验证本轮输入，不能让恢复的历史掩盖缺失或不支持的输入。
	if _, err := llmdialogue.ParseDialogue("openai-responses", out); err != nil {
		return nil, err
	}
	if len(history) > 0 {
		encoded, err := llmdialogue.EncodeDialogue(llmdialogue.Dialogue{Turns: history}, "openai-responses", "")
		if err != nil {
			return nil, err
		}
		input := out["input"]
		if text, ok := input.(string); ok {
			input = []any{map[string]any{"role": "user", "content": text}}
		}
		out["input"] = append(encoded["input"].([]any), input.([]any)...)
	}
	// 代理护栏配置仍由 Serve 消费，不能因协议字段清理而丢失。
	for _, key := range []string{"guardrails", "litellm_trace_id", "disable_fallbacks"} {
		if value, ok := body[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// ResponseHistory 构造下一轮可恢复的对话历史，保留输入与已验证的助手回复/工具调用。
// 参数 d 为护栏后的完整统一对话，body 为本轮请求，result 为成功解析结果；返回新的 Turn 切片。
// Serve 成功分支及 Responses 流完成前回调使用；移除 instructions 派生的首条临时指令，保留输入中的显式系统消息。
// 复制切片并追加回复，保持工具 ID 关联；嵌套字段可能共享引用，本函数本身不写会话缓存或数据库。
func ResponseHistory(d llmdialogue.Dialogue, body map[string]any, result llmdialogue.DialogueResult) []llmdialogue.Turn {
	if d.Source == "openai-responses" && result.Source == "openai-responses" {
		var history []llmdialogue.Turn
		input := d.Native["input"]
		if text, ok := input.(string); ok {
			input = []any{map[string]any{"role": "user", "content": text}}
		}
		for _, group := range []any{input, result.Native["output"]} {
			if items, ok := group.([]any); ok {
				for _, value := range items {
					if item, ok := value.(map[string]any); ok {
						history = append(history, llmdialogue.Turn{NativeProtocol: "openai-responses", Native: item})
					}
				}
			}
		}
		return history
	}
	turns := d.Turns
	if instructions, _ := body["instructions"].(string); instructions != "" && len(turns) > 0 {
		turns = turns[1:]
	}
	history := append([]llmdialogue.Turn(nil), turns...)
	if result.Refused && result.Refusal != "" {
		// 本轮响应已经显式标记 refusal；续轮仅将上游公开说明作为助手历史文本，
		// 避免用户换正常问题仍被 Native 协议归属锁死，也不能让拒绝历史退化为空消息。
		return append(history, llmdialogue.Turn{Role: "assistant", Text: result.Refusal})
	}
	return append(history, llmdialogue.Turn{Role: "assistant", Text: result.Text, Calls: result.Calls, Reasoning: result.Reasoning})
}

// CallerProtocol 将统一入口操作名映射为显式调用方协议 ID。
// 参数 op 为目录操作；返回已支持协议，非对话操作返回空串；Serve 用它决定是否进行对话编解码。
// 空操作或未知操作返回空串；Serve 再按原操作进入注册请求构造链，本方法不补默认聊天协议。
// 只按明确操作映射，不从供应商、模型前缀或 URL 猜协议。
// 纯映射，无配置修改或外部调用；空返回值应进入登记的非对话请求构造链。
func CallerProtocol(op string) string {
	return map[string]string{"chat": "openai-chat", "responses": "openai-responses", "messages": "anthropic-messages", "gemini": "gemini", "vertex": "vertex"}[op]
}

// DialogueRequestBody 在 ParseDialogue 前移除网关已经消费的代理字段。
// 参数 body 为护栏可能改写过的正文；返回新的浅拷贝，消息内容保留护栏修改结果。
// Serve 调用；guardrails 和 litellm_trace_id 等是执行控制而非上游对话能力，不能交给严格协议解析器误判。
// 未知协议字段仍保留，由能力解析明确拒绝；函数不改顶层原映射，嵌套值并未深拷贝，不发送请求。
func DialogueRequestBody(body map[string]any) map[string]any {
	out := make(map[string]any, len(body))
	for key, value := range body {
		out[key] = value
	}
	delete(out, "guardrails")
	delete(out, "litellm_trace_id")
	delete(out, "disable_fallbacks")
	// 会话身份已由 PlanRoute 读取；统一对话没有原厂缓存键的跨协议表示。
	delete(out, "session_id")
	// prompt_cache_key 是 OpenAI 原生缓存参数，同协议必须保留。
	return out
}

// BuildDialogue 按部署登记的执行配置编码统一对话上游请求。
// 参数 dep 为已附加凭据的部署，d 为已解析对话；返回 Upstream 或凭据、能力、编码错误。
// Serve 与 ProbeDialogue 调用；只使用登记 create 动作、鉴权头和协议编码器，不从域名或供应商名称猜路径。
// 地址或密钥缺失返回 ErrDialogueCredentials；Google 流式使用登记 streaming 动作与 alt=sse。
// 构造 URL、Header、Body，不实际发送 HTTP，不修改调用方对话；执行器负责上下文、重试和资源生命周期。
func BuildDialogue(dep config.ModelEntry, d llmdialogue.Dialogue) (dptransport.Upstream, error) {
	t, ok := providerdeployment.Execution(dep)
	if !ok {
		return dptransport.Upstream{}, fmt.Errorf("explicit upstream execution profile required")
	}
	payload, dropped, err := providerdeployment.EncodeDialogue(dep, d)
	if err != nil {
		return dptransport.Upstream{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return dptransport.Upstream{}, err
	}
	for _, a := range t.Actions {
		if a.Name != "create" {
			continue
		}
		// Google 的流式状态在操作路径表达，不写入原厂正文。
		if t.Protocol == "gemini" || t.Protocol == "vertex" {
			op := "generateContent"
			if d.Stream {
				op = "streamGenerateContent"
			}
			if !strings.HasSuffix(a.UpstreamPath, ":"+op) {
				continue
			}
		}
		hit := providercontract.Hit{Transport: t, Action: a, Names: map[string]string{"model": dep.ParamString("model", "")}}
		base, key := dptransport.BypassAuth(dep)
		if base == "" || key == "" {
			return dptransport.Upstream{}, ErrDialogueCredentials
		}
		header := http.Header{"Content-Type": []string{"application/json"}}
		for k, v := range t.Headers {
			header.Set(k, v)
		}
		value := key
		if t.Auth.Prefix != "" {
			value = t.Auth.Prefix + " " + value
		}
		if t.Auth.Header == "" {
			return dptransport.Upstream{}, fmt.Errorf("registered authentication header required")
		}
		header.Set(t.Auth.Header, value)
		target := dptransport.BypassDeploymentURL(base, hit)
		if d.Stream && (t.Protocol == "gemini" || t.Protocol == "vertex") {
			parsed, err := url.Parse(target)
			if err != nil {
				return dptransport.Upstream{}, err
			}
			query := parsed.Query()
			query.Set("alt", "sse")
			parsed.RawQuery = query.Encode()
			target = parsed.String()
		}
		return dptransport.Upstream{URL: target, Header: header, Body: raw, DroppedCapabilities: dropped}, nil
	}
	return dptransport.Upstream{}, fmt.Errorf("create operation is not registered")
}

// BuildRegisteredOperation 构造 embedding、图片、音频、重排等非对话统一操作的登记请求。
// 参数 dep 为已附加凭据部署，body 为用户正文，op 供错误定位；返回显式路径、鉴权头和 JSON 正文或错误。
// Serve 的非对话分支调用；复制正文，替换上游模型并剥离网关控制字段，保留供应商未知扩展。
// 必须登记 create 动作与鉴权规则，地址/密钥缺失返回 ErrDialogueCredentials，编码失败原样返回错误。
// 不猜供应商默认域名/协议，不修改原正文，也不发请求；最终授权与超时由执行器负责。
func BuildRegisteredOperation(dep config.ModelEntry, body map[string]any, op string) (dptransport.Upstream, error) {
	t, ok := providerdeployment.Execution(dep)
	if !ok {
		return dptransport.Upstream{}, fmt.Errorf("explicit upstream execution profile required for %s", op)
	}
	for _, a := range t.Actions {
		if a.Name != "create" {
			continue
		}
		hit := providercontract.Hit{Transport: t, Action: a}
		base, key := dptransport.BypassAuth(dep)
		if base == "" || key == "" {
			return dptransport.Upstream{}, ErrDialogueCredentials
		}
		if t.Auth.Header == "" {
			return dptransport.Upstream{}, fmt.Errorf("registered authentication header required")
		}
		payload := make(map[string]any, len(body)+1)
		for k, v := range body {
			payload[k] = v
		}
		StripGatewayFields(payload)
		// 跨模型回退开关仅供网关消费，非对话原生操作也不能将它当作供应商能力。
		if t.ModelField != "" && a.Model == "" {
			payload[t.ModelField] = providercontract.OfficialID(t.StripPrefix, dep.ParamString("model", ""))
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			return dptransport.Upstream{}, err
		}
		header := http.Header{"Content-Type": []string{"application/json"}}
		for k, v := range t.Headers {
			header.Set(k, v)
		}
		value := key
		if t.Auth.Prefix != "" {
			value = t.Auth.Prefix + " " + value
		}
		header.Set(t.Auth.Header, value)
		return dptransport.Upstream{URL: dptransport.BypassDeploymentURL(base, hit), Header: header, Body: raw}, nil
	}
	return dptransport.Upstream{}, fmt.Errorf("create operation is not registered for %s", op)
}
