// Package main 是 Openspace OS CLI 工具的入口。
//
// 提供与 Openspace OS Core 服务交互的命令行工具，基于 cobra 实现完整的子命令体系，
// 涵盖节点管理、关系管理、事件、指令、遥测、插件、认证等全部 REST API 能力。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// apiV1 是 REST API 的统一前缀。
const apiV1 = "/api/v1"

// 全局参数变量，由 root 命令的 PersistentFlags 绑定。
var (
	serverAddr string
	token      string
	outputFmt  string
)

func main() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		PrintError(err)
		os.Exit(1)
	}
}

// newRootCmd 创建根命令并挂载所有子命令与全局参数。
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "aos",
		Short: "Openspace OS 命令行工具",
		Long:  "Openspace OS CLI —— 与 Openspace OS Core 服务交互的命令行工具。",
		// 运行时错误（如连接失败）不打印 usage，由 main 统一输出错误信息
		SilenceUsage: true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&serverAddr, "server", "s", "http://localhost:8080", "Openspace OS Core 服务地址")
	root.PersistentFlags().StringVarP(&token, "token", "t", "", "JWT token（也可通过环境变量 OPENSPACE_TOKEN 设置）")
	root.PersistentFlags().StringVarP(&outputFmt, "output", "o", "json", "输出格式（json/table）")

	root.AddCommand(newStatusCmd())
	root.AddCommand(newNodeCmd())
	root.AddCommand(newRelationshipCmd())
	root.AddCommand(newEventCmd())
	root.AddCommand(newCommandCmd())
	root.AddCommand(newTelemetryCmd())
	root.AddCommand(newPluginCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newClientCmd())
	root.AddCommand(newMigrateCmd())

	return root
}

// getClient 根据全局参数构造 HTTP 客户端，优先使用 --token，其次读取环境变量 OPENSPACE_TOKEN。
func getClient() *Client {
	t := token
	if t == "" {
		t = os.Getenv("OPENSPACE_TOKEN")
	}
	return NewClient(serverAddr, t)
}

// -------------------- 状态 --------------------

// newStatusCmd 创建 status 子命令，查询 Core 服务健康状态。
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "查询 Openspace OS Core 服务状态",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get("/healthz")
			if err != nil {
				return fmt.Errorf("连接 Openspace OS Core 失败: %w", err)
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
}

// -------------------- 节点管理 --------------------

// newNodeCmd 创建 node 父命令及其子命令。
func newNodeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "节点管理",
	}
	cmd.AddCommand(newNodeRegisterCmd())
	cmd.AddCommand(newNodeListCmd())
	cmd.AddCommand(newNodeShowCmd())
	cmd.AddCommand(newNodeUpdateCmd())
	cmd.AddCommand(newNodeDeleteCmd())
	cmd.AddCommand(newNodeRelateCmd())
	cmd.AddCommand(newNodeGraphCmd())
	return cmd
}

