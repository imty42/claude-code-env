package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ServiceConfig 表示单个服务的配置（保留兼容性）
type ServiceConfig map[string]string

// Provider 表示单个服务提供商配置
type Provider struct {
	Name  string            `json:"name"`
	State string            `json:"state"`
	Env   map[string]string `json:"env"`
}

// Routing 表示路由策略配置
type Routing struct {
	Strategy string `json:"strategy"`
}

// Config 表示配置文件结构
type Config struct {
	Version        string     `json:"version"`
	APIKey         string     `json:"APIKEY"`
	CCEnvHost      string     `json:"CCENV_HOST"`
	CCEnvPort      int        `json:"CCENV_PORT"`
	APIProxy       string     `json:"API_PROXY"`
	LoggingLevel   string     `json:"LOGGING_LEVEL"`
	APITimeoutMS   int        `json:"API_TIMEOUT_MS"`
	Providers      []Provider `json:"providers"`
	Routing        Routing    `json:"routing"`
}

// ExampleConfig 硬编码的示例配置
const ExampleConfig = `{
    "version": "2.0",
    "APIKEY": "your-secret-key",
    "CCENV_HOST": "localhost", 
    "CCENV_PORT": 9999,
    "API_PROXY": "http://127.0.0.1:7890",
    "LOGGING_LEVEL": "DEBUG",
    "API_TIMEOUT_MS": 600000,
    "providers": [
        {
            "name": "provider-a",
            "state": "off",
            "env": {
                "ANTHROPIC_BASE_URL": "https://api.siliconflow.cn",
                "ANTHROPIC_AUTH_TOKEN": "sk-1",
                "ANTHROPIC_MODEL": "deepseek-ai/DeepSeek-V3"
            }
        },
        {
            "name": "provider-b", 
            "state": "on",
            "env": {
                "ANTHROPIC_BASE_URL": "https://api.siliconflow.cn",
                "ANTHROPIC_API_KEY": "sk-2",
                "ANTHROPIC_MODEL": "deepseek-ai/DeepSeek-V3"
            }
        }
    ],
    "routing": {
        "strategy": "default"
    }
}`

// LoadConfig 加载配置文件
func LoadConfig() (*Config, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户主目录失败: %v", err)
	}
	
	configPath := filepath.Join(homeDir, ".claude-code-env", "settings.json")
	
	// 检查配置文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("配置文件不存在")
	}

	// 读取配置文件
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 解析 JSON
	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 设置默认值
	config.SetDefaults()

	return &config, nil
}

// SetDefaults 设置默认值
func (c *Config) SetDefaults() {
	if c.CCEnvHost == "" {
		c.CCEnvHost = "localhost"
	}
	if c.CCEnvPort == 0 {
		c.CCEnvPort = 9999
	}
	if c.LoggingLevel == "" {
		c.LoggingLevel = "INFO"
	}
	if c.APITimeoutMS == 0 {
		c.APITimeoutMS = 600000 // 10 分钟
	}
	
	// 验证并设置路由策略
	if c.Routing.Strategy != "default" && c.Routing.Strategy != "robin" {
		c.Routing.Strategy = "default"
	}
	
	// 验证 API_PROXY 格式
	if c.APIProxy != "" {
		if !strings.HasPrefix(c.APIProxy, "http://") && !strings.HasPrefix(c.APIProxy, "https://") {
			c.APIProxy = "" // 不符合格式，重置为空
		}
	}
}

// GetActiveProviders 获取状态为 "on" 的 providers
func (c *Config) GetActiveProviders() []Provider {
	var activeProviders []Provider
	for _, provider := range c.Providers {
		if provider.State == "on" {
			activeProviders = append(activeProviders, provider)
		}
	}
	return activeProviders
}

// ConfigWatcher 配置文件监控器
type ConfigWatcher struct {
	watcher    *fsnotify.Watcher
	configPath string
	reloadChan chan *Config
	errorChan  chan error
	stopChan   chan bool
}

// NewConfigWatcher 创建新的配置监控器
func NewConfigWatcher() (*ConfigWatcher, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("获取用户主目录失败: %v", err)
	}

	configPath := filepath.Join(homeDir, ".claude-code-env", "settings.json")
	
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("创建文件监控器失败: %v", err)
	}

	cw := &ConfigWatcher{
		watcher:    watcher,
		configPath: configPath,
		reloadChan: make(chan *Config, 1),
		errorChan:  make(chan error, 1),
		stopChan:   make(chan bool, 1),
	}

	return cw, nil
}

