package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/imty42/claude-code-env/internal/config"
	"github.com/imty42/claude-code-env/internal/executor"
	"github.com/spf13/cobra"
)

// Version 程序版本，构建时注入
var Version = "2.0.0"

// rootCmd 根命令
var rootCmd = &cobra.Command{
	Use:   "ccenv",
	Short: "Claude Code Environment - 透明代理管理工具",
	Long: `Claude Code Environment (ccenv) 是一个透明代理管理工具，
用于自动管理 Claude Code 的代理服务连接。`,
	Version: Version,
	CompletionOptions: cobra.CompletionOptions{
		HiddenDefaultCmd: true,
	},
}

// startCmd 启动代理服务命令
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "启动透明代理服务（前台运行）",
	Long:  "启动透明代理服务，用于测试。服务将在前台运行，按 Ctrl+C 可停止服务。",
	Run: func(cmd *cobra.Command, args []string) {
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
	},
}

// codeCmd 启动 claude code 命令
var codeCmd = &cobra.Command{
	Use:   "code [args...]",
	Short: "启动 claude code（自动管理代理服务）",
	Long: `启动 claude code 交互模式，自动管理代理服务。
支持所有 claude code 的参数透传。

示例:
  ccenv code                    # 启动交互模式
  ccenv code "分析这个项目"      # 传递初始消息
  ccenv code --help             # 查看 claude code 帮助`,
	Run: func(cmd *cobra.Command, args []string) {
		// 检查配置文件
		if err := ensureConfig(); err != nil {
			fmt.Printf("配置错误: %v\n", err)
			os.Exit(1)
		}

		// 使用代理模式启动 claude code
		err := executor.ExecuteClaudeWithProxy(args)
		if err != nil {
			fmt.Printf("启动失败: %v\n", err)
			os.Exit(1)
		}
	},
	DisableFlagParsing: true, // 禁用标志解析，允许参数透传
}

// logsCmd 查看日志命令
var logsCmd = &cobra.Command{
	Use:   "logs [options...]",
	Short: "查看日志文件",
	Long: `查看代理服务的日志文件，支持所有 tail 命令的参数。

示例:
  ccenv logs                    # 查看最近的日志
  ccenv logs -f                 # 实时跟踪日志
  ccenv logs -n 50              # 查看最后50行日志`,
	Run: func(cmd *cobra.Command, args []string) {
		// 检查配置文件
		if err := ensureConfig(); err != nil {
			fmt.Printf("配置错误: %v\n", err)
			os.Exit(1)
		}

		// 显示日志，支持所有 tail 参数
		err := executor.ShowLogs(args)
		if err != nil {
			fmt.Printf("显示日志失败: %v\n", err)
			os.Exit(1)
		}
	},
	DisableFlagParsing: true, // 禁用标志解析，允许参数透传
	ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		// 为 logs 命令提供 tail 命令的常用参数补全
		var suggestions []string

		// 根据输入情况提供不同的补全建议
		if len(args) == 0 || toComplete == "-" {
			// 提供主要的 tail 选项
			suggestions = []string{
				"-f",
				"-n",
				"-c",
				"--help",
			}
		} else if len(args) > 0 {
			lastArg := args[len(args)-1]
			if lastArg == "-n" || lastArg == "-c" {
				// 为 -n 和 -c 参数提供常用数字
				suggestions = []string{"10", "20", "50", "100", "200", "500"}
			}
		}

		return suggestions, cobra.ShellCompDirectiveNoFileComp
	},
}

// configCmd 显示配置命令
var configCmd = &cobra.Command{
	Use:   "config",
	Short: "显示当前配置文件信息",
	Long:  "显示当前的配置文件信息和状态。",
	Run: func(cmd *cobra.Command, args []string) {
		// 显示配置信息
		err := executor.ShowConfig()
		if err != nil {
			fmt.Printf("显示配置失败: %v\n", err)
			os.Exit(1)
		}
	},
}

// createCompletionCmd 创建自动补全命令
func createCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [shell]",
		Short: "生成自动补全脚本",
		Long: `生成指定shell的自动补全脚本

支持的shell: bash, zsh, fish, powershell

安装示例:
  Bash: ccenv completion bash > ~/.bash_completion && source ~/.bash_completion
  Zsh:  ccenv completion zsh > ~/.zsh/completions/_ccenv
  Fish: ccenv completion fish > ~/.config/fish/completions/ccenv.fish`,
		DisableFlagsInUseLine: true,
		ValidArgs:             []string{"bash", "zsh", "fish", "powershell"},
		Args:                  cobra.ExactValidArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			switch args[0] {
			case "bash":
				rootCmd.GenBashCompletion(os.Stdout)
			case "zsh":
				rootCmd.GenZshCompletion(os.Stdout)
			case "fish":
				rootCmd.GenFishCompletion(os.Stdout, true)
			case "powershell":
				rootCmd.GenPowerShellCompletion(os.Stdout)
			}
		},
	}
}

