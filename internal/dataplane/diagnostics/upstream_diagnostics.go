// 本文件旁路观察原始 HTTP 响应及用量，区分供应商统计缺失、明确零值和诊断中断。
// 调用关系与边界：出站事实观察和安全进程日志；具体参数、返回及副作用见各方法说明。
package diagnostics

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"
)

// UpstreamDiagnostics 保存转换之前的真实出站响应事实，适配和原生链共同填充。
// Headers/Trailers 是原始多值头，并未在此结构里脱敏；写日志或向客户端输出时由消费方另行过滤。
// 正文读取过程中原地更新共享对象，消费方应在读取完成后使用；原始 Usage 与 BillingUsage 分开，避免估算覆盖原厂统计。
type UpstreamDiagnostics struct {
	// 供应商明确失败的完整错误及关联信息；与 HTTP 状态分开保存，避免流捕获上限丢失尾部错误。
	Error map[string]any `json:"error,omitempty"`
	// 实际出站路径，不含用户信息和查询参数，用于安全关联部署端点。
	Endpoint string `json:"endpoint"`
	// 当前出站尝试根据部署策略移除的能力；持久化原因固定为部署不支持该能力。
	DroppedCapabilities []string `json:"dropped_capabilities,omitempty"`
	// 首次 HTTP 响应字节的时刻；零值表示未测得，与首 SSE data 的时间不同。
	FirstByteAt time.Time `json:"-"`
	// 上游原始 HTTP 状态，协议转换或网关错误映射前保留。
	StatusCode int `json:"status_code"`
	// 完整原始多值响应头的副本，尚未脱敏；日志持久化或客户端输出须另行过滤。
	Headers http.Header `json:"headers"`
	// 读到 EOF 后收集的原始多值尾部头；提前关闭时可能不完整。
	Trailers http.Header `json:"trailers,omitempty"`
	// 上游原始统计，包括零值、嵌套详情和扩展字段，避免被估算覆盖。
	Usage map[string]any `json:"usage"`
	// 区分没有统计与明确报告空对象或零用量，不能仅靠 token 是否大于零判断。
	UsageReported bool `json:"usage_reported"`
	// 诊断正文损坏、超限或提前中断的标记，不替代真实读取错误。
	ParseError string `json:"usage_parse_error,omitempty"`
	// 执行/计价链使用的规范化统计，与原始 Usage 分开以便审计估算和协议映射。
	BillingUsage map[string]any `json:"billing_usage,omitempty"`
}

// TraceFirstByte 为真实 HTTP 请求安装首响应字节追踪。
// 参数 req 为已构造的非 nil 出站请求；返回携带新 context 的请求副本与记录时间的指针。
// 适配/原生转发器在 Do 前调用，Do 完成后读取时间；GotFirstResponseByte 首次触发才记录。
// 不额外发探测、不改正文或头；HTTP 首字节可能只属于响应头，不能直接当作首 token 或首 SSE data。
// 时间指针限该请求生命周期内使用，调用方不应在传输尚更新时并发读取。
func TraceFirstByte(req *http.Request) (*http.Request, *time.Time) {
	first := new(time.Time)
	trace := &httptrace.ClientTrace{GotFirstResponseByte: func() {
		if first.IsZero() {
			*first = time.Now()
		}
	}}
	return req.WithContext(httptrace.WithClientTrace(req.Context(), trace)), first
}

