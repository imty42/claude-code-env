package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/imty42/claude-code-env/internal/config"
	"github.com/imty42/claude-code-env/internal/logger"
	"github.com/imty42/claude-code-env/internal/proxy"
	"github.com/imty42/claude-code-env/internal/provider"
)

// ClientRequest 客户端请求数据结构
type ClientRequest struct {
	ClientID string `json:"client_id"`
	PID      int    `json:"pid"`
	Hostname string `json:"hostname"`
}

// ClientResponse 客户端响应数据结构
type ClientResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

// generateClientID 生成客户端ID
func generateClientID() string {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	
	pid := os.Getpid()
	timestamp := time.Now().Unix()
	
	return fmt.Sprintf("ccenv_%s_%d_%d", hostname, pid, timestamp)
}

// sendClientRequest 发送客户端请求到代理服务器
func sendClientRequest(host string, port int, endpoint string, req ClientRequest) (*ClientResponse, error) {
	url := fmt.Sprintf("http://%s:%d/ccenv/%s", host, port, endpoint)
	
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %v", err)
	}
	
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()
	
	var clientResp ClientResponse
	if err := json.NewDecoder(resp.Body).Decode(&clientResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}
	
	return &clientResp, nil
}

// registerClient 注册客户端
func registerClient(host string, port int, clientID string) error {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	
	req := ClientRequest{
		ClientID: clientID,
		PID:      os.Getpid(),
		Hostname: hostname,
	}
	
	resp, err := sendClientRequest(host, port, "register", req)
	if err != nil {
		return err
	}
	
	if !resp.Success {
		return fmt.Errorf("注册失败: %s", resp.Message)
	}
	
	// 移除重复的注册成功日志，proxy层已经记录了
	return nil
}

// unregisterClient 注销客户端
func unregisterClient(host string, port int, clientID string) error {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	
	req := ClientRequest{
		ClientID: clientID,
		PID:      os.Getpid(),
		Hostname: hostname,
	}
	
	resp, err := sendClientRequest(host, port, "unregister", req)
	if err != nil {
		logger.Warn(logger.ModuleExecutor, "注销请求失败: %v", err)
		return err
	}
	
	if !resp.Success {
		logger.Warn(logger.ModuleExecutor, "注销失败: %s", resp.Message)
		return fmt.Errorf("注销失败: %s", resp.Message)
	}
	
	// 移除重复的注销成功日志，proxy层已经记录了
	return nil
}

