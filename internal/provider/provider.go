package provider

import (
	"fmt"
	"sync"
	"time"

	"github.com/imty42/claude-code-env/internal/config"
	"github.com/imty42/claude-code-env/internal/logger"
)

// ProviderState 表示 provider 的运行时状态
type ProviderState struct {
	Provider        config.Provider
	FailureCount    int       // 累计失败次数
	LastFailureTime time.Time // 最后失败时间
	IsDisabled      bool      // 是否被暂时禁用
	DisabledUntil   time.Time // 禁用到期时间
}

// ProviderManager 管理多个 providers 的状态和路由
type ProviderManager struct {
	providers       []*ProviderState
	routingStrategy string
	robinIndex int
	mutex           sync.RWMutex
}

// NewProviderManager 创建新的 ProviderManager
func NewProviderManager(cfg *config.Config) *ProviderManager {
	pm := &ProviderManager{
		providers:       make([]*ProviderState, 0),
		routingStrategy: cfg.Routing.Strategy,
		robinIndex:      0,
	}

	// 初始化所有 providers
	for _, provider := range cfg.Providers {
		// 验证认证配置
		authToken := provider.Env["ANTHROPIC_AUTH_TOKEN"]
		apiKey := provider.Env["ANTHROPIC_API_KEY"]
		
		// 如果两个都没有配置，标记为失效
		isDisabled := provider.State != "on"
		if authToken == "" && apiKey == "" {
			isDisabled = true
			logger.Warn(logger.ModuleProvider, "Provider %s 缺少认证配置(ANTHROPIC_AUTH_TOKEN或ANTHROPIC_API_KEY)，已禁用", provider.Name)
		}
		
		ps := &ProviderState{
			Provider:     provider,
			FailureCount: 0,
			IsDisabled:   isDisabled,
		}
		pm.providers = append(pm.providers, ps)
	}

	logger.Info(logger.ModuleProvider, "初始化 ProviderManager，策略: %s，providers 数量: %d", pm.routingStrategy, len(pm.providers))
	
	// 展示每个 provider 的状态
	for _, ps := range pm.providers {
		status := ps.Provider.State
		if ps.IsDisabled && ps.Provider.State == "on" {
			status = "disabled (缺少认证)"
		}
		logger.Info(logger.ModuleProvider, "Provider: %s, State: %s", ps.Provider.Name, status)
	}
	
	return pm
}

// GetNextProvider 根据路由策略获取下一个可用的 provider
func (pm *ProviderManager) GetNextProvider() (*ProviderState, error) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	// 更新 provider 状态（检查是否可以恢复）
	pm.updateProviderStates()

	// 获取所有可用的 providers
	availableProviders := pm.getAvailableProviders()
	if len(availableProviders) == 0 {
		return nil, fmt.Errorf("没有可用的 provider")
	}

	switch pm.routingStrategy {
	case "robin":
		return pm.getNextRobin(availableProviders), nil
	case "default":
		fallthrough
	default:
		return pm.getNextDefault(availableProviders), nil
	}
}

// RecordFailure 记录 provider 失败
func (pm *ProviderManager) RecordFailure(providerName string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, ps := range pm.providers {
		if ps.Provider.Name == providerName {
			ps.FailureCount++
			ps.LastFailureTime = time.Now()
			
			logger.Warn(logger.ModuleProvider, "Provider %s 失败，累计失败次数: %d", providerName, ps.FailureCount)

			// 如果失败次数达到 5 次，禁用 5 分钟
			if ps.FailureCount >= 5 {
				ps.IsDisabled = true
				ps.DisabledUntil = time.Now().Add(5 * time.Minute)
				logger.Error(logger.ModuleProvider, "Provider %s 失败次数达到 5 次，禁用 5 分钟", providerName)
			}
			break
		}
	}
}

// RecordSuccess 记录 provider 成功（重置失败计数）
func (pm *ProviderManager) RecordSuccess(providerName string) {
	pm.mutex.Lock()
	defer pm.mutex.Unlock()

	for _, ps := range pm.providers {
		if ps.Provider.Name == providerName {
			if ps.FailureCount > 0 {
				logger.Info(logger.ModuleProvider, "Provider %s 成功，重置失败计数 (之前: %d)", providerName, ps.FailureCount)
				ps.FailureCount = 0
			}
			break
		}
	}
}

// updateProviderStates 更新所有 provider 状态（检查是否可以恢复）
func (pm *ProviderManager) updateProviderStates() {
	now := time.Now()
	
	for _, ps := range pm.providers {
		// 检查被禁用的 provider 是否可以恢复
		if ps.IsDisabled && !ps.DisabledUntil.IsZero() && now.After(ps.DisabledUntil) {
			ps.IsDisabled = false
			ps.DisabledUntil = time.Time{}
			ps.FailureCount = 0 // 重置失败计数
			logger.Info(logger.ModuleProvider, "Provider %s 禁用期结束，重新启用", ps.Provider.Name)
		}
	}
}

// getAvailableProviders 获取所有可用的 providers
func (pm *ProviderManager) getAvailableProviders() []*ProviderState {
	var available []*ProviderState
	
	for _, ps := range pm.providers {
		// Provider 必须配置为 "on" 且未被禁用
		if ps.Provider.State == "on" && !ps.IsDisabled {
			available = append(available, ps)
		}
	}
	
	return available
}

// getNextDefault 默认策略：按配置顺序返回第一个可用的
func (pm *ProviderManager) getNextDefault(availableProviders []*ProviderState) *ProviderState {
	// 按原始顺序返回第一个可用的
	for _, ps := range pm.providers {
		for _, available := range availableProviders {
			if ps == available {
				logger.Debug(logger.ModuleProvider, "选择 provider: %s (default策略)", ps.Provider.Name)
				return ps
			}
		}
	}
	
	// 理论上不会到这里，因为 availableProviders 不为空
	return availableProviders[0]
}

// getNextRobin 轮询策略：轮流返回可用的 providers
func (pm *ProviderManager) getNextRobin(availableProviders []*ProviderState) *ProviderState {
	if len(availableProviders) == 1 {
		logger.Debug(logger.ModuleProvider, "选择 provider: %s (robin策略，仅一个可用)", availableProviders[0].Provider.Name)
		return availableProviders[0]
	}
	
	// 更新轮询索引
	pm.robinIndex = pm.robinIndex % len(availableProviders)
	selected := availableProviders[pm.robinIndex]
	pm.robinIndex++
	
	logger.Debug(logger.ModuleProvider, "选择 provider: %s (robin策略，索引: %d)", selected.Provider.Name, pm.robinIndex-1)
	return selected
}

// GetProviderStatus 获取所有 provider 状态（用于调试）
func (pm *ProviderManager) GetProviderStatus() []map[string]interface{} {
	pm.mutex.RLock()
	defer pm.mutex.RUnlock()
	
	var status []map[string]interface{}
	
	for _, ps := range pm.providers {
		s := map[string]interface{}{
			"name":         ps.Provider.Name,
			"state":        ps.Provider.State,
			"failure_count": ps.FailureCount,
			"is_disabled":  ps.IsDisabled,
		}
		
		if !ps.DisabledUntil.IsZero() {
			s["disabled_until"] = ps.DisabledUntil.Format("2006-01-02 15:04:05")
		}
		
		status = append(status, s)
	}
	
	return status
}