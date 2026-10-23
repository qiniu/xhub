package dialogue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/ssestream"
	llmdialogue "github.com/sunqirui1987/xhub/internal/llm/dialogue"
	"github.com/sunqirui1987/xhub/internal/util"
)

// PipeNativeDialogue 在同协议时保留完整 SSE 数据，参数与 PipeDialogue 相同；返回写出、用量、首帧耗时、日志捕获及错误。
// 标准入口仍先经过鉴权、额度和护栏；本函数仅处理传输，复用共享增量校验，不转入 bypass 链。
// Responses 校验完成对象；Messages 校验原生内容块/签名/工具快照，终态可见前提交归属；Chat 必须同时看到 finish_reason 与 DONE。
// 截断、供应商失败、写入错误均返回失败，已写出时不能重试；关闭上游正文，捕获最多 2 MiB，不修改不透明推理项。
func PipeNativeDialogue(w http.ResponseWriter, resp *http.Response, start time.Time, alias, protocol string, beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	return pipeNativeDialogue(w, resp, start, alias, protocol, false, false, beforeComplete...)
}

// PipePortableResponses 在统一 Responses 流发布前清理账户绑定推理，参数、返回及错误策略与 PipeNativeDialogue 相同。
// Serve 调用；终态回调收到完整原生对象，客户端只收到无密文、无来源推理 ID 的历史；不影响 bypass、工具 ID、用量或失败事件。
func PipePortableResponses(w http.ResponseWriter, resp *http.Response, start time.Time, alias string, beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	return pipeNativeDialogue(w, resp, start, alias, "openai-responses", true, false, beforeComplete...)
}

// pipeNativeDialogue 共享原生 SSE 验证和输出，portable 决定是否只发布公开推理，其余参数和返回与公开入口相同。
// 两个流入口调用；每帧及最终快照都清理，防止 added/done 帧泄漏密文；读取和记账仍使用原始数据。
func pipeNativeDialogue(w http.ResponseWriter, resp *http.Response, start time.Time, alias, protocol string, portable, native bool, beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	util.PreventSSETransform(w.Header())
	state := dialogueStream{observeOnly: true, nativeResponses: native, protocol: protocol, model: alias, calls: map[int]*llmdialogue.Turn{}, blocks: map[int]int{}, usage: map[string]any{}, textBlock: -1, toolIndices: map[int]int{}}
	var nativeMessages nativeMessagesStream
	var capture bytes.Buffer
	var wrote, completed bool
	var ttft time.Duration
	// write 把已校验事件写到客户端并检查短写；捕获是诊断用途，不能等同客户端接收确认。
	write := func(event string, data []byte) error {
		var b bytes.Buffer
		if event != "" {
			fmt.Fprintf(&b, "event: %s\n", event)
		}
		fmt.Fprintf(&b, "data: %s\n\n", data)
		raw := b.Bytes()
		if capture.Len() < 2<<20 {
			n := min(len(raw), (2<<20)-capture.Len())
			capture.Write(raw[:n])
		}
		n, err := w.Write(raw)
		if n > 0 {
			wrote = true
			if ttft == 0 {
				ttft = time.Since(start)
			}
		}
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
		if f, ok := w.(http.Flusher); ok && err == nil {
			f.Flush()
		}
		return err
	}
	decoder := ssestream.NewDecoder(resp)
	var err error
	for decoder.Next() {
		event := decoder.Event()
		data := strings.TrimSpace(string(event.Data))
		if data == "[DONE]" {
			if protocol != "openai-chat" {
				continue
			}
			if state.stop == "" {
				err = fmt.Errorf("upstream stream ended without finish_reason")
				break
			}
			for _, call := range state.calls {
				if call.ID == "" || call.Name == "" || !json.Valid([]byte(call.Arguments)) {
					err = fmt.Errorf("incomplete upstream tool call")
					break
				}
			}
			if err != nil {
				break
			}
			err = write(event.Type, []byte(data))
			completed = err == nil
			break
		}
		var payload map[string]any
		if err = json.Unmarshal([]byte(data), &payload); err != nil {
			break
		}
		if protocol == "anthropic-messages" {
			var result llmdialogue.DialogueResult
			result, completed, err = nativeMessages.consume(payload)
			state.usage = nativeMessages.usage
			if err != nil {
				break
			}
			if completed && len(beforeComplete) > 0 && beforeComplete[0] != nil {
				// 客户端收到 message_stop 即可提交含原生签名的工具续轮；先记录同一连接归属。
				beforeComplete[0](result)
			}
		} else if err = state.consume(protocol, data); err != nil {
			break
		}
		if protocol == "openai-responses" && (payload["type"] == "response.completed" || payload["type"] == "response.incomplete") {
			raw, _ := json.Marshal(payload["response"])
			var result llmdialogue.DialogueResult
			if native {
				result, err = llmdialogue.ParseNativeResponsesResult(raw)
			} else {
				result, err = llmdialogue.ParseDialogueResult(protocol, raw)
			}
			if err != nil {
				break
			}
			// 完整终态补齐用量，但不能抹去之前流帧已经观察到的计数矛盾。
			state.usage = llmdialogue.MergeDialogueUsage(state.usage, result.Usage)
			if len(beforeComplete) > 0 && beforeComplete[0] != nil {
				beforeComplete[0](result)
			}
			completed = true
		}
		if portable {
			if item := streamMap(payload["item"]); item != nil {
				payload["item"] = llmdialogue.PortableReasoningItems([]any{item}).([]any)[0]
			}
			if response := streamMap(payload["response"]); response != nil {
				payload["response"] = llmdialogue.PortableResponsesObject(response, "output")
			}
		}
		// 公开模型始终使用别名；原生透传保留密文，统一入口只保留可读推理和原有工具/用量事件。
		if _, ok := payload["model"]; ok {
			payload["model"] = alias
		}
		if response := streamMap(payload["response"]); response != nil {
			response["model"] = alias
		}
		if message := streamMap(payload["message"]); message != nil {
			message["model"] = alias
		}
		raw, _ := json.Marshal(payload)
		if err = write(event.Type, raw); err != nil {
			break
		}
		if completed {
			break
		}
	}
	if err == nil {
		err = decoder.Err()
	}
	if err == nil && !completed {
		err = fmt.Errorf("upstream stream ended without completion")
	}
	if err != nil && wrote {
		event, payload := streamFailureEvent(err, protocol, state.id)
		raw, _ := json.Marshal(payload)
		_ = write(event, raw)
	}
	return wrote, llmdialogue.NormalizeDialogueUsage(state.usage), ttft, capture.Bytes(), err
}

// PipePortableResponsesMode 在灰度原生路径使用独立终态观测；参数 native 来自可信候选选择，其余同 PipePortableResponses。
// 返回写出/用量/首帧/捕获/错误，执行器调用；保持公开历史清理、提交顺序和断流行为，关闭开关仍走旧校验。
func PipePortableResponsesMode(w http.ResponseWriter, resp *http.Response, start time.Time, alias string, native bool, beforeComplete ...func(llmdialogue.DialogueResult)) (bool, map[string]any, time.Duration, []byte, error) {
	return pipeNativeDialogue(w, resp, start, alias, "openai-responses", true, native, beforeComplete...)
}
