// 本文件按登记鉴权转发原生 HTTP，过滤入站凭据、保持 SSE 原字节并测量原生终态用量。
// 调用关系与边界：原生出站请求载体、正文/URL 处理和 HTTP 转发；具体参数、返回及副作用见各方法说明。
package transport

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dpdiagnostics "github.com/sunqirui1987/xhub/internal/dataplane/diagnostics"
	dpstream "github.com/sunqirui1987/xhub/internal/dataplane/stream"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	providercontract "github.com/sunqirui1987/xhub/internal/provider/contract"
	providermeasurement "github.com/sunqirui1987/xhub/internal/provider/measurement"
	"github.com/sunqirui1987/xhub/internal/util"
)

// ForwardResponse 描述原生转发的一次 HTTP 结果，由 ForwardOfficial 返回给创建或查询执行器。
// 保留状态、原始头与同步正文；Streamed 表示 SSE 已直接发送，WriteThrough 必须避免再次写入。
// Usage 保存协议测量事实，缺失计价维度用 pricing_blocked 表示；诊断保存转换前的原始观察，输出时仍需过滤。
type ForwardResponse struct {
	// 本次上游的原始响应诊断，共享对象随正文读取更新，头尚未脱敏。
	Diagnostics *dpdiagnostics.UpstreamDiagnostics // 原生转发的完整响应事实。
	// 上游实际 HTTP 状态，零值由 WriteThrough 兜底为 502。
	StatusCode int
	// 完整原始响应头，向客户端输出时只复制白名单。
	Header http.Header
	// 已缓冲的同步响应正文；流式发送后不再用于重复输出。
	Body []byte
	// 首个 SSE data 行到达时间；同步响应或无 data 流时为零。
	FirstByteAt time.Time // 首个 SSE data 行到达时刻；同步响应不产生此样本。
	// 是否已经直接发送 SSE，决定 WriteThrough 必须跳过再次写出。
	Streamed bool
	// 协议测量得到的用量及核价阻塞标记，不以缺失统计估造成功收费。
	Usage map[string]any
	// 成功原生流的响应归属 ID；同步响应由执行器解析正文。
	ResponseID string // 成功原生流的存储响应归属，非流由正文读取。
}

