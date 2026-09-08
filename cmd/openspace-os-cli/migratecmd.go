package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/openspace-os/openspace-os-core/internal/core"
)

// newMigrateCmd 创建 migrate 子命令（数据迁移工具，T1.7）。
func newMigrateCmd() *cobra.Command {
	var (
		sqlitePath string
		dsn        string
		batch      int
	)
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "数据迁移工具（SQLite → PostgreSQL）",
		Long:  "将存量 SQLite 中的 nodes 与 relationships 数据**可选**导入 PostgreSQL（T1.7）。",
	}

	importCmd := &cobra.Command{
		Use:   "import",
		Short: "将 SQLite 数据导入 PostgreSQL",
		Example: "  aos migrate import --sqlite ./data/openspace-os.db \\\n" +
			"    --dsn 'postgres://aos:aos_secret@localhost:5432/openspace?sslmode=disable'",
		RunE: func(cmd *cobra.Command, args []string) error {
			if sqlitePath == "" || dsn == "" {
				return fmt.Errorf("--sqlite 与 --dsn 均必填")
			}

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()

			fmt.Printf("打开 SQLite: %s\n", sqlitePath)
			src, err := core.InitDB(sqlitePath)
			if err != nil {
				return fmt.Errorf("打开 SQLite 失败: %w", err)
			}
			defer src.Close()

			fmt.Println("连接 PostgreSQL...")
			dst, err := core.InitPostgresDB(ctx, dsn)
			if err != nil {
				return fmt.Errorf("打开 PostgreSQL 失败: %w", err)
			}
			defer dst.Close()

			stats, err := core.MigrateSQLiteToPostgres(ctx, src, dst, core.MigrationOptions{BatchSize: batch})
			if err != nil {
				return err
			}

			fmt.Printf("导入完成：节点新建 %d / 跳过 %d；关系新建 %d / 跳过 %d\n",
				stats.NodesNew, stats.NodesSkipped, stats.RelsNew, stats.RelsSkipped)
			fmt.Println("（跳过项为目标库已存在的主键，幂等可重复执行）")
			return nil
		},
	}
	importCmd.Flags().StringVar(&sqlitePath, "sqlite", "", "SQLite 数据库文件路径（必填）")
	importCmd.Flags().StringVar(&dsn, "dsn", "", "PostgreSQL 连接串（必填）")
	importCmd.Flags().IntVar(&batch, "batch", 500, "每批提交行数")

	cmd.AddCommand(importCmd)
	return cmd
}