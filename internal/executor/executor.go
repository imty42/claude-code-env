package executor

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/imty42/claude-code-env/internal/config"
)

// ExecuteClaudeWithConfig 使用配置执行 claude 命令
func ExecuteClaudeWithConfig(serviceConfig config.ServiceConfig, verbose bool) error {
	// 获取用户的默认 shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh" // 默认使用 zsh
	}
	
	// 构建命令：先加载配置文件，然后执行 claude
	// 使用 -i 确保是交互式 shell，这样能正确加载 alias
	cmdStr := "source ~/.zshrc && claude"
	cmd := exec.Command(shell, "-i", "-c", cmdStr)

	// 设置环境变量
	cmd.Env = os.Environ()
	for key, value := range serviceConfig {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}

	// 设置标准输入、输出和错误
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// verbose 模式下显示详细信息
	if verbose {
		fmt.Println("=== Verbose Mode ===")
		fmt.Printf("执行命令: %s -i -c \"%s\"\n", shell, cmdStr)
		fmt.Println("设置的环境变量:")
		for key, value := range serviceConfig {
			fmt.Printf("  %s=%s\n", key, value)
		}
		fmt.Println("==================")
	}

	// 执行命令并等待结束
	err := cmd.Run()
	
	// 处理不同的退出状态
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()
			
			// 127: 命令未找到 - 需要报告错误
			if exitCode == 127 {
				return fmt.Errorf("claude 命令未找到，请确保 Claude Code 已正确安装")
			}
			
			// 1, 2, 130: 用户主动退出 (Ctrl+C, 正常退出等) - 静默处理
			if exitCode == 1 || exitCode == 2 || exitCode == 130 {
				return nil
			}
			
			// 其他非零退出码 - 报告错误但提供更友好的信息
			return fmt.Errorf("claude 命令异常退出 (退出码: %d)", exitCode)
		}
	}
	
	return err
}