// Start 开始监控配置文件
func (cw *ConfigWatcher) Start() error {
	// 监控配置文件目录
	configDir := filepath.Dir(cw.configPath)
	err := cw.watcher.Add(configDir)
	if err != nil {
		return fmt.Errorf("添加监控目录失败: %v", err)
	}

	go cw.watchLoop()
	
	return nil
}

// watchLoop 监控循环
func (cw *ConfigWatcher) watchLoop() {
	var lastReloadTime time.Time
	
	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}
			
			// 只关心配置文件的写入事件
			if event.Name == cw.configPath && (event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
				// 防止短时间内多次重载
				if time.Since(lastReloadTime) < 1*time.Second {
					continue
				}
				lastReloadTime = time.Now()
				
				// 稍等片刻确保文件写入完成
				time.Sleep(100 * time.Millisecond)
				
				// 尝试重新加载配置
				newConfig, err := LoadConfig()
				if err != nil {
					cw.errorChan <- fmt.Errorf("重新加载配置失败: %v", err)
					continue
				}
				
				// 发送新配置
				select {
				case cw.reloadChan <- newConfig:
					// 配置发送成功
				default:
					// 如果通道已满，跳过这次更新
				}
			}
			
		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			cw.errorChan <- fmt.Errorf("文件监控错误: %v", err)
			
		case <-cw.stopChan:
			return
		}
	}
}

// GetReloadChan 获取配置重载通道
func (cw *ConfigWatcher) GetReloadChan() <-chan *Config {
	return cw.reloadChan
}

// GetErrorChan 获取错误通道
func (cw *ConfigWatcher) GetErrorChan() <-chan error {
	return cw.errorChan
}

// Stop 停止配置监控
func (cw *ConfigWatcher) Stop() error {
	close(cw.stopChan)
	return cw.watcher.Close()
}

// maskSensitiveValue 对敏感值进行打码
func maskSensitiveValue(value string) string {
	if value == "" {
		return ""
	}
	
	if len(value) <= 8 {
		// 短值全部打码
		return strings.Repeat("*", len(value))
	}
	
	// 长值保留前4位和后4位
	return value[:4] + strings.Repeat("*", len(value)-8) + value[len(value)-4:]
}

// DisplayConfig 显示配置信息（敏感信息打码）
func (c *Config) DisplayConfig() {
	fmt.Println("=== Claude Code Env 配置信息 ===")
	fmt.Printf("版本: %s\n", c.Version)
	fmt.Printf("代理主机: %s\n", c.CCEnvHost)
	fmt.Printf("代理端口: %d\n", c.CCEnvPort)
	fmt.Printf("日志级别: %s\n", c.LoggingLevel)
	fmt.Printf("API超时: %dms\n", c.APITimeoutMS)
	
	if c.APIProxy != "" {
		fmt.Printf("API代理: %s\n", c.APIProxy)
	} else {
		fmt.Printf("API代理: 未配置\n")
	}
	
	fmt.Printf("路由策略: %s\n", c.Routing.Strategy)
	
	fmt.Printf("\n=== Provider 配置 (%d个) ===\n", len(c.Providers))
	for i, provider := range c.Providers {
		fmt.Printf("\n[%d] %s\n", i+1, provider.Name)
		fmt.Printf("  状态: %s\n", provider.State)
		
		// 显示环境变量
		fmt.Printf("  环境变量:\n")
		for key, value := range provider.Env {
			if key == "ANTHROPIC_AUTH_TOKEN" || key == "ANTHROPIC_API_KEY" {
				// 对敏感信息打码
				fmt.Printf("    %s: %s\n", key, maskSensitiveValue(value))
			} else {
				fmt.Printf("    %s: %s\n", key, value)
			}
		}
	}
	
	// 显示活跃的providers
	activeProviders := c.GetActiveProviders()
	fmt.Printf("\n=== 当前活跃的 Providers (%d个) ===\n", len(activeProviders))
	for _, provider := range activeProviders {
		authType := "未知"
		if provider.Env["ANTHROPIC_AUTH_TOKEN"] != "" {
			authType = "Bearer Token"
		} else if provider.Env["ANTHROPIC_API_KEY"] != "" {
			authType = "API Key"
		}
		fmt.Printf("- %s (认证方式: %s)\n", provider.Name, authType)
	}
	
	fmt.Println("\n=== 配置文件路径 ===")
	homeDir, _ := os.UserHomeDir()
	configPath := filepath.Join(homeDir, ".claude-code-env", "settings.json")
	fmt.Printf("%s\n", configPath)
}

// ShowExampleConfig 显示示例配置
func ShowExampleConfig() {
	fmt.Println("参考配置:")
	fmt.Println(ExampleConfig)
}