package server_routing_manager

import (
	"fmt"
	"time"

	"github.com/imty42/claude-code-env/internal/admin"
	"github.com/imty42/claude-code-env/internal/llm_proxy"
	"github.com/imty42/claude-code-env/internal/logger"
	"github.com/imty42/claude-code-env/internal/provider"
)

// ServerRoutingManager 服务路由管理器 - 管理 LLMProxyServer 和 AdminServer 的生命周期
type ServerRoutingManager struct {
	llmServer   *llm_proxy.LLMProxyServer
	adminServer *admin.AdminServer
	
	// 配置参数
	host      string
	apiPort   int
	adminPort int
}

// NewServerRoutingManager 创建新的服务路由管理器
func NewServerRoutingManager(providerManager *provider.ProviderManager, host string, apiPort, adminPort int, apiProxy string, timeout time.Duration) *ServerRoutingManager {
	logger.Info(logger.ModuleProxy, "初始化服务路由管理器: LLM代理端口=%d, 管理端口=%d", apiPort, adminPort)
	
	// 创建 LLM API 服务器
	llmServer := llm_proxy.NewLLMProxyServer(providerManager, host, apiPort, apiProxy, timeout)
	
	// 创建管理服务器
	adminServer := admin.NewAdminServer(host, adminPort)
	
	manager := &ServerRoutingManager{
		llmServer:   llmServer,
		adminServer: adminServer,
		host:        host,
		apiPort:     apiPort,
		adminPort:   adminPort,
	}
	
	return manager
}

// Start 启动所有服务
func (s *ServerRoutingManager) Start() error {
	logger.Info(logger.ModuleProxy, "启动服务路由管理器...")
	
	// 启动 LLM API 服务器
	if err := s.llmServer.Start(); err != nil {
		return fmt.Errorf("启动LLM API服务器失败: %v", err)
	}
	
	// 启动管理服务器
	if err := s.adminServer.Start(); err != nil {
		// 如果管理服务器启动失败，需要关闭已启动的LLM服务器
		s.llmServer.Shutdown()
		return fmt.Errorf("启动管理服务器失败: %v", err)
	}
	
	logger.Info(logger.ModuleProxy, "所有服务启动完成")
	logger.Info(logger.ModuleProxy, "  - LLM API服务: http://%s:%d", s.host, s.apiPort)
	logger.Info(logger.ModuleProxy, "  - 管理服务: http://%s:%d", s.host, s.adminPort)
	
	return nil
}

// Shutdown 关闭所有服务
func (s *ServerRoutingManager) Shutdown() error {
	logger.Info(logger.ModuleProxy, "关闭服务路由管理器...")
	
	var llmErr, adminErr error
	
	// 并行关闭两个服务器
	done := make(chan bool, 2)
	
	// 关闭 LLM API 服务器
	go func() {
		llmErr = s.llmServer.Shutdown()
		if llmErr != nil {
			logger.Error(logger.ModuleProxy, "关闭LLM API服务器失败: %v", llmErr)
		}
		done <- true
	}()
	
	// 关闭管理服务器  
	go func() {
		adminErr = s.adminServer.Shutdown()
		if adminErr != nil {
			logger.Error(logger.ModuleProxy, "关闭管理服务器失败: %v", adminErr)
		}
		done <- true
	}()
	
	// 等待两个服务器都关闭
	<-done
	<-done
	
	// 如果有任何一个关闭失败，返回错误
	if llmErr != nil {
		return fmt.Errorf("关闭LLM API服务器失败: %v", llmErr)
	}
	if adminErr != nil {
		return fmt.Errorf("关闭管理服务器失败: %v", adminErr)
	}
	
	logger.Info(logger.ModuleProxy, "所有服务已关闭")
	return nil
}