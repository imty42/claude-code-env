package main

import (
	"fmt"
	"os"

	"github.com/imty42/claude-code-env/internal/config"
	"github.com/imty42/claude-code-env/internal/executor"
)

// Version 程序版本，构建时注入
var Version = "dev"

func main() {
	// 首先检查和加载配置文件
	settings, err := config.LoadSettings()
	if err != nil {
		if config.CreateConfigIfNeeded() {
			// 重新尝试读取配置
			settings, err = config.LoadSettings()
			if err != nil {
				fmt.Printf("创建配置文件后仍无法读取: %v\n", err)
				os.Exit(1)
			}
		} else {
			fmt.Printf("请在完成配置 ~/.claude-code-env/settings.json\n")
			config.ShowExampleConfig()
			os.Exit(1)
		}
	}

	// 解析命令行参数
	var serviceName string
	var verbose bool

	args := os.Args[1:] // 排除程序名
	
	// 检查参数数量
	if len(args) < 1 || len(args) > 2 {
		fmt.Println("用法: cce [--verbose|-v] service-name 或 cce service-name [--verbose|-v]")
		fmt.Println("可用的服务配置:")
		for name := range settings {
			fmt.Printf("  - %s\n", name)
		}
		os.Exit(1)
	}

	// 解析参数
	for _, arg := range args {
		if arg == "--verbose" || arg == "-v" {
			verbose = true
		} else {
			if serviceName != "" {
				fmt.Println("错误: 只能指定一个服务名称")
				os.Exit(1)
			}
			serviceName = arg
		}
	}

	if serviceName == "" {
		fmt.Println("错误: 必须指定服务名称")
		fmt.Println("用法: cce [--verbose|-v] service-name 或 cce service-name [--verbose|-v]")
		fmt.Println("可用的服务配置:")
		for name := range settings {
			fmt.Printf("  - %s\n", name)
		}
		os.Exit(1)
	}

	// 查找对应的服务配置
	serviceConfig, exists := settings[serviceName]
	if !exists {
		fmt.Printf("服务 '%s' 在配置中不存在\n", serviceName)
		fmt.Println("可用的服务配置:")
		for name := range settings {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Printf("\n请在 ~/.claude-code-env/settings.json 中添加 '%s' 的配置\n", serviceName)
		config.ShowExampleConfig()
		os.Exit(1)
	}

	// 设置环境变量并执行 claude 命令
	err = executor.ExecuteClaudeWithConfig(serviceConfig, verbose)
	if err != nil {
		fmt.Printf("启动 claude 命令失败: %v\n", err)
		os.Exit(1)
	}
}