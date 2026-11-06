// 本文件透传无需协议转换的字节流，增量提取用量和响应 ID；与 dialogue 的终态校验转换链不同。
// 调用关系与边界：原字节流透传、增量用量解析与响应归属标识提取；具体参数、返回及副作用见各方法说明。
package stream

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	dpusage "github.com/sunqirui1987/xhub/internal/dataplane/usage"
	"github.com/sunqirui1987/xhub/internal/util"
)

// captureLimit 是一次流式响应最多保留给用量日志的字节数。超过这个量的正文
// 不再留档，但要记一行：截断之后按正文估算的 token 会偏低。
const captureLimit = 2 << 20

// ErrClientWrite 标识响应写向客户端时的失败；调用方不得据此推断是谁中断连接。
var ErrClientWrite = errors.New("client response write failed")

// noteCaptureTruncated 在流式日志捕获达到上限时记录诊断提示。
// 参数 kept 为已保存字节数；返回值无，PipeStream 追加捕获后调用，未到上限时不输出。
// 提示正文估算可能偏低，但真实用量解析仍消费完整流；只写进程日志，不截断上游传输。
func noteCaptureTruncated(kept int) {
	if kept >= captureLimit {
		util.Debug("stream capture reached its limit kept=%d; token estimates from the body may run low", kept)
	}
}

// ResponseID 从同步 JSON 或捕获 SSE 中寻找首个响应级 ID，供成功后保存归属。
// 参数 raw 为响应字节；返回 ID，非法或没有响应标识返回空串；Serve 成功路径调用。
// 先尝试完整 JSON，再按 data 行解析，跳过 DONE/坏帧；Responses 工具和输出项 ID 不能成为父响应 ID。
// 纯解析，不提交归属；捕获截断时可能找不到 ID，调用方应允许空值而非生成供应商 ID。
func ResponseID(raw []byte) string {
	if id := responseDocumentID(raw); id != "" {
		return id
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
		if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
			continue
		}
		if id := responseDocumentID(line); id != "" {
			return id
		}
	}
	return ""
}

// responseDocumentID 读取一份 JSON 文档中的响应级标识。
// 参数 raw 为单帧或同步 JSON；返回 ID 或空串；ResponseID 调用。
// response.* 只读取 response.id，message_start 读取 message.id，其他文档读顶层 id。
// 每次独立解码避免坏帧沿用上一帧数据；不读取工具/output item 的 ID，不修改正文。
func responseDocumentID(raw []byte) string {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return ""
	}
	kind, _ := doc["type"].(string)
	if strings.HasPrefix(kind, "response.") {
		nested, _ := doc["response"].(map[string]any)
		id, _ := nested["id"].(string)
		return id
	}
	if kind == "message_start" {
		nested, _ := doc["message"].(map[string]any)
		id, _ := nested["id"].(string)
		return id
	}
	id, _ := doc["id"].(string)
	return id
}

// PipeStream 将无需协议转换的上游流字节透传，同时解析用量并捕获日志正文。
// 参数 w 为客户端写入器，resp 为上游响应，start 为请求开始时间；返回 wrote、usage、ttft、captured、err。
// Serve 的普通流分支调用；本函数负责关闭 resp.Body，首次读到字节时发送状态/流媒体头并测量耗时。
// wrote 表示已开始响应，不能等同于客户端实际完整接收；写失败和短写以 ErrClientWrite 返回。
// 原始读取错误向调用方返回，EOF 视为正常结束；与 PipeDialogue 不同，这条透传链不校验协议成功终态。
// 捕获最多 2 MiB，超过部分继续转发且仍全部送给用量解析器；截断只限制日志，不限制统计扫描。
// 有写出后调用方不可重试，避免拼接两次回复；函数不执行路由、记账或会话提交。
func PipeStream(w http.ResponseWriter, resp *http.Response, start time.Time) (bool, map[string]any, time.Duration, []byte, error) {
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	wrote := false
	var ttft time.Duration
	var captured []byte
	parser := streamUsageParser{}
	for {
		n, err := resp.Body.Read(buf)
		// Reader 可以同时返回有效字节和 EOF/错误，必须先处理 n>0 的
		// 最后片段，再执行下面的收尾；否则末帧用量或正文会丢失。
		if n > 0 {
			if !wrote {
				ttft = time.Since(start)
				w.Header().Set("Content-Type", "text/event-stream")
				util.PreventSSETransform(w.Header())
				w.WriteHeader(resp.StatusCode)
				wrote = true
			}
			// 尽早转发已读数据，避免大块事件先解析而拖慢用户看到首字。
			written, writeErr := w.Write(buf[:n])
			if writeErr == nil && written != n {
				writeErr = io.ErrShortWrite
			}
			if writeErr == nil {
				// 沿包装链查找可返回错误的刷新器，避免中间层吞掉连接错误。
				if flushErr := util.FlushResponse(w); flushErr != nil {
					writeErr = flushErr
				}
			}
			if len(captured) < 2<<20 {
				take := n
				if len(captured)+take > 2<<20 {
					take = (2 << 20) - len(captured)
				}
				captured = append(captured, buf[:take]...)
				noteCaptureTruncated(len(captured))
			}
			parser.write(buf[:n], false)
			// 解析器始终消费完整读取片段；上面的 2 MiB 限制只作用于
			// 日志留档，不能让末尾出现的供应商实测用量被忽略。
			if writeErr != nil {
				// 上游已经读到的用量仍保留为诊断事实；写失败不代表用户主动停止，也不能继续重放。
				parser.write(nil, true)
				return wrote, parser.usage, ttft, captured, fmt.Errorf("%w: %w", ErrClientWrite, writeErr)
			}
		}
		if err != nil {
			parser.write(nil, true)
			if err == io.EOF {
				err = nil
			}
			return wrote, parser.usage, ttft, captured, err
		}
	}
}