// upstreamObserver 包装上游正文，透明返回原读取结果，同时旁路解析诊断用量。
// JSON 暂存上限 64 MiB，SSE 单事件上限 8 MiB；超限只标记诊断缺失，真实传输仍继续。
// 由 ObserveUpstream 为单个响应创建，不可并发读取；调用方仍负责 Close，只有读到 EOF 才能认为完整观察。
type upstreamObserver struct {
	// 真实正文读取器，Read/Close 的结果保持不变，观察器不吞传输错误。
	io.ReadCloser
	// 原 HTTP 响应，EOF 后读取其 Trailer。
	response *http.Response
	// 边读边更新的共享诊断对象，不支持并发读写。
	diagnostics *UpstreamDiagnostics
	// 媒体类型是否按 SSE 处理，决定事件解析或完整 JSON 暂存。
	stream bool
	// 尚未形成完整行的 SSE 残片，或等待 EOF 的 JSON 正文。
	pending []byte
	// 当前 SSE 事件的多行 data 拼接内容。
	event []byte
	// 当前事件已超诊断上限，丢弃观察内容直到事件边界；仍原样转发。
	dropping bool
	// 是否已读到 EOF，关闭时据此区分完整与提前中断。
	finished bool
}

// ObserveUpstream 在任何正文读取或协议转换前安装透明诊断观察器。
// 参数 resp 为非 nil 且带可读 Body 的 HTTP 响应；返回随读取持续更新的 UpstreamDiagnostics 指针。
// 两类转发器调用；复制完整多值响应头并记录实际出站路径，以包装器替换 resp.Body。
// 观察器不改变返回给传输层的字节或读取错误；诊断超限只标记不完整，不截断实际转发。
// 本函数不读取/关闭正文，调用方仍须关闭包装后的 Body；返回值限单请求使用，不是并发安全快照。
func ObserveUpstream(resp *http.Response) *UpstreamDiagnostics {
	diagnostics := &UpstreamDiagnostics{StatusCode: resp.StatusCode, Headers: resp.Header.Clone()}
	if resp.Request != nil && resp.Request.URL != nil {
		diagnostics.Endpoint = resp.Request.URL.EscapedPath()
	}
	resp.Body = &upstreamObserver{ReadCloser: resp.Body, response: resp, diagnostics: diagnostics,
		stream: strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream")}
	return diagnostics
}

// Read 透明读取底层响应，同时按 JSON 或 SSE 提取原始用量诊断。
// 参数 p 为调用方缓冲区；返回底层原样 n、err；传输层通过包装后的 Body 调用。
// 首次正文数据补充时间；JSON 暂存不超过 64 MiB，SSE 行/事件不超过 8 MiB，超限标记 ParseError 而不改变传输。
// EOF 才处理最后无换行事件并复制 trailers；非 EOF 读取错误标记不完整，不能伪造完整统计。
// 修改请求私有诊断与拼帧缓冲，不修改调用方接收到的数据，不独立关闭响应。
func (o *upstreamObserver) Read(p []byte) (int, error) {
	n, err := o.ReadCloser.Read(p)
	if n > 0 && o.diagnostics.FirstByteAt.IsZero() {
		o.diagnostics.FirstByteAt = time.Now()
	}
	if o.stream {
		for _, b := range p[:n] {
			if b == 10 {
				o.line(o.pending)
				o.pending = nil
			} else if len(o.pending) < 8<<20 {
				o.pending = append(o.pending, b)
			} else {
				o.diagnostics.ParseError = "upstream SSE event exceeds 8 MiB"
				o.dropping = true
			}
		}
	} else if !o.dropping {
		if len(o.pending)+n > 64<<20 {
			o.pending = nil
			o.dropping = true
			o.diagnostics.ParseError = "upstream JSON exceeds 64 MiB"
		} else {
			o.pending = append(o.pending, p[:n]...)
		}
	}
	if err != nil && !o.finished {
		o.finished = true
		if err == io.EOF {
			if o.stream {
				if len(o.pending) > 0 {
					o.line(o.pending)
				}
				o.line(nil)
			} else if !o.dropping {
				o.document(o.pending)
			}
			o.diagnostics.Trailers = o.response.Trailer.Clone()
		} else {
			o.diagnostics.ParseError = "upstream body read failed"
		}
	}
	return n, err
}

// Close 关闭被观察的上游正文，并标记提前关闭导致的诊断缺失。
// 无参数；返回底层 Close 错误；转发器正常或失败清理时调用。
// 尚未 EOF 且没有已有错误时记录未完成状态，保留已提取用量；不吞掉底层关闭错误，也不宣称关闭等于读完。
func (o *upstreamObserver) Close() error {
	if !o.finished && o.diagnostics.ParseError == "" {
		o.diagnostics.ParseError = "upstream body closed before EOF"
	}
	return o.ReadCloser.Close()
}

// line 将 SSE 的多行 data 拼为一个完整事件。
// 参数 line 为无 LF 的一行，可带 CR；返回值无，Read 和 EOF 收尾调用。
// 去末尾 CR，空行提交事件；仅 data 行参与 JSON 拼接，每段后补 LF 以遵守 SSE 多行语义。
// 超限后丢弃当前事件的诊断内容，在下一个空行重置 dropping；真实响应字节不受影响。
// 仅修改观察器缓冲及诊断，不直接写客户端。
func (o *upstreamObserver) line(line []byte) {
	line = bytes.TrimSuffix(line, []byte{13})
	if len(line) == 0 {
		if !o.dropping && len(o.event) > 0 {
			o.document(o.event)
		}
		o.event = nil
		o.dropping = false
		return
	}
	if bytes.HasPrefix(line, []byte("data:")) && !o.dropping {
		data := bytes.TrimPrefix(line[5:], []byte(" "))
		if len(o.event)+len(data)+1 > 8<<20 {
			o.dropping = true
			o.diagnostics.ParseError = "upstream SSE event exceeds 8 MiB"
			o.event = nil
			return
		}
		o.event = append(o.event, data...)
		o.event = append(o.event, 10)
	}
}

// document 对完整 JSON 正文或 SSE data 事件提取原始用量及错误，错误独立于正文捕获上限保存。
// 参数 raw 为事件/正文；返回值无，由 line 或 Read 的 EOF 分支调用。
// 忽略空数据和 DONE，用 UseNumber 保留大整数及供应商数值文本，非法 JSON 标记诊断错误。
// 有效但无用量的事件不构造假统计；仅更新诊断，不转换客户端协议或计价。
func (o *upstreamObserver) document(raw []byte) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("[DONE]")) {
		return
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&doc); err != nil {
		o.diagnostics.ParseError = "upstream response is not valid JSON"
		return
	}
	if failure := ExtractUpstreamError(doc); failure != nil {
		o.diagnostics.Error = failure.Envelope
	}
	o.usage(doc)
}