// newNodeRegisterCmd 创建 node register 子命令。
func newNodeRegisterCmd() *cobra.Command {
	var (
		nodeType     string
		name         string
		nodeID       string
		community    string
		status       string
		shardKey     string
		federationID string
	)
	cmd := &cobra.Command{
		Use:   "register",
		Short: "注册新节点",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{
				"nodeId":           nodeID,
				"nodeType":         nodeType,
				"name":             name,
				"status":           status,
				"ownerCommunityId": community,
			}
			if shardKey != "" {
				req["shardKey"] = shardKey
			}
			if federationID != "" {
				req["federationId"] = federationID
			}
			resp, err := getClient().Post(apiV1+"/nodes", req)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			printAny(result,
				[]string{"NodeID", "Type", "Name", "Status", "Community"},
				[]string{strField(result, "nodeId"), strField(result, "nodeType"), strField(result, "name"), strField(result, "status"), strField(result, "ownerCommunityId")},
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeType, "type", "", "节点类型（必填）")
	cmd.Flags().StringVar(&name, "name", "", "节点名称（必填）")
	cmd.Flags().StringVar(&nodeID, "id", "", "节点 ID（必填）")
	cmd.Flags().StringVar(&community, "community", "", "所属社区 ID（必填）")
	cmd.Flags().StringVar(&status, "status", "", "节点状态（必填）")
	cmd.Flags().StringVar(&shardKey, "shard-key", "", "分片键（选填）")
	cmd.Flags().StringVar(&federationID, "federation-id", "", "联邦标识（选填）")
	_ = cmd.MarkFlagRequired("type")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("id")
	_ = cmd.MarkFlagRequired("community")
	_ = cmd.MarkFlagRequired("status")
	return cmd
}

// newNodeListCmd 创建 node list 子命令。
func newNodeListCmd() *cobra.Command {
	var (
		nodeType  string
		community string
		status    string
		limit     int
		offset    int
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出节点",
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if nodeType != "" {
				q.Set("nodeType", nodeType)
			}
			if community != "" {
				q.Set("communityId", community)
			}
			if status != "" {
				q.Set("status", status)
			}
			if limit > 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if offset > 0 {
				q.Set("offset", strconv.Itoa(offset))
			}
			path := apiV1 + "/nodes"
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			resp, err := getClient().Get(path)
			if err != nil {
				return err
			}
			var nodes []map[string]any
			if err := readResponse(resp, &nodes); err != nil {
				return err
			}
			headers := []string{"NodeID", "Type", "Name", "Status", "Community"}
			rows := make([][]string, 0, len(nodes))
			for _, n := range nodes {
				rows = append(rows, []string{
					strField(n, "nodeId"),
					strField(n, "nodeType"),
					strField(n, "name"),
					strField(n, "status"),
					strField(n, "ownerCommunityId"),
				})
			}
			printList(nodes, headers, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&nodeType, "type", "", "按节点类型过滤")
	cmd.Flags().StringVar(&community, "community", "", "按所属社区 ID 过滤")
	cmd.Flags().StringVar(&status, "status", "", "按状态过滤")
	cmd.Flags().IntVar(&limit, "limit", 0, "最大返回数量")
	cmd.Flags().IntVar(&offset, "offset", 0, "分页偏移量")
	return cmd
}

// newNodeShowCmd 创建 node show 子命令。
func newNodeShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <nodeId>",
		Short: "查询单个节点",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/nodes/" + url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
}

// newNodeUpdateCmd 创建 node update 子命令。
func newNodeUpdateCmd() *cobra.Command {
	var (
		name         string
		status       string
		shardKey     string
		federationID string
		properties   string
	)
	cmd := &cobra.Command{
		Use:   "update <nodeId>",
		Short: "更新节点",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			changes := map[string]any{}
			if name != "" {
				changes["name"] = name
			}
			if status != "" {
				changes["status"] = status
			}
			if shardKey != "" {
				changes["shardKey"] = shardKey
			}
			if federationID != "" {
				changes["federationId"] = federationID
			}
			if properties != "" {
				var props map[string]any
				if err := json.Unmarshal([]byte(properties), &props); err != nil {
					return fmt.Errorf("--properties 不是有效的 JSON: %w", err)
				}
				changes["properties"] = props
			}
			if len(changes) == 0 {
				return fmt.Errorf("至少需要指定一个要更新的字段（--name/--status/--shard-key/--federation-id/--properties）")
			}
			resp, err := getClient().Put(apiV1+"/nodes/"+url.PathEscape(args[0]), changes)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "新名称")
	cmd.Flags().StringVar(&status, "status", "", "新状态")
	cmd.Flags().StringVar(&shardKey, "shard-key", "", "新分片键")
	cmd.Flags().StringVar(&federationID, "federation-id", "", "新联邦标识")
	cmd.Flags().StringVar(&properties, "properties", "", "属性 JSON（选填）")
	return cmd
}

// newNodeDeleteCmd 创建 node delete 子命令。
func newNodeDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <nodeId>",
		Short: "删除节点",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Delete(apiV1 + "/nodes/" + url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			if err := readResponse(resp, nil); err != nil {
				return err
			}
			fmt.Printf("节点 %s 已删除\n", args[0])
			return nil
		},
	}
}

