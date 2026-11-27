package platform

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const MockName = "employee_management_status"
const MockDescription = "查询员工管理能力是否已接通。此工具只返回状态，不读写员工配置。"

func MockServer() *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "employee-management", Version: "1.0.0"}, nil)
	sdk.AddTool(server, &sdk.Tool{Name: MockName, Description: MockDescription},
		func(context.Context, *sdk.CallToolRequest, struct{}) (*sdk.CallToolResult, map[string]any, error) {
			return nil, map[string]any{"available": true, "writes_enabled": false, "message": "管理能力已获授权，实际员工编辑尚未开放。"}, nil
		})
	return server
}
