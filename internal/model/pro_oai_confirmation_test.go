package model

import "testing"

func TestProOAIAnswerStatus(t *testing.T) {
	for _, tc := range []struct{ name, answer, want string }{
		{"recorded answer", "已确认该订阅（child@example.com）对应的退款已发起/处理；预计 5–10 个工作日；发票号 TEST-0001。\n已成功", "confirmed"},
		{"formatting", "退款已处理。\n**已成功**", "confirmed"},
		{"not bank arrival", "已发起退款，银行尚未到账。如果超期请联系银行。\n已成功", "confirmed"},
		{"negative", "未找到退款记录。\n已失败", "unconfirmed"},
		{"marker only", "已成功", "review"},
		{"prompt echoed", "如果是退款已发起/处理请在最后一行输入:已成功\n如果是退款未发起或没有请在最后一行输入:已失败", "review"},
		{"contradiction", "退款已处理，但无法确认退款记录。\n已成功", "review"},
		{"not submitted", "退款尚未发起，此前退款已完成。\n已成功", "review"},
		{"conditional", "如果退款已处理，通常需要五天。\n已成功", "review"},
		{"other account", "other@example.com 退款已完成。\n已成功", "review"},
		{"partial", "退款已完成。", "review"},
		{"unknown", "当前无法查询。\n待核实", "review"},
		{"suffix", "退款已处理。\n已成功但未查询", "review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProOAIAnswerStatus("child@example.com", tc.answer); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
