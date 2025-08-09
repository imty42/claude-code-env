package executor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/imty42/claude-code-env/internal/config"
	"github.com/imty42/claude-code-env/internal/logger"
	"github.com/imty42/claude-code-env/internal/provider"
	"github.com/imty42/claude-code-env/internal/server_routing_manager"
)

// StartProxyService 启动代理服务（前台运行）
func StartProxyService() error {
	// 1. 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("加载配置失败: %v", err)
	}

	// 2. 初始化日志系统
	err = logger.InitLogger(cfg.LoggingLevel)
	if err != nil {
		return fmt.Errorf("初始化日志系统失败: %v", err)
	}
	defer logger.CloseLogger()

	// 3. 检测端口占用情况 (不允许多实例)
	llmProxyPortInUse := isPortInUse(cfg.CCEnvHost, cfg.LLMProxyPort)
	adminPortInUse := isPortInUse(cfg.CCEnvHost, cfg.AdminPort)
	
	if llmProxyPortInUse || adminPortInUse {
		var occupiedPorts []int
		if llmProxyPortInUse {
			occupiedPorts = append(occupiedPorts, cfg.LLMProxyPort)
		}
		if adminPortInUse {
			occupiedPorts = append(occupiedPorts, cfg.AdminPort)
		}
		
		logger.Error(logger.ModuleExecutor, "端口 %v 已被占用，无法启动服务", occupiedPorts)
		fmt.Printf("\n错误: 端口 %v 已被占用，无法启动代理服务\n", occupiedPorts)
		fmt.Printf("解决方案:\n")
		fmt.Printf("  1. 修改配置文件中的端口配置 (LLM_PROXY_PORT: %d, ADMIN_PORT: %d)\n", cfg.LLMProxyPort, cfg.AdminPort)
		fmt.Printf("  2. 或使用 'lsof -i' 查找并停止占用进程\n")

		return fmt.Errorf("端口 %v 已被占用", occupiedPorts)
	}

	logger.Info(logger.ModuleExecutor, "启动透明代理服务...")

	// 4. 创建配置监控器
	configWatcher, err := config.NewConfigWatcher()
	if err != nil {
		return fmt.Errorf("创建配置监控器失败: %v", err)
	}
	defer configWatcher.Stop()

	err = configWatcher.Start()
	if err != nil {
		return fmt.Errorf("启动配置监控失败: %v", err)
	}

	// 5. 创建 ProviderManager 和服务路由管理器
	providerManager := provider.NewProviderManager(cfg)
	routingManager := server_routing_manager.NewServerRoutingManager(
		providerManager,
		cfg.CCEnvHost,
		cfg.LLMProxyPort,
		cfg.AdminPort,
		cfg.APIProxy,
		time.Duration(cfg.APITimeoutMS)*time.Millisecond,
	)

	err = routingManager.Start()
	if err != nil {
		return fmt.Errorf("启动代理服务器失败: %v", err)
	}

	logger.Info(logger.ModuleExecutor, "透明代理服务已启动，按 Ctrl+C 停止")

	// 6. 监听信号和配置变化
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-sigChan:
			logger.Info(logger.ModuleExecutor, "正在关闭代理服务...")
			err = routingManager.Shutdown()
			if err != nil {
				return fmt.Errorf("关闭代理服务器失败: %v", err)
			}
			logger.Info(logger.ModuleExecutor, "代理服务已关闭")
			return nil

		case newConfig := <-configWatcher.GetReloadChan():
			logger.Info(logger.ModuleExecutor, "检测到配置文件变化，重启代理服务...")

			// 关闭旧服务
			err = routingManager.Shutdown()
			if err != nil {
				logger.Error(logger.ModuleExecutor, "关闭旧代理服务失败: %v", err)
			}

			// 重新初始化日志（可能日志级别改了）
			logger.CloseLogger()
			err = logger.InitLogger(newConfig.LoggingLevel)
			if err != nil {
				logger.Error(logger.ModuleExecutor, "重新初始化日志系统失败: %v", err)
				continue
			}

			// 创建新的 ProviderManager 和服务路由管理器
			providerManager = provider.NewProviderManager(newConfig)
			routingManager = server_routing_manager.NewServerRoutingManager(
				providerManager,
				newConfig.CCEnvHost,
				newConfig.LLMProxyPort,
				newConfig.AdminPort,
				newConfig.APIProxy,
				time.Duration(newConfig.APITimeoutMS)*time.Millisecond,
			)

			err = routingManager.Start()
			if err != nil {
				logger.Error(logger.ModuleExecutor, "重启代理服务器失败: %v", err)
				continue
			}

			logger.Info(logger.ModuleExecutor, "代理服务已重启完成")

		case err := <-configWatcher.GetErrorChan():
			logger.Error(logger.ModuleExecutor, "配置监控错误: %v", err)
		}
	}
}

