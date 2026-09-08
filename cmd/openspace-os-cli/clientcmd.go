package main

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

// newClientCmd 创建 client 父命令及其子命令，用于管理机器接入方（Client）与 API Key。
func newClientCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "client",
		Short: "机器接入方（Client）管理",
		Long:  "管理与 Core 对接的机器接入方（模块/租户）：创建并获取 API Key、列出、吊销。",
	}
	cmd.AddCommand(newClientCreateCmd())
	cmd.AddCommand(newClientListCmd())
	cmd.AddCommand(newClientRevokeCmd())
	return cmd
}

// newClientCreateCmd 创建 client create 子命令，创建机器接入方并返回明文 API Key。
func newClientCreateCmd() *cobra.Command {
	var (
		name        string
		moduleName  string
		communityID string
		roles       string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "创建机器接入方并生成 API Key",
		RunE: func(cmd *cobra.Command, args []string) error {
			body := map[string]any{"name": name}
			if moduleName != "" {
				body["moduleName"] = moduleName
			}
			if communityID != "" {
				body["communityId"] = communityID
			}
			if roles != "" {
				var roleList []string
				for _, r := range strings.Split(roles, ",") {
					r = strings.TrimSpace(r)
					if r != "" {
						roleList = append(roleList, r)
					}
				}
				if len(roleList) > 0 {
					body["roles"] = roleList
				}
			}

			resp, err := getClient().Post(apiV1+"/clients", body)
			if err != nil {
				return err
			}
			var result map[string]any
			if err := readResponse(resp, &result); err != nil {
				return err
			}
			printAny(result,
				[]string{"ClientID", "Name", "Module", "Community", "Roles", "APIKey"},
				[]string{
					strField(result, "clientId"),
					strField(result, "name"),
					strField(result, "moduleName"),
					strField(result, "communityId"),
					strField(result, "roles"),
					strField(result, "apiKey"),
				},
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "客户端名称（必填）")
	cmd.Flags().StringVar(&moduleName, "module", "", "所属模块名（选填）")
	cmd.Flags().StringVar(&communityID, "community", "", "所属社区/租户 ID（选填）")
	cmd.Flags().StringVar(&roles, "roles", "operator", "角色列表，逗号分隔（默认 operator）")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

// newClientListCmd 创建 client list 子命令，列出所有机器接入方。
func newClientListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "列出所有机器接入方",
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Get(apiV1 + "/clients")
			if err != nil {
				return err
			}
			var clients []map[string]any
			if err := readResponse(resp, &clients); err != nil {
				return err
			}
			headers := []string{"ClientID", "Name", "Module", "Community", "Roles", "Status"}
			rows := make([][]string, 0, len(clients))
			for _, c := range clients {
				rows = append(rows, []string{
					strField(c, "clientId"),
					strField(c, "name"),
					strField(c, "moduleName"),
					strField(c, "communityId"),
					strField(c, "roles"),
					strField(c, "status"),
				})
			}
			printList(clients, headers, rows)
			return nil
		},
	}
}

// newClientRevokeCmd 创建 client revoke 子命令，吊销机器接入方。
func newClientRevokeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <clientId>",
		Short: "吊销机器接入方",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			resp, err := getClient().Delete(apiV1 + "/clients/" + url.PathEscape(args[0]))
			if err != nil {
				return err
			}
			if err := readResponse(resp, nil); err != nil {
				return err
			}
			fmt.Printf("客户端 %s 已吊销\n", args[0])
			return nil
		},
	}
}