// newNodeRelateCmd 创建 node relate 子命令。
func newNodeRelateCmd() *cobra.Command {
	var (
		toNodeID string
		relType  string
	)
	cmd := &cobra.Command{
		Use:   "relate <fromNodeId>",
		Short: "建立节点间关系",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{
				"toNodeId": toNodeID,
				"relType":  relType,
			}
			resp, err := getClient().Post(apiV1+"/nodes/"+url.PathEscape(args[0])+"/relationships", req)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			printAny(result,
				[]string{"RelID", "From", "To", "Type"},
				[]string{strField(result, "relId"), strField(result, "fromNodeId"), strField(result, "toNodeId"), strField(result, "relType")},
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&toNodeID, "to", "", "目标节点 ID（必填）")
	cmd.Flags().StringVar(&relType, "type", "", "关系类型（必填）")
	_ = cmd.MarkFlagRequired("to")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// newNodeGraphCmd 创建 node graph 子命令。
func newNodeGraphCmd() *cobra.Command {
	var (
		direction string
		depth     int
		relType   string
	)
	cmd := &cobra.Command{
		Use:   "graph <nodeId>",
		Short: "查询节点关系图",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			q := url.Values{}
			if direction != "" {
				q.Set("direction", direction)
			}
			if depth > 0 {
				q.Set("depth", strconv.Itoa(depth))
			}
			if relType != "" {
				q.Set("relType", relType)
			}
			path := apiV1 + "/nodes/" + url.PathEscape(args[0]) + "/graph"
			if enc := q.Encode(); enc != "" {
				path += "?" + enc
			}
			resp, err := getClient().Get(path)
			if err != nil {
				return err
			}
			var rels []map[string]any
			if err := readResponse(resp, &rels); err != nil {
				return err
			}
			headers := []string{"RelID", "From", "To", "Type"}
			rows := make([][]string, 0, len(rels))
			for _, r := range rels {
				rows = append(rows, []string{
					strField(r, "relId"),
					strField(r, "fromNodeId"),
					strField(r, "toNodeId"),
					strField(r, "relType"),
				})
			}
			printList(rels, headers, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&direction, "direction", "out", "遍历方向（out/in/both）")
	cmd.Flags().IntVar(&depth, "depth", 1, "最大遍历深度")
	cmd.Flags().StringVar(&relType, "rel-type", "", "限定关系类型")
	return cmd
}

// -------------------- 关系管理 --------------------

// newRelationshipCmd 创建 relationship 父命令及其子命令。
func newRelationshipCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "relationship",
		Short: "关系管理",
	}
	cmd.AddCommand(newRelationshipDeleteCmd())
	return cmd
}

// newRelationshipDeleteCmd 创建 relationship delete 子命令。
func newRelationshipDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <relId>",
		Short: "删除关系",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Delete(apiV1 + "/relationships/" + url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			if err := readResponse(resp, nil); err != nil {
				return err
			}
			fmt.Printf("关系 %s 已删除\n", args[0])
			return nil
		},
	}
}

// -------------------- 事件 --------------------

// newEventCmd 创建 event 父命令及其子命令。
func newEventCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "event",
		Short: "事件管理",
	}
	cmd.AddCommand(newEventWatchCmd())
	cmd.AddCommand(newEventReplayCmd())
	cmd.AddCommand(newEventSchemasCmd())
	return cmd
}

