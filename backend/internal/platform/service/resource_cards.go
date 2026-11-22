package service

import "strings"

// ResourceCard is release-owned display copy. It is independent of immutable skill and plugin versions.
type ResourceCard struct {
	Category   string `json:"category"`
	SummaryZH  string `json:"summary_zh"`
	SummaryEN  string `json:"summary_en"`
	Highlights string `json:"highlights"`
}

var cardCopy = map[string]map[string]*ResourceCard{
	"skill": {
		"tavily-search":       {"research", "用已授权的 Tavily 工具查找近期资讯、核对原文，并在回答中引用来源。", "Use the connected Tavily tools to find recent news, check original pages, and cite sources.", "时效检索、原文核实、来源引用"},
		"status-report":       {"office", "从进展、阻塞和待办中提炼本周变化，标出需要决策的风险。", "Turn project updates, blockers, and next steps into a weekly report with risks that need a decision.", "项目周报、风险清单"},
		"market-close":        {"market", "将带时间戳的行情整理为收盘简报，标明交易日、数据来源与缺失项。", "Turn timestamped quotes into a market-close brief that identifies the trading date, sources, and missing data.", "收盘简报"},
		"code-review":         {"dev", "阅读代码差异，按影响程度整理问题、证据与修改建议。", "Review code changes and organize findings, evidence, and suggested fixes by impact.", "PR 评审意见"},
		"minimax-xlsx":        {"market", "合并工作表，核验公式和关键数值，整理需要复核的单元格。", "Merge worksheets, validate formulas and key values, and flag cells for review.", "合并工作簿、校验清单"},
		"synthesize-research": {"research", "按主题归纳反馈，保留代表性原话和需要跟进的问题。", "Group feedback by theme, keeping representative quotes and questions for follow-up.", "反馈主题摘要"},
		"pptx-generator":      {"editor", "将材料组织成面向汇报对象的演示文稿，突出结论及依据。", "Turn source material into a presentation tailored to its audience, highlighting conclusions and evidence.", "汇报演示文稿"},
		"reconciliation":      {"market", "逐项比对账表，定位差额和未匹配记录。", "Compare financial records and locate discrepancies and unmatched entries.", "对账差异表"},
		"kb-article":          {"sales", "将重复咨询和已确认答复整理为可复用的知识条目。", "Turn recurring questions and verified answers into reusable support articles.", "FAQ 条目"},
		"competitive-brief":   {"research", "比较竞品变化和行业动态，整理可追溯的结论。", "Compare competitor changes and industry developments in a source-backed brief.", "行业简报"},
		"draft-content":       {"editor", "根据受众和渠道调整同一主题的写法，生成可编辑初稿。", "Adapt a topic for different audiences and channels in editable drafts.", "多渠道文案"},
	},
	"plugin": {
		"tavily":          {"research", "使用 Tavily API Key 搜索网页、提取正文和检索站点内容。", "Use a Tavily API key to search the web, extract pages, and explore websites.", "网页搜索、正文提取、站点地图与抓取"},
		"stock":           {"market", "查询上证、深证、创业板指数和个股快照，附报价时间。", "Look up the three major mainland China stock indices and individual stock quotes with timestamps.", "三大指数、个股行情快照"},
		"github":          {"dev", "使用个人访问令牌（PAT）或网页授权，读取仓库的 PR、Issue、代码差异和提交记录。", "Connect with a personal access token (PAT) or OAuth to read pull requests, issues, diffs, and commits in authorized repositories.", "PR 与 Issue 查询、代码差异、提交记录"},
		"feishu":          {"office", "搜索并读取应用有权访问的飞书文档，提取正文和出处。", "Find and read Feishu documents the connected app can access, with source references.", "文档检索、内容读取"},
		"hithink":         {"market", "查询 A 股现价、历史走势和财务指标，供行情简报引用。", "Retrieve current A-share prices, historical quotes, and financial indicators for market reports.", "行情快照、历史行情、财务指标"},
		"tencent_meeting": {"office", "按会议查找录制、转写和智能纪要，回溯讨论内容。", "Find recordings, transcripts, and AI minutes for completed Tencent Meetings.", "会议记录、转写与纪要"},
		"exa":             {"research", "检索公开网页并返回来源，方便核对引用。", "Search public web pages and return source links for verification.", "公开网页检索"},
		"zhipu":           {"research", "检索公开网络资料，为待核实的问题寻找最新来源。", "Search the public web for current sources to check a claim.", "联网搜索"},
		"dingtalk":        {"office", "按条件查询待办事项，查看任务详情和子任务。", "Look up to-dos by criteria and inspect task details and subtasks.", "待办查询、任务详情"},
		"fxiaoke":         {"sales", "查询授权范围内的客户、商机和跟进记录。", "Look up customers, sales opportunities, and follow-up records within the connected account’s scope.", "客户与商机查询、跟进记录"},
		"amap":            {"sales", "查询门店位置、距离及驾车路线，安排客户拜访。", "Find store locations, distances, and driving routes to plan customer visits.", "地点查询、距离与路线"},
	},
}

func BuiltinResourceCard(kind, key string) *ResourceCard {
	return cardCopy[kind][key]
}

func MatchingCardKeys(kind, query string) []string {
	var keys []string
	if query == "" {
		return keys
	}
	term := strings.ToLower(query)
	for key, c := range cardCopy[kind] {
		if strings.Contains(strings.ToLower(c.SummaryZH+" "+c.SummaryEN+" "+c.Highlights), term) {
			keys = append(keys, key)
		}
	}
	return keys
}