// isPortInUse 检测端口是否已被占用
func isPortInUse(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), time.Second*2)
	if err != nil {
		return false // 端口空闲
	}
	conn.Close()
	return true // 端口被占用
}

// ExecuteClaudeWithProxy 使用代理模式执行 claude code 命令
func ExecuteClaudeWithProxy(args []string) error {
	// 1. 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("加载配置失败: %v", err)
	}

	// 2. 初始化日志系统
	err = logger.InitLogger(cfg.LoggingLevel)
	if err != nil {
		return fmt.Errorf("初始化日志系统失败: %v", err)
	}
	defer logger.CloseLogger()

	// 3. 检测LLM代理端口是否已被占用
	llmProxyPortInUse := isPortInUse(cfg.CCEnvHost, cfg.LLMProxyPort)
	
	var routingManager *server_routing_manager.ServerRoutingManager
	var configWatcher *config.ConfigWatcher

	if llmProxyPortInUse {
		// 端口被占用，直接使用现有服务
		logger.Info(logger.ModuleExecutor, "检测到LLM代理端口 %d 被占用，直接使用现有代理服务", cfg.LLMProxyPort)
	} else {
		// 端口空闲，启动代理服务
		logger.Info(logger.ModuleExecutor, "启动新的透明代理服务...")

		// 4. 创建配置监控器
		configWatcher, err = config.NewConfigWatcher()
		if err != nil {
			return fmt.Errorf("创建配置监控器失败: %v", err)
		}
		defer func() {
			if configWatcher != nil {
				configWatcher.Stop()
			}
		}()

		err = configWatcher.Start()
		if err != nil {
			return fmt.Errorf("启动配置监控失败: %v", err)
		}

		// 5. 创建 ProviderManager 和服务路由管理器
		providerManager := provider.NewProviderManager(cfg)
		routingManager = server_routing_manager.NewServerRoutingManager(
			providerManager,
			cfg.CCEnvHost,
			cfg.LLMProxyPort,
			cfg.AdminPort,
			cfg.APIProxy,
			time.Duration(cfg.APITimeoutMS)*time.Millisecond,
		)

		err = routingManager.Start()
		if err != nil {
			return fmt.Errorf("启动代理服务器失败: %v", err)
		}

		// 确保代理服务器在程序退出时关闭
		defer routingManager.Shutdown()

		logger.Info(logger.ModuleExecutor, "透明代理服务已启动")

		// 6. 在后台处理配置变化
		go func() {
			for {
				select {
				case newConfig := <-configWatcher.GetReloadChan():
					logger.Info(logger.ModuleExecutor, "检测到配置文件变化，重启代理服务...")

					// 关闭旧服务
					err := routingManager.Shutdown()
					if err != nil {
						logger.Error(logger.ModuleExecutor, "关闭旧代理服务失败: %v", err)
					}

					// 重新初始化日志（可能日志级别改了）
					logger.CloseLogger()
					err = logger.InitLogger(newConfig.LoggingLevel)
					if err != nil {
						logger.Error(logger.ModuleExecutor, "重新初始化日志系统失败: %v", err)
						continue
					}

					// 创建新的 ProviderManager 和服务路由管理器
					providerManager = provider.NewProviderManager(newConfig)
					routingManager = server_routing_manager.NewServerRoutingManager(
						providerManager,
						newConfig.CCEnvHost,
						newConfig.LLMProxyPort,
						newConfig.AdminPort,
						newConfig.APIProxy,
						time.Duration(newConfig.APITimeoutMS)*time.Millisecond,
					)

					err = routingManager.Start()
					if err != nil {
						logger.Error(logger.ModuleExecutor, "重启代理服务器失败: %v", err)
						continue
					}

					logger.Info(logger.ModuleExecutor, "代理服务已重启完成")

				case err := <-configWatcher.GetErrorChan():
					logger.Error(logger.ModuleExecutor, "配置监控错误: %v", err)
				}
			}
		}()
	}

	// 7. 启动 claude 命令
	// 直接将用户参数传给 claude，不包含 "code"
	cmd := exec.Command("claude", args...)

	// 设置环境变量（指向LLM代理端口）
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ANTHROPIC_BASE_URL=http://%s:%d", cfg.CCEnvHost, cfg.LLMProxyPort),
		"ANTHROPIC_AUTH_TOKEN=dummy-token", // 代理会替换为真实token
	)

	// 添加API代理环境变量（用于claude code本身的网络请求）
	if cfg.APIProxy != "" {
		if strings.HasPrefix(cfg.APIProxy, "http://") {
			cmd.Env = append(cmd.Env, "HTTP_PROXY="+cfg.APIProxy)
		} else if strings.HasPrefix(cfg.APIProxy, "https://") {
			cmd.Env = append(cmd.Env, "HTTPS_PROXY="+cfg.APIProxy)
		}
	}

	// 设置标准输入、输出和错误
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	logger.Info(logger.ModuleExecutor, "启动 claude code...")

	// 8. 执行 claude code 命令并等待结束
	err = cmd.Run()

	// 处理不同的退出状态
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode := exitError.ExitCode()

			// 127: 命令未找到
			if exitCode == 127 {
				logger.Error(logger.ModuleExecutor, "claude code 命令未找到")
				return fmt.Errorf("claude code 命令未找到，请确保 Claude Code 已正确安装")
			}

			// 1, 2, 130: 用户主动退出 - 静默处理
			if exitCode == 1 || exitCode == 2 || exitCode == 130 {
				logger.Info(logger.ModuleExecutor, "claude code 正常退出")
				return nil
			}

			// 其他非零退出码
			logger.Error(logger.ModuleExecutor, "claude code 异常退出 (退出码: %d)", exitCode)
			return fmt.Errorf("claude code 命令异常退出 (退出码: %d)", exitCode)
		}
	}

	logger.Info(logger.ModuleExecutor, "claude code 执行完成")
	return err
}

// ShowConfig 显示当前配置信息
func ShowConfig() error {
	// 加载配置
	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("加载配置失败: %v", err)
	}

	// 显示配置信息
	cfg.DisplayConfig()

	return nil
}

// ExecuteClaudeWithConfig 使用配置执行 claude 命令 (保留兼容性，暂时不用)
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

// ShowLogs 显示日志文件，支持所有 tail 参数
func ShowLogs(args []string) error {
	// 1. 获取日志文件路径
	// 使用与其他函数相同的路径获取方式
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("获取用户主目录失败: %v", err)
	}

	logPath := fmt.Sprintf("%s/.claude-code-env/ccenv.log", homeDir)

	// 2. 检查文件是否存在
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return fmt.Errorf("日志文件不存在: %s\n提示：请先运行 'ccenv start' 或 'ccenv code' 生成日志", logPath)
	}

	// 3. 构建命令：tail [用户参数...] 日志文件路径
	cmdArgs := append(args, logPath)
	cmd := exec.Command("tail", cmdArgs...)

	// 4. 设置标准输入、输出和错误
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// 5. 执行命令
	return cmd.Run()
}