// createSetupCmd 创建 setup 命令
func createSetupCmd() *cobra.Command {
	setupCmd := &cobra.Command{
		Use:   "setup",
		Short: "初始化和配置 ccenv 环境",
		Long: `初始化和配置 ccenv 环境。

此命令用于确保配置文件正确创建，并设置 shell 自动补全功能。
`,
	}

	// 添加 completion 子命令
	setupCmd.AddCommand(createSetupCompletionCmd())

	return setupCmd
}

// createSetupCompletionCmd 创建 setup completion 子命令
func createSetupCompletionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "completion",
		Short: "配置 shell 自动补全功能",
		Long: `配置 shell 自动补全功能。

使用方法:
  ccenv setup completion

该命令会自动检测当前使用的 shell 并安装相应的自动补全脚本。
支持的 shell: bash, zsh, fish

自动补全功能可以让你在使用 ccenv 命令时通过按 Tab 键来自动补全命令和参数。
`,
		Run: func(cmd *cobra.Command, args []string) {
			shell := getCurrentShell()
			if shell == "" {
				fmt.Println("无法检测到当前使用的 shell，请手动指定 shell 类型:")
				fmt.Println("  ccenv completion bash > ~/.bash_completion && source ~/.bash_completion  # for Bash")
				fmt.Println("  ccenv completion zsh > ~/.zsh/completions/_ccenv                       # for Zsh")
				fmt.Println("  ccenv completion fish > ~/.config/fish/completions/ccenv.fish          # for Fish")
				return
			}

			// 根据检测到的 shell 安装补全
			err := installCompletion(shell)
			if err != nil {
				fmt.Printf("安装自动补全失败: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("已成功为 %s shell 安装自动补全功能！\n", shell)
			fmt.Println("请重新启动终端或运行以下命令以启用补全功能:")

			switch shell {
			case "bash":
				fmt.Println("  source ~/.bash_completion")
			case "zsh":
				fmt.Println("  source ~/.zshrc")
			case "fish":
				fmt.Println("  source ~/.config/fish/config.fish")
			}
		},
	}
}

// getCurrentShell 获取当前使用的 shell
func getCurrentShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return ""
	}

	// 从路径中提取 shell 名称
	parts := strings.Split(shell, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return shell
}

// installCompletion 安装自动补全
func installCompletion(shell string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("获取用户主目录失败: %v", err)
	}

	var completionScript bytes.Buffer
	var completionFile string

	// 生成补全脚本
	switch shell {
	case "bash":
		rootCmd.GenBashCompletion(&completionScript)
		completionFile = filepath.Join(homeDir, ".bash_completion")
	case "zsh":
		rootCmd.GenZshCompletion(&completionScript)
		completionDir := filepath.Join(homeDir, ".zsh", "completions")
		// 创建目录
		err := os.MkdirAll(completionDir, 0755)
		if err != nil {
			return fmt.Errorf("创建目录失败: %v", err)
		}
		completionFile = filepath.Join(completionDir, "_ccenv")
	case "fish":
		rootCmd.GenFishCompletion(&completionScript, true)
		completionDir := filepath.Join(homeDir, ".config", "fish", "completions")
		// 创建目录
		err := os.MkdirAll(completionDir, 0755)
		if err != nil {
			return fmt.Errorf("创建目录失败: %v", err)
		}
		completionFile = filepath.Join(completionDir, "ccenv.fish")
	default:
		return fmt.Errorf("不支持的 shell: %s", shell)
	}

	// 将补全脚本写入文件
	err = os.WriteFile(completionFile, completionScript.Bytes(), 0644)
	if err != nil {
		return fmt.Errorf("写入补全文件失败: %v", err)
	}

	// 对于 bash，确保 .bashrc 或 .bash_profile 中包含了自动加载
	if shell == "bash" {
		err = ensureBashCompletionLoaded(homeDir)
		if err != nil {
			return fmt.Errorf("配置 bash 自动加载失败: %v", err)
		}
	}

	return nil
}

// ensureBashCompletionLoaded 确保 bash 自动加载补全脚本
func ensureBashCompletionLoaded(homeDir string) error {
	// 检查 .bashrc
	bashrcPath := filepath.Join(homeDir, ".bashrc")
	bashrcContent, err := os.ReadFile(bashrcPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	loadLine := "source ~/.bash_completion"
	if !strings.Contains(string(bashrcContent), loadLine) {
		// 添加加载行到 .bashrc
		newContent := string(bashrcContent) + "\n" + loadLine + "\n"
		err = os.WriteFile(bashrcPath, []byte(newContent), 0644)
		if err != nil {
			return err
		}
	}

	return nil
}

func init() {
	// 添加所有子命令
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(codeCmd)
	rootCmd.AddCommand(logsCmd)
	rootCmd.AddCommand(configCmd)

	// 添加自动补全命令
	rootCmd.AddCommand(createCompletionCmd())

	// 添加 setup 命令
	rootCmd.AddCommand(createSetupCmd())

	// 设置版本信息
	rootCmd.SetVersionTemplate("ccenv version: {{.Version}}\n")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
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