// newEventWatchCmd 创建 event watch 子命令，实时订阅 SSE 事件流。
func newEventWatchCmd() *cobra.Command {
	var eventTypes string
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "实时订阅事件（SSE）",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := apiV1 + "/events/subscribe"
			if eventTypes != "" {
				q := url.Values{}
				q.Set("types", eventTypes)
				path += "?" + q.Encode()
			}
			body, err := getClient().Stream(path)
			if err != nil {
				return err
			}
			defer body.Close()

			scanner := bufio.NewScanner(body)
			// 增大缓冲区，避免长事件被截断
			scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for scanner.Scan() {
				line := scanner.Text()
				switch {
				case strings.HasPrefix(line, "data: "):
					fmt.Println(strings.TrimPrefix(line, "data: "))
				case strings.HasPrefix(line, ":"):
					// SSE 注释行，忽略
				case line == "":
					// 空行分隔，忽略
				default:
					fmt.Println(line)
				}
			}
			return scanner.Err()
		},
	}
	cmd.Flags().StringVar(&eventTypes, "type", "", "事件类型过滤（逗号分隔）")
	return cmd
}

// newEventReplayCmd 创建 event replay 子命令。
func newEventReplayCmd() *cobra.Command {
	var (
		eventTypes string
		source     string
		fromTime   string
		toTime     string
		limit      int
	)
	cmd := &cobra.Command{
		Use:   "replay",
		Short: "回放历史事件",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{}
			if eventTypes != "" {
				var types []string
				for _, t := range strings.Split(eventTypes, ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						types = append(types, t)
					}
				}
				if len(types) > 0 {
					req["eventTypes"] = types
				}
			}
			if source != "" {
				req["sourceNodeId"] = source
			}
			if fromTime != "" {
				req["startTime"] = fromTime
			}
			if toTime != "" {
				req["endTime"] = toTime
			}
			if limit > 0 {
				req["limit"] = limit
			}
			resp, err := getClient().Post(apiV1+"/events/replay", req)
			if err != nil {
				return err
			}
			var events []map[string]any
			if err := readResponse(resp, &events); err != nil {
				return err
			}
			headers := []string{"EventID", "Type", "Source", "Timestamp"}
			rows := make([][]string, 0, len(events))
			for _, e := range events {
				rows = append(rows, []string{
					strField(e, "eventId"),
					strField(e, "eventType"),
					strField(e, "sourceNodeId"),
					strField(e, "timestamp"),
				})
			}
			printList(events, headers, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&eventTypes, "type", "", "事件类型过滤（逗号分隔）")
	cmd.Flags().StringVar(&source, "source", "", "事件来源节点 ID")
	cmd.Flags().StringVar(&fromTime, "from", "", "起始时间（RFC3339 格式）")
	cmd.Flags().StringVar(&toTime, "to", "", "结束时间（RFC3339 格式）")
	cmd.Flags().IntVar(&limit, "limit", 100, "最大返回数量")
	return cmd
}

// newEventSchemasCmd 创建 event schemas 子命令。
func newEventSchemasCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schemas",
		Short: "列出所有事件 Schema",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/schemas")
			if err != nil {
				return err
			}
			var schemas []map[string]any
			if err := readResponse(resp, &schemas); err != nil {
				return err
			}
			headers := []string{"EventType", "Version", "Fields"}
			rows := make([][]string, 0, len(schemas))
			for _, s := range schemas {
				fields := "0"
				if arr, ok := s["fields"].([]any); ok {
					fields = strconv.Itoa(len(arr))
				}
				rows = append(rows, []string{
					strField(s, "eventType"),
					strField(s, "version"),
					fields,
				})
			}
			printList(schemas, headers, rows)
			return nil
		},
	}
}

// -------------------- 指令 --------------------

// newCommandCmd 创建 command 父命令及其子命令。
func newCommandCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "command",
		Short: "指令管理",
	}
	cmd.AddCommand(newCommandSendCmd())
	cmd.AddCommand(newCommandStatusCmd())
	cmd.AddCommand(newCommandListCmd())
	cmd.AddCommand(newCommandCancelCmd())
	return cmd
}

