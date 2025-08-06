package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CreateConfigIfNeeded 检查并创建配置文件
func CreateConfigIfNeeded() bool {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Printf("获取用户主目录失败: %v\n", err)
		return false
	}
	
	configDir := filepath.Join(homeDir, ".claude-code-env")
	configPath := filepath.Join(configDir, "settings.json")
	
	// 检查目录是否存在
	if _, err := os.Stat(configDir); os.IsNotExist(err) {
		fmt.Printf("配置目录 %s 不存在，是否创建？(Y/n): ", configDir)
		if !askUserConfirmation() {
			return false
		}
		
		err = os.MkdirAll(configDir, 0755)
		if err != nil {
			fmt.Printf("创建配置目录失败: %v\n", err)
			return false
		}
		fmt.Printf("已创建配置目录: %s\n", configDir)
	}
	
	// 检查配置文件是否存在
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("配置文件 %s 不存在，是否创建？(Y/n): ", configPath)
		if !askUserConfirmation() {
			return false
		}
		
		err = os.WriteFile(configPath, []byte(ExampleConfig), 0644)
		if err != nil {
			fmt.Printf("创建配置文件失败: %v\n", err)
			return false
		}
		fmt.Printf("已创建配置文件: %s\n", configPath)
		fmt.Println("请根据需要修改配置文件中的服务配置")
		return true
	}
	
	return false
}

// askUserConfirmation 询问用户确认
func askUserConfirmation() bool {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		input := strings.TrimSpace(strings.ToLower(scanner.Text()))
		// 空输入默认为 yes
		if input == "" || input == "y" || input == "yes" {
			return true
		}
	}
	return false
}