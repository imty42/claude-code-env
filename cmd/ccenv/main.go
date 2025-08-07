package main

import (
	"fmt"
	"os"

	"github.com/imty42/claude-code-env/internal/config"
	"github.com/imty42/claude-code-env/internal/executor"
)

// Version 程序版本，构建时注入
var Version = "dev"

const HELP_TEXT = `
用法: ccenv [command] [args...]

命令:
  start         启动透明代理服务（前台运行）
  code [args]   启动 claude code（自动管理代理服务），支持参数透传
  config        显示当前配置文件信息
  -v, version   显示版本信息  
  -h, help      显示帮助信息

示例:
  ccenv start                   # 启动代理服务，用于测试
  ccenv code                    # 启动 claude code 交互模式
  ccenv code "分析这个项目"      # 启动 claude code 并传递初始消息
  ccenv code --help             # 查看 claude code 帮助信息
  ccenv config                  # 查看当前配置
`

func main() {
	args := os.Args[1:] // 排除程序名

	// 检查参数
	if len(args) == 0 {
		fmt.Print(HELP_TEXT)
		os.Exit(1)
	}

	command := args[0]

	switch command {
	case "start":
		// 检查配置文件
		if err := ensureConfig(); err != nil {
			fmt.Printf("配置错误: %v\n", err)
			os.Exit(1)
		}

		// 启动代理服务（前台运行）
		err := executor.StartProxyService()
		if err != nil {
			fmt.Printf("启动代理服务失败: %v\n", err)
			os.Exit(1)
		}

	case "code":
		// 检查配置文件
		if err := ensureConfig(); err != nil {
			fmt.Printf("配置错误: %v\n", err)
			os.Exit(1)
		}

		// 提取 code 之后的参数
		claudeArgs := args[1:] // 跳过 "code" 参数本身

		// 使用代理模式启动 claude code
		err := executor.ExecuteClaudeWithProxy(claudeArgs)
		if err != nil {
			fmt.Printf("启动失败: %v\n", err)
			os.Exit(1)
		}

	case "config":
		// 显示配置信息
		err := executor.ShowConfig()
		if err != nil {
			fmt.Printf("显示配置失败: %v\n", err)
			os.Exit(1)
		}

	case "-v", "version":
		fmt.Printf("ccenv version: %s\n", Version)

	case "-h", "help":
		fmt.Print(HELP_TEXT)

	default:
		fmt.Printf("未知命令: %s\n", command)
		fmt.Print(HELP_TEXT)
		os.Exit(1)
	}
}

// ensureConfig 确保配置文件存在
func ensureConfig() error {
	_, err := config.LoadConfig()
	if err != nil {
		if config.CreateConfigIfNeeded() {
			// 重新尝试读取配置
			_, err = config.LoadConfig()
			if err != nil {
				return fmt.Errorf("创建配置文件后仍无法读取: %v", err)
			}
		} else {
			fmt.Printf("请完成配置 ~/.claude-code-env/settings.json\n")
			config.ShowExampleConfig()
			return fmt.Errorf("配置文件不存在")
		}
	}
	return nil
}