// newCommandSendCmd 创建 command send 子命令。
func newCommandSendCmd() *cobra.Command {
	var (
		satID    string
		cmdType  string
		priority int
		params   string
	)
	cmd := &cobra.Command{
		Use:   "send",
		Short: "发送遥控指令",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]any{
				"satelliteId":  satID,
				"commandType":  cmdType,
				"priority":     priority,
			}
			if params != "" {
				var paramMap map[string]any
				if err := json.Unmarshal([]byte(params), &paramMap); err != nil {
					return fmt.Errorf("--params 不是有效的 JSON: %w", err)
				}
				req["parameters"] = paramMap
			}
			resp, err := getClient().Post(apiV1+"/commands", req)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			printAny(result,
				[]string{"CommandID", "Status"},
				[]string{strField(result, "commandId"), strField(result, "status")},
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&satID, "sat", "", "目标卫星 ID（必填）")
	cmd.Flags().StringVar(&cmdType, "type", "", "指令类型（必填）")
	cmd.Flags().IntVar(&priority, "priority", 0, "优先级（数值越大越优先）")
	cmd.Flags().StringVar(&params, "params", "", "指令参数 JSON（选填）")
	_ = cmd.MarkFlagRequired("sat")
	_ = cmd.MarkFlagRequired("type")
	return cmd
}

// newCommandStatusCmd 创建 command status 子命令。
func newCommandStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status <commandId>",
		Short: "查询指令状态",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/commands/" + url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
}

// newCommandListCmd 创建 command list 子命令。
func newCommandListCmd() *cobra.Command {
	var status string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "列出指令",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := apiV1 + "/commands"
			if status != "" {
				q := url.Values{}
				q.Set("status", status)
				path += "?" + q.Encode()
			}
			resp, err := getClient().Get(path)
			if err != nil {
				return err
			}
			var commands []map[string]any
			if err := readResponse(resp, &commands); err != nil {
				return err
			}
			headers := []string{"CommandID", "Satellite", "Type", "Priority", "Status"}
			rows := make([][]string, 0, len(commands))
			for _, c := range commands {
				rows = append(rows, []string{
					strField(c, "commandId"),
					strField(c, "satelliteId"),
					strField(c, "commandType"),
					strField(c, "priority"),
					strField(c, "status"),
				})
			}
			printList(commands, headers, rows)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "按状态过滤")
	return cmd
}

// newCommandCancelCmd 创建 command cancel 子命令。
func newCommandCancelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <commandId>",
		Short: "取消指令",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Delete(apiV1 + "/commands/" + url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
}

// -------------------- 遥测 --------------------

// newTelemetryCmd 创建 telemetry 父命令及其子命令。
func newTelemetryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "telemetry",
		Short: "遥测管理",
	}
	cmd.AddCommand(newTelemetryParsersCmd())
	cmd.AddCommand(newTelemetrySendCmd())
	return cmd
}

// newTelemetryParsersCmd 创建 telemetry parsers 子命令。
func newTelemetryParsersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "parsers",
		Short: "列出遥测解析器",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/telemetry/parsers")
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			// 构造表格行：解析器列表与当前活跃解析器
			var parserList []string
			if arr, ok := result["parsers"].([]any); ok {
				for _, p := range arr {
					parserList = append(parserList, fmt.Sprintf("%v", p))
				}
			}
			if isTableOutput() {
				rows := [][]string{
					{strings.Join(parserList, ", ")},
					{strField(result, "active")},
				}
				PrintTable([]string{"Parsers", "Active"}, transposeRows(rows))
				return nil
			}
			PrintJSON(result)
			return nil
		},
	}
}