// streamUsageParser 是透传流的增量用量扫描器，为一次 PipeStream 保留跨 Read 的残行。
// 完整行尝试提取统计并递归合并；不验证协议成功终态，也不处理 SSE 多行 data 合并语义。
// 不能在多个请求间共享，统计对象可能在后续写入时原地更新。
type streamUsageParser struct {
	// 跨 Read 保留的未完成一行，final 收尾时也尝试解析。
	pending []byte
	// 累计用量对象，后报字段覆盖同名旧值，缺失字段保留。
	usage map[string]any
}

// write 向用量解析器追加一段字节，消费所有完整行并保留跨读取的残片。
// 参数 raw 为本次片段，final 为是否收尾；返回值无，PipeStream 每次读取和退出前调用。
// 供应商可能在 JSON token 中间分块，pending 必须跨 Read 保留；final 时再处理无换行末行。
// 修改 pending/usage，实际传输不受解析影响；按行处理，不实现多行 data 拼成一个 SSE 事件的语义。
func (p *streamUsageParser) write(raw []byte, final bool) {
	p.pending = append(p.pending, raw...)
	for {
		i := bytes.IndexByte(p.pending, '\n')
		if i < 0 {
			break
		}
		p.consumeLine(p.pending[:i])
		p.pending = p.pending[i+1:]
	}
	if final && len(p.pending) > 0 {
		p.consumeLine(p.pending)
		p.pending = nil
	}
}

// consumeLine 对一条完整 SSE data 或逐行 JSON 提取并合并用量。
// 参数 line 为一行字节；返回值无，由 write 调用。
// 去 data 前缀和空白，跳过 DONE、空行与非法 JSON；有效用量递归覆盖合并，后报零值不能丢失。
// 只修改 parser.usage，不写响应或报告协议成功；多行 JSON 事件不在本局部解析器支持范围。
func (p *streamUsageParser) consumeLine(line []byte) {
	line = bytes.TrimSpace(line)
	if bytes.HasPrefix(line, []byte("data:")) {
		line = bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
	}
	if len(line) == 0 || bytes.Equal(line, []byte("[DONE]")) {
		return
	}
	var doc map[string]any
	if json.Unmarshal(line, &doc) == nil {
		p.usage = dpusage.MergeUsage(p.usage, dpusage.UsageFromDocument(doc))
	}
}

// streamUsage 一次性扫描完整片段中的用量，并保留此前统计。
// 参数 raw 为准备完整扫描的字节，prev 为旧用量，可为 nil；返回合并映射，无新统计时沿用 prev。
// 本包辅助调用/测试使用；临时 parser 以 final 收尾，不适合逐次扫描跨调用残缺行，连续读取应复用 streamUsageParser。
// 可能原地更新 prev，不发送响应或关闭资源。
func streamUsage(raw []byte, prev map[string]any) map[string]any {
	p := streamUsageParser{usage: prev}
	p.write(raw, true)
	return p.usage
}
