package demo

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/unrealm-ai/kikoteam/backend/internal/manager/mcp/assistant"
)

type Args struct {
	CaseID  string `json:"case_id"`
	Receipt string `json:"rollback_receipt,omitempty"`
	Window  string `json:"window,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

type Spec struct {
	Name, Description string
	Cases             []string
}

var orderCases = []string{"ORD-1042", "ORD-1087"}
var meetingCases = []string{"MEET-301"}
var incidentCases = []string{"INC-207"}

var Specs = []Spec{
	{"get_order", "客诉订单：按当前订单号查询订单和商品。只用于订单，不用于会议或告警。", orderCases},
	{"get_logistics", "客诉订单：按当前订单号查询物流和签收。只用于订单，不用于会议或告警。", orderCases},
	{"get_support_history", "客诉订单：按当前订单号查询已有客服记录。只用于订单，不用于会议或告警。", orderCases},
	{"get_meeting_notes", "会后跟进：按会议号查询会议决议、承诺和待定项。不用于查订单或告警。", meetingCases},
	{"get_release_plan", "会后跟进：按会议号查询发布里程碑和依赖。不用于查订单或支付告警。", meetingCases},
	{"list_project_chat", "会后跟进：按会议号查询会后项目群进展。不用于查订单或支付告警。", meetingCases},
	{"get_incident_logs", "支付告警：根据告警号和告警消息中的 trace_id 查询支付日志。不用于客诉订单或会议。", incidentCases},
	{"get_incident_metrics", "支付告警：按告警号查询监控。恢复后传回滚回执，并分别查询 window=1 和 window=2 两个五分钟窗口。", incidentCases},
	{"get_release_changes", "支付告警：按告警号查近期发布变更。不用于查订单或会议。", incidentCases},
	{"get_oncall_runbook", "支付告警：按告警号查处置条件、范围和恢复验收标准。不用于查订单或会议。", incidentCases},
	{"rollback_release", "支付告警：只有值班手册授权时才在样例中撤回对应灰度；返回复查所需回执。不操作真实系统。", incidentCases},
}

func (spec Spec) schema() map[string]any {
	props := map[string]any{
		"case_id": map[string]any{"type": "string", "enum": spec.Cases, "description": "须与当前子任务中的样例编号一致；不要改用另一个场景的编号。"},
	}
	required := []string{"case_id"}
	switch spec.Name {
	case "get_incident_logs":
		props["trace_id"] = map[string]any{"type": "string", "description": "告警消息给出的完整 trace ID。"}
		required = append(required, "trace_id")
	case "get_incident_metrics":
		props["rollback_receipt"] = map[string]any{"type": "string", "description": "回滚工具原样返回的回执。查询基线时不填。"}
		props["window"] = map[string]any{"type": "string", "enum": []string{"1", "2"}, "description": "回滚后分别指定两个窗口；查询基线时不填。"}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

var fixtures = map[string]map[string]string{
	"get_order":           {"ORD-1042": "便携投影仪，实付 629 元，订单选择星桥园区 1 号门驿站自提，客户已在 1 号门驿站，在线等回复。", "ORD-1087": "订单购买黑色 X2，拣货记录错误出库了银色 X1，周三签收。"},
	"get_logistics":       {"ORD-1042": "14:22 物流标为驿站代收，无代收人；交接照片的立牌是 7 号门，和订单的 1 号门不一致。7 号门尚未核实，不能断定已误投或丢失。", "ORD-1087": "周三 13:16 签收，重量与发货记录相符。"},
	"get_support_history": {"ORD-1042": "16:12 客户已在 1 号门驿站，工作人员核对货架和登记均未找到。客服已确认地址并承诺 16:50 前更新，不要让客户再找 1 号门。", "ORD-1087": "客户已上传型号照片、保留原包装，明早 08:00 活动要用，希望先换正确型号；尚未核实附近是否有货，不能承诺能及时换到。"},
	"get_meeting_notes":   {"MEET-301": "周三新人引导改版会：阿岚周四 12:00 前交定稿，小程周四 18:00 前交预览，晓宁周五 12:00 前做双端验收。周五 15:00 视验收结果判断是否放行。更新欢迎邮件留到下一期；发布后漏斗监控仍无人认领。"},
	"get_release_plan":    {"MEET-301": "周五 17:00 小流量上线是目标，不是批准。前置为定稿文案、正式预览和双端通过；安卓联调环境是前置，环境与各项交付的最新状态以会后项目群为准；15:00 按真实验收结论放行。"},
	"list_project_chat":   {"MEET-301": "14:18 小程已用占位文案搭骨架；14:22 晓宁先准备 iOS 用例，安卓环境在修；14:28 数据同学可答漏斗口径但未认领值守；14:35 晓宁更新：安卓联调环境已恢复并自检通过，双端验收按原定时间推进。阿岚、前端和测试均确认原交付时间不变。"},
	"get_release_changes": {"INC-207": "Android 签名 v3 灰度 02:02 开始，Android 支付成功率同时下跌；iOS、网关、支付供应商无异常。"},
	"get_oncall_runbook":  {"INC-207": "如果 Android 支付成功率在签名 v3 灰度后低于 95%，其他端正常，允许撤回这一灰度，无需另行确认。回滚后复查连续两个五分钟窗口，均高于 99% 才算恢复。"},
}

func receipt(secret, task string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("kiko-demo:INC-207:v3:" + task))
	return hex.EncodeToString(mac.Sum(nil))
}

func Run(secret, task, tool string, args Args) (map[string]any, error) {
	if text := fixtures[tool][args.CaseID]; text != "" {
		return map[string]any{"source": "样例数据", "case_id": args.CaseID, "result": text}, nil
	}
	if args.CaseID != "INC-207" || task == "" {
		return nil, fmt.Errorf("找不到这个样例编号")
	}
	out := map[string]any{"source": "样例数据", "case_id": args.CaseID}
	switch tool {
	case "get_incident_logs":
		if args.TraceID != "tr-pay-207-a81f" {
			return nil, fmt.Errorf("没有这个 trace 的样例日志")
		}
		out["result"] = "02:03:11 /pay/confirm: Android signer v3 校验失败，错误 SIGNATURE_MISMATCH；命中 release=android-signature-v3 灰度。"

	case "rollback_release":
		out["result"] = "已在样例中撤回 Android 签名 v3 灰度，未修改真实系统。"
		out["rollback_receipt"] = receipt(secret, task)
	case "get_incident_metrics":
		if args.Receipt == "" {
			out["result"] = "基线 02:05–02:10：Android 支付成功率 91.6%，iOS 99.7%，仅灰度 Android 异常。"
			break
		}
		if !hmac.Equal([]byte(args.Receipt), []byte(receipt(secret, task))) {
			return nil, fmt.Errorf("回滚回执无效或不属于当前任务")
		}
		switch args.Window {
		case "1":
			out["result"] = "窗口一 02:12–02:17：Android 支付成功率 99.4%，高于 99%。"
		case "2":
			out["result"] = "窗口二 02:17–02:22：Android 支付成功率 99.6%，高于 99%。"
		default:
			return nil, fmt.Errorf("请分别查询窗口 1 或 2")
		}
	default:
		return nil, fmt.Errorf("找不到这个样例编号")
	}
	return out, nil
}

func Server(secret string) *sdk.Server {
	s := sdk.NewServer(&sdk.Implementation{Name: "sample-operations", Version: "1.0.0"}, nil)
	for _, spec := range Specs {
		sdk.AddTool(s, &sdk.Tool{Name: spec.Name, Description: spec.Description, InputSchema: spec.schema()},
			func(ctx context.Context, _ *sdk.CallToolRequest, args Args) (*sdk.CallToolResult, map[string]any, error) {
				out, err := Run(secret, assistant.Config(ctx).TaskID, spec.Name, args)
				if err != nil {
					return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}}, nil, nil
				}
				return nil, out, nil
			})
	}
	return s
}