// startHeartbeat 启动心跳
func startHeartbeat(host string, port int, clientID string, stopChan <-chan bool) {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	
	req := ClientRequest{
		ClientID: clientID,
		PID:      os.Getpid(),
		Hostname: hostname,
	}
	
	ticker := time.NewTicker(30 * time.Second) // 每30秒发送一次心跳
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			resp, err := sendClientRequest(host, port, "heartbeat", req)
			if err != nil {
				logger.Warn(logger.ModuleExecutor, "心跳请求失败: %v", err)
				continue
			}
			
			if !resp.Success {
				logger.Warn(logger.ModuleExecutor, "心跳失败: %s", resp.Message)
				// 如果心跳失败，可能是客户端未注册，尝试重新注册
				if err := registerClient(host, port, clientID); err != nil {
					logger.Error(logger.ModuleExecutor, "重新注册失败: %v", err)
				}
			}
			// 心跳成功时不记录日志，避免噪音
			
		case <-stopChan:
			logger.Debug(logger.ModuleExecutor, "心跳服务已停止")
			return
		}
	}
}

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

	// 3. 检测端口占用情况
	if isPortInUse(cfg.CCEnvHost, cfg.CCEnvPort) {
		// 端口被占用，获取占用进程信息
		process, err := getPortProcess(cfg.CCEnvHost, cfg.CCEnvPort)
		if err != nil {
			logger.Error(logger.ModuleExecutor, "端口 %d 已被占用，但无法获取进程信息: %v", cfg.CCEnvPort, err)
			fmt.Printf("\n错误: 端口 %d 已被占用，但无法获取进程详情\n", cfg.CCEnvPort)
			fmt.Printf("解决方案:\n")
			fmt.Printf("  1. 修改配置文件 ~/.claude-code-env/settings.json 中的 CCENV_PORT 为其他端口\n")
			fmt.Printf("  2. 或使用 'lsof -i :%d' 查找并停止占用进程\n", cfg.CCEnvPort)
			return fmt.Errorf("端口 %d 已被占用", cfg.CCEnvPort)
		}
		
		// 无论是否为 ccenv 进程，start 命令都应该报错（避免重复启动）
		logger.Error(logger.ModuleExecutor, "端口 %d 已被进程占用 (PID: %d, 进程: %s)", cfg.CCEnvPort, process.PID, process.Name)
		
		fmt.Printf("\n错误: 端口 %d 已被占用，无法启动代理服务\n\n", cfg.CCEnvPort)
		fmt.Printf("占用进程信息:\n")
		fmt.Printf("  PID: %d\n", process.PID)
		fmt.Printf("  进程: %s\n", process.Name)
		fmt.Printf("  命令: %s\n", process.Command)
		fmt.Printf("  用户: %s\n\n", process.User)
		
		if isCCEnvProcess(process) {
			fmt.Printf("提示: 检测到已有 ccenv 进程在运行\n")
			fmt.Printf("  - 使用 'ccenv code' 复用现有代理服务\n")
			fmt.Printf("  - 或停止现有进程: kill %d\n", process.PID)
		} else {
			fmt.Printf("解决方案:\n")
			fmt.Printf("  1. 修改配置文件 ~/.claude-code-env/settings.json 中的 CCENV_PORT 为其他端口\n")
			fmt.Printf("  2. 或停止占用进程: kill %d\n", process.PID)
		}
		
		return fmt.Errorf("端口 %d 已被占用", cfg.CCEnvPort)
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

	// 5. 创建 ProviderManager 和代理服务器
	providerManager := provider.NewProviderManager(cfg)
	proxyServer := proxy.NewProxyServer(
		providerManager,
		cfg.CCEnvPort,
		cfg.APIProxy,
	)

	err = proxyServer.Start()
	if err != nil {
		return fmt.Errorf("启动代理服务器失败: %v", err)
	}

	logger.Info(logger.ModuleExecutor, "透明代理服务已启动，按 Ctrl+C 停止")
	
	// 6. 监听信号和配置变化
	sigChan := make(chan os.Signal, 1)
	shutdownChan := make(chan bool, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 设置当所有客户端断开时的关闭回调
	proxyServer.SetShutdownCallback(func() {
		// 延迟5秒以防止误关闭（给客户端重连时间）
		logger.Info(logger.ModuleExecutor, "所有客户端断开，5秒后自动关闭代理服务...")
		time.Sleep(5 * time.Second)
		if proxyServer.GetClientCount() == 0 {
			logger.Info(logger.ModuleExecutor, "确认所有ccenv code客户端已断开，自动关闭代理服务")
			shutdownChan <- true
		} else {
			logger.Info(logger.ModuleExecutor, "检测到新客户端连接，取消自动关闭")
		}
	})

	for {
		select {
		case <-sigChan:
			logger.Info(logger.ModuleExecutor, "正在关闭代理服务...")
			err = proxyServer.Shutdown()
			if err != nil {
				return fmt.Errorf("关闭代理服务器失败: %v", err)
			}
			logger.Info(logger.ModuleExecutor, "代理服务已关闭")
			return nil

		case <-shutdownChan:
			logger.Info(logger.ModuleExecutor, "自动关闭代理服务...")
			err = proxyServer.Shutdown()
			if err != nil {
				return fmt.Errorf("关闭代理服务器失败: %v", err)
			}
			logger.Info(logger.ModuleExecutor, "代理服务已关闭")
			return nil

		case newConfig := <-configWatcher.GetReloadChan():
			logger.Info(logger.ModuleExecutor, "检测到配置文件变化，重启代理服务...")
			
			// 关闭旧服务
			err = proxyServer.Shutdown()
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

			// 创建新的 ProviderManager 和代理服务器
			providerManager = provider.NewProviderManager(newConfig)
			proxyServer = proxy.NewProxyServer(
				providerManager,
				newConfig.CCEnvPort,
				newConfig.APIProxy,
			)

			err = proxyServer.Start()
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

// PortProcess 端口占用进程信息
type PortProcess struct {
	PID     int    // 进程ID
	Name    string // 进程名
	Command string // 完整命令
	User    string // 用户
}

// getPortProcess 获取占用指定端口的进程信息
func getPortProcess(host string, port int) (*PortProcess, error) {
	// 使用 lsof 命令获取端口占用信息
	cmd := exec.Command("lsof", "-i", fmt.Sprintf(":%d", port), "-n", "-P")
	output, err := cmd.Output()
	if err != nil {
		// lsof 失败，尝试使用 netstat
		return getPortProcessByNetstat(port)
	}

	// 解析 lsof 输出
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "LISTEN") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				// 解析 PID
				pid, err := strconv.Atoi(fields[1])
				if err != nil {
					continue
				}
				
				return &PortProcess{
					PID:     pid,
					Name:    fields[0],
					Command: getProcessCommand(pid),
					User:    getProcessUser(fields),
				}, nil
			}
		}
	}
	
	return nil, fmt.Errorf("未找到占用端口 %d 的进程", port)
}