// ForwardOfficial 只向登记的供应商地址发送一次原生请求，并保留协议响应及实测事实。
// 参数 upstreamClient 为共享 HTTP 客户端；r 提供请求 context；method/address/key 为登记操作与服务端凭据；body/in 为载荷与入站头；transport 为显式注册传输；w 可为 nil 以禁用即时流式写出。
// 返回 ForwardResponse 与网络、读取、写出、超限或缺少成功终态错误；写出错误及短写含 ErrClientWrite，原生创建和查询调用。
// 过滤客户端鉴权与逐跳头后设置登记的供应商认证，复制 client 禁止重定向；若 context 有截止时间则由它独占超时控制。
// 安装原始诊断后读取；2xx SSE 且有 w 时逐行保留换行转发并 Flush，事件/行上限 8 MiB，必须观测正常终态。
// 完整 Responses completed 或明确输出上限终态可立即结束；其他协议保留既有读取规则。
// 多个图像事件保留事实并阻止当前单规格自动定价；同步正文最多 64 MiB，不将缺失统计伪造成付费事实。
// 本函数关闭上游 Body；流式开始后可能已写状态或部分正文，调用方必须检查 Streamed 并禁止重复写出/危险重试。
// 不负责路由重试、任务归属或结算去重，副作用为单次出站 HTTP 及可选客户端输出。
func ForwardOfficial(upstreamClient *http.Client, r *http.Request, method, address, key string, body []byte, in http.Header, transport providercontract.Transport, w http.ResponseWriter) (ForwardResponse, error) {
	var result ForwardResponse
	req, err := http.NewRequestWithContext(r.Context(), method, address, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	for k, values := range in {
		// 调用方身份只供网关鉴权；发送给供应商的凭据来自下面的登记配置。
		// 同时剔除逐跳头，避免把一次客户端连接的传输控制带到另一条连接。
		if !bypassHeader(k, in) {
			continue
		}
		for _, v := range values {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept-Encoding", "identity")
	if transport.Auth.Header == "" {
		return result, fmt.Errorf("upstream authentication is not configured")
	}
	value := key
	if transport.Auth.Prefix != "" {
		value = transport.Auth.Prefix + " " + key
	}
	req.Header.Set(transport.Auth.Header, value)
	for k, v := range transport.Headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := *upstreamClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if _, deadline := r.Context().Deadline(); deadline {
		// 执行器已限定本次尝试的截止时间；关闭共享 client 的额外超时副本，
		// 防止两套计时提前截断长流，同时保持原共享客户端配置不变。
		client.Timeout = 0
	}
	req, firstByte := dpdiagnostics.TraceFirstByte(req)
	resp, err := client.Do(req)
	if err != nil {
		// 网络失败没有响应对象，仍保留已选择的实际出站路径供失败日志展示。
		result.Diagnostics = &dpdiagnostics.UpstreamDiagnostics{Endpoint: req.URL.EscapedPath()}
		return result, err
	}
	resp.Request = req
	result.Diagnostics = dpdiagnostics.ObserveUpstream(resp)
	if !firstByte.IsZero() {
		result.Diagnostics.FirstByteAt = *firstByte
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	result.Header = resp.Header.Clone()
	if w != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		result.Streamed = true
		// 写状态码即已开始客户端响应，即使还没收到 data，也不能再换
		// 部署或由 WriteThrough 重写状态；Streamed 必须在这里提前置位。
		copyBypassResponseHeaders(w, result.Header)
		w.WriteHeader(resp.StatusCode)
		scanner := bufio.NewScanner(resp.Body)
		scanner.Split(sseLines)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		var event bytes.Buffer
		var streamState providermeasurement.NativeStreamState
		request, _ := ParseBypassBody(body, in.Get("Content-Type"))
		for scanner.Scan() {
			line := scanner.Bytes()
			// 客户端收到保留换行的原始行；另用 event 缓冲做旁路测量，
			// 不重新编码 JSON，避免改变原生事件格式或供应商扩展字段。
			if result.FirstByteAt.IsZero() && bytes.HasPrefix(line, []byte("data:")) {
				result.FirstByteAt = time.Now()
			}
			if _, err = dpstream.WriteClient(w, line); err != nil {
				return result, err
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			if event.Len()+len(line) > 8<<20 {
				return result, fmt.Errorf("bypass SSE event exceeds 8 MiB")
			}
			event.Write(line)
			if bytes.Equal(line, []byte("\n")) || bytes.Equal(line, []byte("\r\n")) {
				streamState.Observe(transport.Protocol, event.Bytes())
				facts := providermeasurement.NativeStreamUsage(transport.Protocol, event.Bytes(), request.Fields)
				if len(facts) > 0 {
					// 与标准流使用相同的逐帧合并，保留真矛盾并替换旧别名快照。
					result.Usage = llmdialogue.MergeDialogueUsage(result.Usage, facts)
					// 多张图片可能各自带用量和规格。当前结算是单规格账单，不能覆盖
					// 前一张的事实后按最后一张规格计价；保留事实并阻止自动结算。
					if transport.Protocol == "openai-images" && streamState.Images > 1 {
						result.Usage["pricing_blocked"] = "multiple_image_events_require_itemized_pricing"
					}
				}
				event.Reset()
				// Responses 完整快照已带最终输出和用量；供应商保持 HTTP 连接不应
				// 把正常结束变成超时失败。仅此协议提前关闭，多图片等事件流仍读到 EOF。
				if transport.Protocol == "openai-responses" && streamState.ResponseTerminal && !streamState.Failed {
					break
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return result, err
		}
		if streamState.Failed || !streamState.Completed {
			// EOF 只能证明连接结束；没有协议成功终态可能是服务端中断，
			// 不能提交成功归属或把部分输出按完整结果结算。
			return result, fmt.Errorf("native stream ended without successful completion")
		}
		result.ResponseID = streamState.ResponseID
		if result.Usage == nil {
			result.Usage = map[string]any{"pricing_blocked": "upstream_usage_missing"}
		}
		if transport.Protocol == "openai-images" {
			result.Usage["images"] = streamState.Images
		}
		return result, nil
	}
	result.Body, err = io.ReadAll(io.LimitReader(resp.Body, (64<<20)+1))
	if err == nil && len(result.Body) > 64<<20 {
		err = fmt.Errorf("bypass response exceeds 64 MiB")
	}
	return result, err
}

// sseLines 是保留行尾原始字节的 Scanner 分割函数。
// 参数 data 为当前缓冲，atEOF 表示输入结束；返回消费字节数、完整行及错误。
// ForwardOfficial 调用；有 LF 时连同 LF 返回，EOF 的末尾非空片段也返回，尚未完整则请求更多数据。
// 与默认 ScanLines 不同，保留 CRLF/LF 使原生转发字节不变；纯分割，不解析事件或修改缓冲。
func sseLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// bypassHeader 判断某个入站头能否安全透传到供应商。
// 参数 k 为头名，headers 为整份入站头；返回允许时 true；ForwardOfficial 复制头时调用。
// 剔除调用方凭据、Cookie 和逐跳传输头，并检查 Connection 声明的额外逐跳字段；名称按 HTTP 规则处理。
// 供应商认证必须由服务端登记配置重建，不能沿用用户 Authorization；只判断，不改原头。
func bypassHeader(k string, headers http.Header) bool {
	switch strings.ToLower(k) {
	case "x-goog-api-key", "authorization", "host", "content-length", "cookie", "set-cookie", "x-api-key", "api-key", "x-litellm-api-key", "connection", "proxy-connection", "keep-alive", "proxy-authorization", "proxy-authenticate", "te", "trailer", "transfer-encoding", "upgrade":
		return false
	}
	for _, named := range strings.Split(headers.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(named), k) {
			return false
		}
	}
	return true
}

// copyBypassResponseHeaders 将允许的供应商响应头复制到客户端。
// 参数 w 为尚可修改头的写入器，header 为完整上游头；返回值无，流开始和 WriteThrough 调用。
// 按白名单保留协议需要的多值头，避免转发上游 Cookie/逐跳字段；完整诊断头仍单独保存在 ForwardResponse。
// 副作用为响应头修改，必须在 WriteHeader/正文写出前调用，不负责正文或状态码。
func copyBypassResponseHeaders(w http.ResponseWriter, header http.Header) {
	for _, k := range []string{"Content-Type", "Content-Encoding", "Cache-Control", "X-Accel-Buffering", "Retry-After", "Request-Id", "X-Request-Id"} {
		if values := header.Values(k); len(values) > 0 {
			w.Header()[k] = append([]string(nil), values...)
		}
	}
	// 非2xx的原生SSE也可能携带错误事件；全部SSE在发送状态前禁止压缩缓冲，JSON不受影响。
	if strings.HasPrefix(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		util.PreventSSETransform(w.Header())
	}
}

// WriteThrough 写出尚未流式发送的原生响应。
// 参数 w 为客户端写入器，response 为转发结果；返回值无，原生执行器完成记账后调用。
// Streamed 为 true 时立即返回，防止复制流或再次写状态；状态零回退为 502，其余保留上游状态和原正文。
// 先复制允许头再写正文；当前接口不返回客户端写入错误，调用方不能据返回推断客户端完整接收。
func WriteThrough(w http.ResponseWriter, response ForwardResponse) {
	if response.Streamed {
		return
	}
	copyBypassResponseHeaders(w, response.Header)
	status := response.StatusCode
	if status == 0 {
		status = http.StatusBadGateway
	}
	w.WriteHeader(status)
	_, _ = w.Write(response.Body)
}