// usage 在供应商原始文档中寻找用量对象及已知包络。
// 参数 doc 为 JSON 对象；返回值无，document 调用并对 response/message 嵌套递归。
// usage 或 usageMetadata 即使为空对象也算明确报告；保留零值、扩展字段与原始名称。
// 修改 diagnostics.Usage/UsageReported，不归一 token 或改正文；账单使用单独的 BillingUsage。
func (o *upstreamObserver) usage(doc map[string]any) {
	for _, key := range []string{"usage", "usageMetadata"} {
		if usage, ok := doc[key].(map[string]any); ok {
			o.diagnostics.UsageReported = true
			o.diagnostics.Usage = mergeDiagnosticUsage(o.diagnostics.Usage, usage)
		}
	}
	for _, key := range []string{"response", "message"} {
		if nested, ok := doc[key].(map[string]any); ok {
			o.usage(nested)
		}
	}
}

// mergeDiagnosticUsage 递归覆盖合并分段上游统计，保留供应商原始值。
// 参数 dst 为旧统计，src 为新片段；返回合并映射，dst 为 nil 时创建；usage 调用。
// 同名标量以后报为准，嵌套对象递归，空对象和明确零仍保留，不能逐帧相加重复累计。
// 原地修改 dst，限请求私有诊断使用；无费用计算或外部存储副作用。
func mergeDiagnosticUsage(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = map[string]any{}
	}
	for key, value := range src {
		if nested, ok := value.(map[string]any); ok {
			existing, _ := dst[key].(map[string]any)
			dst[key] = mergeDiagnosticUsage(existing, nested)
		} else {
			dst[key] = value
		}
	}
	return dst
}
