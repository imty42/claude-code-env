package config

import (
	"testing"
)

func TestExampleConfig(t *testing.T) {
	// 测试示例配置是否有效
	if ExampleConfig == "" {
		t.Error("Example config should not be empty")
	}
	
	// 简单的JSON格式检查
	if len(ExampleConfig) < 10 {
		t.Error("Example config seems too short")
	}
}