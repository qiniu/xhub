package catalog

import (
	"math"
	"testing"
)

// TestRoundMoneyRemovesBinaryFloatTail 验证费用计算结果在接口边界不会暴露二进制浮点尾数。
// 前置条件是用截图中的输入、缓存和输出金额相加；验证结果是得到稳定的十进制金额；测试没有外部副作用。
func TestRoundMoneyRemovesBinaryFloatTail(t *testing.T) {
	got := RoundMoney(0.004534 + 0.0025856 + 0.00117)
	const want = 0.0082896
	if got != want {
		t.Fatalf("rounded money = %.18f, want %.18f", got, want)
	}
}

// TestFormatRoundsMoneyForResponseHeaders 验证费用响应头使用的十进制文本没有长尾。
// 前置条件是传入会产生浮点尾数的金额；验证结果是响应文本为可读的小数；测试没有外部副作用。
func TestFormatRoundsMoneyForResponseHeaders(t *testing.T) {
	if got, want := Format(0.008289600000000001), "0.0082896"; got != want {
		t.Fatalf("formatted money = %q, want %q", got, want)
	}
}

// TestRoundMoneyBoundaries 验证零、负数、极小费用和非有限输入，输出归一化不把非零费用归零；纯函数无需清理。
func TestRoundMoneyBoundaries(t *testing.T) {
	for _, value := range []float64{0, -0.0082896, 1e-25, math.MaxFloat64} {
		got := RoundMoney(value)
		if value != 0 && got == 0 {
			t.Fatalf("费用 %g 被归零", value)
		}
		if value == 1e-25 && got != value {
			t.Fatalf("极小费用改变: %g", got)
		}
	}
	if !math.IsNaN(RoundMoney(math.NaN())) || !math.IsInf(RoundMoney(math.Inf(1)), 1) {
		t.Fatal("异常值未交给调用方校验")
	}
}
