package transport

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/textproto"
	"reflect"
	"strings"
	"testing"
)

// TestBypassBodyJSONContract 锁定原生正文在多次候选改写中的精度、扩展与控制字段边界。
// 参数 t 为测试上下文；前置只读 JSON，返回无；检查正常、路径模型及不改写分支，纯内存无需清理。
func TestBypassBodyJSONContract(t *testing.T) {
	raw := []byte(`{ "model":"public", "session_id":"owned", "disable_fallbacks":true, "opaque":9007199254740993, "nested":{"nil":null,"zero":0,"empty":"","array":[1,false]}, "prompt_cache_key":"cache" }`)
	parsed, err := ParseBypassBody(raw, "application/json")
	if err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), raw...)
	for _, candidate := range []string{"upstream-a", "upstream-b"} {
		payload, err := parsed.Payload("model", candidate, false)
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		if string(got["model"]) != `"`+candidate+`"` || string(got["opaque"]) != "9007199254740993" || string(got["nested"]) != `{"nil":null,"zero":0,"empty":"","array":[1,false]}` {
			t.Fatalf("候选改写丢失精度或扩展: %s", payload)
		}
		if got["session_id"] != nil || got["disable_fallbacks"] != nil || string(got["prompt_cache_key"]) != `"cache"` {
			t.Fatalf("网关控制字段/原生缓存字段边界错误: %s", payload)
		}
	}
	pathPayload, err := parsed.Payload("", "path-model", true)
	if err != nil {
		t.Fatal(err)
	}
	var pathFields map[string]json.RawMessage
	if err := json.Unmarshal(pathPayload, &pathFields); err != nil {
		t.Fatal(err)
	}
	if pathFields["model"] != nil || pathFields["opaque"] == nil {
		t.Fatalf("路径模型正文错误: %s", pathPayload)
	}
	if !bytes.Equal(parsed.Raw, original) || parsed.Fields["model"] != "public" || parsed.Fields["session_id"] != "owned" {
		t.Fatal("第一次候选改写污染了原始正文或路由投影")
	}
	untouched, err := ParseBypassBody([]byte(" { \"extra\" : 42 }\n"), "")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := untouched.Payload("", "", false)
	if err != nil || !bytes.Equal(payload, untouched.Raw) {
		t.Fatalf("无需改写的原文未保留: %q %v", payload, err)
	}
}

// TestBypassBodyMultipartContract 验证有序文件和扩展头在模型改写后保留，控制字段被移除且原始分段可重复使用。
// 参数 t 为测试上下文；内存表单包含二进制和重复普通字段；返回无，所有分段在内存中读取，无外部清理。
func TestBypassBodyMultipartContract(t *testing.T) {
	var raw bytes.Buffer
	writer := multipart.NewWriter(&raw)
	if err := writer.SetBoundary("phase5-boundary"); err != nil {
		t.Fatal(err)
	}
	for _, field := range [][2]string{{"model", "public"}, {"session_id", "owned"}, {"disable_fallbacks", "true"}, {"vendor", "one"}, {"vendor", "two"}} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	header := textproto.MIMEHeader{"Content-Disposition": {`form-data; name="image[]"; filename="original.bin"`}, "Content-Type": {"application/octet-stream"}, "X-Vendor-Part": {"keep"}}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	file := []byte{0, 255, 13, 10, 42}
	if _, err := part.Write(file); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	original := append([]byte(nil), raw.Bytes()...)
	body, err := ParseBypassBody(original, writer.FormDataContentType())
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{"first", "second"} {
		payload, err := body.Payload("model", candidate, false)
		if err != nil {
			t.Fatal(err)
		}
		reader := multipart.NewReader(bytes.NewReader(payload), body.Boundary)
		var names, values []string
		for {
			part, err := reader.NextRawPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(part)
			if err != nil {
				t.Fatal(err)
			}
			names = append(names, part.FormName())
			values = append(values, string(data))
			if part.FileName() != "" && (part.FileName() != "original.bin" || !reflect.DeepEqual(part.Header, header)) {
				t.Fatalf("文件名称/扩展头丢失: %v", part.Header)
			}
		}
		if !reflect.DeepEqual(names, []string{"model", "vendor", "vendor", "image[]"}) || !reflect.DeepEqual(values, []string{candidate, "one", "two", string(file)}) {
			t.Fatalf("分段顺序、重复字段或二进制变化: %v %q", names, values)
		}
	}
	if !bytes.Equal(body.Raw, original) || body.Fields["model"] != "public" || string(body.Parts[0].Data) != "public" {
		t.Fatal("候选改写污染入站表单")
	}
}

// TestBypassBodyRejectedInputs 固定非法对象、损坏 multipart、重复模型及未知媒体改写的失败边界。
// 参数 t 为测试上下文；每例是内存输入，返回无；错误必须可观察，未知媒体只允许不改写转发，无清理资源。
func TestBypassBodyRejectedInputs(t *testing.T) {
	for _, tc := range []struct{ name, raw, media string }{
		{"broken-json", "{", "application/json"}, {"null", "null", "application/json"}, {"array", "[]", "application/json"},
		{"trailing-json", `{} {}`, "application/json"}, {"missing-boundary", "file", "multipart/form-data"},
		{"broken-multipart", "--broken\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\npublic", "multipart/form-data; boundary=broken"},
		{"duplicate-model", "--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\na\r\n--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nb\r\n--b--\r\n", "multipart/form-data; boundary=b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseBypassBody([]byte(tc.raw), tc.media); err == nil {
				t.Fatal("非法入站正文未拒绝")
			}
		})
	}
	t.Run("empty-form", func(t *testing.T) {
		body, err := ParseBypassBody([]byte("--empty\r\n"), "multipart/form-data; boundary=empty")
		if err != nil || len(body.Fields) != 0 {
			t.Fatalf("空表单的既有解析行为变化: %v", err)
		}
	})
	t.Run("opaque-media", func(t *testing.T) {
		body, err := ParseBypassBody([]byte{0, 255}, "application/octet-stream")
		if err != nil {
			t.Fatal(err)
		}
		got, err := body.Payload("", "", false)
		if err != nil || !bytes.Equal(got, body.Raw) {
			t.Fatalf("无需改写的不透明载荷失败: %v", err)
		}
		if _, err = body.Payload("model", "new", false); err == nil {
			t.Fatal("不透明载荷错误允许模型改写")
		}
	})
}

// BenchmarkBypassBodyProjection 记录小/大 JSON 与文件表单的单次解析分配成本，供 P1 前后相同输入比较。
// 参数 b 为基准上下文，返回无；仅解析内存，不代表端到端延迟，无外部资源需要清理。
func BenchmarkBypassBodyProjection(b *testing.B) {
	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	if err := writer.WriteField("model", "public"); err != nil {
		b.Fatal(err)
	}
	file, err := writer.CreateFormFile("image", "fixture.bin")
	if err != nil {
		b.Fatal(err)
	}
	if _, err = file.Write(bytes.Repeat([]byte{0, 255}, 512<<10)); err != nil {
		b.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name, media string
		raw         []byte
	}{
		{"small-json", "application/json", []byte(`{"model":"public","input":"Hello"}`)},
		{"large-json", "application/json", []byte(`{"model":"public","input":"` + strings.Repeat("a", 1<<20) + `"}`)},
		{"multipart", writer.FormDataContentType(), form.Bytes()},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(tc.raw)))
			for i := 0; i < b.N; i++ {
				if _, err := ParseBypassBody(tc.raw, tc.media); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