// transposeRows 将按行组织的数据转置为两列对齐的表格行。
// 输入 [[parsers], [active]] 输出 [[parsers-value, active-value]]。
func transposeRows(rows [][]string) [][]string {
	if len(rows) == 0 {
		return nil
	}
	maxLen := 0
	for _, r := range rows {
		if len(r) > maxLen {
			maxLen = len(r)
		}
	}
	out := make([][]string, maxLen)
	for i := 0; i < maxLen; i++ {
		out[i] = make([]string, len(rows))
		for j, r := range rows {
			if i < len(r) {
				out[i][j] = r[i]
			}
		}
	}
	return out
}

// newTelemetrySendCmd 创建 telemetry send 子命令。
func newTelemetrySendCmd() *cobra.Command {
	var data string
	cmd := &cobra.Command{
		Use:   "send",
		Short: "手动发送遥测数据",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]string{"data": data}
			resp, err := getClient().Post(apiV1+"/telemetry/send", req)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&data, "data", "", "遥测数据（JSON 字符串，必填）")
	_ = cmd.MarkFlagRequired("data")
	return cmd
}

// -------------------- 插件 --------------------

// newPluginCmd 创建 plugin 父命令及其子命令。
func newPluginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "插件管理",
	}
	cmd.AddCommand(newPluginListCmd())
	cmd.AddCommand(newPluginUnloadCmd())
	return cmd
}

// newPluginListCmd 创建 plugin list 子命令。
func newPluginListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出已加载插件",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/plugins")
			if err != nil {
				return err
			}
			var plugins []map[string]any
			if err := readResponse(resp, &plugins); err != nil {
				return err
			}
			headers := []string{"Name", "Version", "Status"}
			rows := make([][]string, 0, len(plugins))
			for _, p := range plugins {
				rows = append(rows, []string{
					nestedStr(p, "Manifest", "name"),
					nestedStr(p, "Manifest", "version"),
					strField(p, "Status"),
				})
			}
			printList(plugins, headers, rows)
			return nil
		},
	}
}

// newPluginUnloadCmd 创建 plugin unload 子命令。
func newPluginUnloadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unload <name>",
		Short: "卸载插件",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Post(apiV1+"/plugins/"+url.PathEscape(args[0])+"/unload", nil)
			if err != nil {
				return err
			}
			if err := readResponse(resp, nil); err != nil {
				return err
			}
			fmt.Printf("插件 %s 已卸载\n", args[0])
			return nil
		},
	}
}

// -------------------- 认证 --------------------

// newAuthCmd 创建 auth 父命令及其子命令。
func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "认证管理",
	}
	cmd.AddCommand(newAuthLoginCmd())
	cmd.AddCommand(newAuthMeCmd())
	return cmd
}

// newAuthLoginCmd 创建 auth login 子命令。
func newAuthLoginCmd() *cobra.Command {
	var (
		username string
		password string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "登录并获取 JWT token",
		RunE: func(cmd *cobra.Command, args []string) error {
			req := map[string]string{
				"username": username,
				"password": password,
			}
			resp, err := getClient().Post(apiV1+"/auth/login", req)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
	cmd.Flags().StringVar(&username, "username", "", "用户名（必填）")
	cmd.Flags().StringVar(&password, "password", "", "密码（必填）")
	_ = cmd.MarkFlagRequired("username")
	_ = cmd.MarkFlagRequired("password")
	return cmd
}

// newAuthMeCmd 创建 auth me 子命令。
func newAuthMeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "me",
		Short: "显示当前用户信息",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/auth/me")
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			PrintJSON(result)
			return nil
		},
	}
}

// -------------------- 辅助函数 --------------------

// strField 从 map 中安全提取字符串字段，不存在时返回空串。
func strField(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// nestedStr 按多层 key 路径从嵌套 map 中提取字符串值。
// 例如 nestedStr(m, "Manifest", "name") 等价于 m["Manifest"].(map)["name"]。
func nestedStr(m map[string]any, keys ...string) string {
	cur := m
	for i, k := range keys {
		if i == len(keys)-1 {
			return strField(cur, k)
		}
		next, ok := cur[k].(map[string]any)
		if !ok {
			return ""
		}
		cur = next
	}
	return ""
}