// getPortProcessByNetstat 使用 netstat 获取进程信息（备用方法）
func getPortProcessByNetstat(port int) (*PortProcess, error) {
	cmd := exec.Command("netstat", "-tlnp")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("无法获取端口占用信息: %v", err)
	}

	// 解析 netstat 输出，查找监听指定端口的进程
	lines := strings.Split(string(output), "\n")
	portPattern := fmt.Sprintf(":%d ", port)
	
	for _, line := range lines {
		if strings.Contains(line, portPattern) && strings.Contains(line, "LISTEN") {
			// 提取进程信息
			re := regexp.MustCompile(`(\d+)/(.+)`)
			matches := re.FindStringSubmatch(line)
			if len(matches) >= 3 {
				pid, err := strconv.Atoi(matches[1])
				if err != nil {
					continue
				}
				
				return &PortProcess{
					PID:     pid,
					Name:    matches[2],
					Command: getProcessCommand(pid),
					User:    "unknown",
				}, nil
			}
		}
	}
	
	return nil, fmt.Errorf("未找到占用端口 %d 的进程", port)
}

// getProcessCommand 获取进程的完整命令行
func getProcessCommand(pid int) string {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

// getProcessUser 从 lsof 输出字段中提取用户信息
func getProcessUser(fields []string) string {
	if len(fields) >= 3 {
		return fields[2]
	}
	return "unknown"
}

// isCCEnvProcess 判断进程是否为 ccenv
func isCCEnvProcess(process *PortProcess) bool {
	if process == nil {
		return false
	}
	
	// 检查进程名或命令中是否包含 ccenv
	name := strings.ToLower(process.Name)
	command := strings.ToLower(process.Command)
	
	return strings.Contains(name, "ccenv") || strings.Contains(command, "ccenv")
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

	// 生成客户端ID
	clientID := generateClientID()
	
	// 3. 检测端口占用情况并注册客户端
	proxyExists := isPortInUse(cfg.CCEnvHost, cfg.CCEnvPort)
	
	if proxyExists {
		// 端口被占用，获取占用进程信息
		process, err := getPortProcess(cfg.CCEnvHost, cfg.CCEnvPort)
		if err != nil {
			// 无法获取进程信息，但端口确实被占用
			logger.Error(logger.ModuleExecutor, "端口 %d 已被占用，但无法获取进程信息: %v", cfg.CCEnvPort, err)
			return fmt.Errorf("端口 %d 已被占用，请检查并处理占用进程", cfg.CCEnvPort)
		}
		
		if isCCEnvProcess(process) {
			// 是 ccenv 进程，可以复用，注册客户端
			logger.Info(logger.ModuleExecutor, "检测到端口 %d 已被 ccenv 进程占用 (PID: %d)，复用现有代理服务", cfg.CCEnvPort, process.PID)
			
			// 尝试注册客户端到现有服务器
			if err := registerClient(cfg.CCEnvHost, cfg.CCEnvPort, clientID); err != nil {
				logger.Warn(logger.ModuleExecutor, "注册到现有代理服务失败: %v，将启动新的代理服务", err)
				proxyExists = false // 如果注册失败，当作没有代理服务处理
			} else {
				logger.Info(logger.ModuleExecutor, "成功注册到现有代理服务")
			}
		} else {
			// 不是 ccenv 进程，需要用户处理
			logger.Error(logger.ModuleExecutor, "端口 %d 已被其他进程占用", cfg.CCEnvPort)
			fmt.Printf("\n错误: 端口 %d 已被其他进程占用\n\n", cfg.CCEnvPort)
			fmt.Printf("占用进程信息:\n")
			fmt.Printf("  PID: %d\n", process.PID)
			fmt.Printf("  进程: %s\n", process.Name)
			fmt.Printf("  命令: %s\n", process.Command)
			fmt.Printf("  用户: %s\n\n", process.User)
			fmt.Printf("解决方案:\n")
			fmt.Printf("  1. 修改配置文件 ~/.claude-code-env/settings.json 中的 CCENV_PORT 为其他端口\n")
			fmt.Printf("  2. 或停止占用进程: kill %d\n", process.PID)
			
			return fmt.Errorf("端口冲突，需要用户处理")
		}
	}

	var proxyServer *proxy.ProxyServer
	var configWatcher *config.ConfigWatcher
	var heartbeatStopChan chan bool

	if !proxyExists {
		// 端口空闲或注册失败，启动完整的代理服务
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

		// 5. 创建 ProviderManager 和代理服务器
		providerManager := provider.NewProviderManager(cfg)
		proxyServer = proxy.NewProxyServer(
			providerManager,
			cfg.CCEnvPort,
			cfg.APIProxy,
		)

		err = proxyServer.Start()
		if err != nil {
			return fmt.Errorf("启动代理服务器失败: %v", err)
		}

		// 确保代理服务器在程序退出时关闭
		defer proxyServer.Shutdown()
		
		// 注册客户端到新启动的代理服务器
		if err := registerClient(cfg.CCEnvHost, cfg.CCEnvPort, clientID); err != nil {
			logger.Warn(logger.ModuleExecutor, "注册到新启动的代理服务失败: %v", err)
		} else {
			logger.Info(logger.ModuleExecutor, "成功注册到新启动的代理服务")
		}

		// 6. 在后台处理配置变化
		go func() {
			for {
				select {
				case newConfig := <-configWatcher.GetReloadChan():
					logger.Info(logger.ModuleExecutor, "检测到配置文件变化，重启代理服务...")
					
					// 关闭旧服务
					err := proxyServer.Shutdown()
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

					// 创建新的 ProviderManager 和代理服务器
					providerManager = provider.NewProviderManager(newConfig)
					proxyServer = proxy.NewProxyServer(
						providerManager,
						newConfig.CCEnvPort,
						newConfig.APIProxy,
					)

					err = proxyServer.Start()
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

	// 启动心跳机制
	heartbeatStopChan = make(chan bool, 1)
	go startHeartbeat(cfg.CCEnvHost, cfg.CCEnvPort, clientID, heartbeatStopChan)

	// 确保客户端在程序退出时注销
	defer func() {
		// 停止心跳
		close(heartbeatStopChan)
		
		// 注销客户端
		if err := unregisterClient(cfg.CCEnvHost, cfg.CCEnvPort, clientID); err != nil {
			logger.Warn(logger.ModuleExecutor, "客户端注销失败: %v", err)
		}
	}()

	// 6. 启动 claude 命令
	// 直接将用户参数传给 claude，不包含 "code"
	cmd := exec.Command("claude", args...)

	// 设置环境变量
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ANTHROPIC_BASE_URL=http://%s:%d", cfg.CCEnvHost, cfg.CCEnvPort),
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

	// 6. 执行 claude code 命令并等待结束
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