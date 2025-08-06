package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ServiceConfig 表示单个服务的配置
type ServiceConfig map[string]string

// Settings 表示所有服务配置
type Settings map[string]ServiceConfig

// ExampleConfig 硬编码的示例配置
const ExampleConfig = `{
    "service-name1": {
        "ANTHROPIC_BASE_URL": "https://service1",
        "ANTHROPIC_AUTH_TOKEN": "sk-1"
    },
    "service-name2": {
        "ANTHROPIC_BASE_URL": "https://service2",
        "ANTHROPIC_API_KEY": "sk-2",
        "ANTHROPIC_ANTHROPIC_MODEL": "kimi-k2"
    }
}`

// LoadSettings 加载配置文件
func LoadSettings() (Settings, error) {
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
	var settings Settings
	err = json.Unmarshal(data, &settings)
	if err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %v", err)
	}

	return settings, nil
}

// ShowExampleConfig 显示示例配置
func ShowExampleConfig() {
	fmt.Println("参考配置:")
	fmt.Println(ExampleConfig)
}