package proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/imty42/claude-code-env/internal/logger"
	"github.com/imty42/claude-code-env/internal/provider"
)

// AnthropicErrorResponse Anthropic API 错误响应格式
type AnthropicErrorResponse struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeAnthropicError 写入 Anthropic API 格式的错误响应
func writeAnthropicError(w http.ResponseWriter, statusCode int, errorType, message string) {
	errorResp := AnthropicErrorResponse{
		Type: "error",
	}
	errorResp.Error.Type = errorType
	errorResp.Error.Message = "CCENV " + message

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	jsonData, err := json.Marshal(errorResp)
	if err != nil {
		// 如果JSON序列化失败，返回基本错误
		w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"CCENV 序列化错误响应失败"}}`))
		return
	}

	w.Write(jsonData)
}

// ProxyServer 代理服务器
type ProxyServer struct {
	server          *http.Server
	providerManager *provider.ProviderManager
	host            string // 服务主机
	port            int    // 服务端口
	httpClient      *http.Client
	clientManager   *ClientManager
}

// ClientManager 客户端生命周期管理器
type ClientManager struct {
	mu      sync.RWMutex
	clients map[string]*ClientInfo
	onEmpty func() // 当所有客户端都断开时调用的回调函数
}

// ClientInfo 客户端信息
type ClientInfo struct {
	ID         string    `json:"id"`
	RegisterAt time.Time `json:"register_at"`
	LastPing   time.Time `json:"last_ping"`
	PID        int       `json:"pid"`      // 进程ID
	Hostname   string    `json:"hostname"` // 主机名
}

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

// ClientStatusResponse 客户端状态响应
type ClientStatusResponse struct {
	TotalClients int                    `json:"total_clients"`
	Clients      map[string]*ClientInfo `json:"clients"`
	ServerTime   time.Time              `json:"server_time"`
}

// NewClientManager 创建新的客户端管理器
func NewClientManager() *ClientManager {
	return &ClientManager{
		clients: make(map[string]*ClientInfo),
	}
}

// SetOnEmptyCallback 设置当所有客户端都断开时的回调函数
func (cm *ClientManager) SetOnEmptyCallback(callback func()) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.onEmpty = callback
}

// RegisterClient 注册客户端
func (cm *ClientManager) RegisterClient(clientID string, pid int, hostname string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	cm.clients[clientID] = &ClientInfo{
		ID:         clientID,
		RegisterAt: now,
		LastPing:   now,
		PID:        pid,
		Hostname:   hostname,
	}

	logger.Info(logger.ModuleProxy, "客户端生命周期: [注册] %s -> 总数:%d",
		clientID, len(cm.clients))
}

// UpdateHeartbeat 更新客户端心跳
func (cm *ClientManager) UpdateHeartbeat(clientID string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if client, exists := cm.clients[clientID]; exists {
		client.LastPing = time.Now()
		// 心跳成功不记录日志，避免日志噪音
		return true
	}

	logger.Warn(logger.ModuleProxy, "心跳失败: 客户端未注册 %s", clientID)
	return false
}

// UnregisterClient 注销客户端
func (cm *ClientManager) UnregisterClient(clientID string) bool {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if _, exists := cm.clients[clientID]; exists {
		delete(cm.clients, clientID)
		remainingCount := len(cm.clients)
		logger.Info(logger.ModuleProxy, "客户端生命周期: [注销] %s -> 剩余:%d", clientID, remainingCount)

		// 如果没有客户端了，调用回调函数
		if remainingCount == 0 && cm.onEmpty != nil {
			logger.Info(logger.ModuleProxy, "触发自动关闭: 所有客户端已断开")
			go cm.onEmpty()
		}
		return true
	}

	logger.Warn(logger.ModuleProxy, "注销失败: 客户端不存在 %s", clientID)
	return false
}

// GetClientCount 获取当前客户端数量
func (cm *ClientManager) GetClientCount() int {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return len(cm.clients)
}

// GetAllClients 获取所有客户端信息（用于状态查询）
func (cm *ClientManager) GetAllClients() map[string]*ClientInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 创建副本避免并发问题
	result := make(map[string]*ClientInfo)
	for id, client := range cm.clients {
		clientCopy := *client
		result[id] = &clientCopy
	}

	return result
}

// GetClientInfo 获取特定客户端信息
func (cm *ClientManager) GetClientInfo(clientID string) *ClientInfo {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	if client, exists := cm.clients[clientID]; exists {
		clientCopy := *client
		return &clientCopy
	}

	return nil
}

// CleanupInactiveClients 清理非活跃客户端
func (cm *ClientManager) CleanupInactiveClients(timeout time.Duration) int {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	now := time.Now()
	var toRemove []string

	for id, client := range cm.clients {
		if now.Sub(client.LastPing) > timeout {
			toRemove = append(toRemove, id)
		}
	}

	if len(toRemove) > 0 {
		for _, id := range toRemove {
			delete(cm.clients, id)
		}

		logger.Info(logger.ModuleProxy, "自动清理: 移除%d个非活跃客户端 -> 剩余:%d",
			len(toRemove), len(cm.clients))
	}

	// 如果清理后没有客户端了，调用回调函数
	if len(cm.clients) == 0 && len(toRemove) > 0 && cm.onEmpty != nil {
		logger.Info(logger.ModuleProxy, "触发自动关闭: 清理后无活跃客户端")
		go cm.onEmpty()
	}

	return len(toRemove)
}

// NewProxyServer 创建新的代理服务器
func NewProxyServer(providerManager *provider.ProviderManager, host string, port int, apiProxy string, timeout time.Duration) *ProxyServer {
	// 创建带代理配置的 HTTP 客户端
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
	}

	// 设置代理
	if apiProxy != "" {
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			return url.Parse(apiProxy)
		}
		logger.Info(logger.ModuleProxy, "配置API代理: %s", apiProxy)
	}

	proxy := &ProxyServer{
		providerManager: providerManager,
		host:            host,
		port:            port,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		clientManager: NewClientManager(),
	}

	mux := http.NewServeMux()

	// 注册客户端生命周期管理接口
	mux.HandleFunc("/ccenv/register", proxy.handleClientRegister)
	mux.HandleFunc("/ccenv/unregister", proxy.handleClientUnregister)
	mux.HandleFunc("/ccenv/heartbeat", proxy.handleClientHeartbeat)
	mux.HandleFunc("/ccenv/status", proxy.handleClientStatus)
	mux.HandleFunc("/ccenv/client/", proxy.handleClientInfo)

	// 注册特定的 /v1/messages 处理器（保留特殊的模型映射逻辑）
	mux.HandleFunc("/v1/messages", proxy.handleMessages)

	// 注册通用的 /v1/ 路由处理器（除了 messages）
	mux.HandleFunc("/v1/", proxy.handleV1Routes)

	// 注册 /health 端点
	mux.HandleFunc("/health", proxy.handleHealth)

	// 预留 /ui/ 路由用于管理界面
	mux.HandleFunc("/ui/", proxy.handleUI)

	// 注册 catch-all 处理器用于调试和日志记录
	mux.HandleFunc("/", proxy.handleCatchAll)

	proxy.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: mux,
	}

	return proxy
}

// SetShutdownCallback 设置当所有客户端断开时的关闭回调
func (p *ProxyServer) SetShutdownCallback(callback func()) {
	p.clientManager.SetOnEmptyCallback(callback)
}

// GetClientCount 获取当前活跃客户端数量
func (p *ProxyServer) GetClientCount() int {
	return p.clientManager.GetClientCount()
}

// handleClientRegister 处理客户端注册请求
func (p *ProxyServer) handleClientRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "请求数据格式错误: " + err.Error(),
		})
		return
	}

	// 注册客户端
	p.clientManager.RegisterClient(req.ClientID, req.PID, req.Hostname)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ClientResponse{
		Success: true,
		Message: "客户端注册成功",
		Count:   p.clientManager.GetClientCount(),
	})
}

// handleClientUnregister 处理客户端注销请求
func (p *ProxyServer) handleClientUnregister(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "请求数据格式错误: " + err.Error(),
		})
		return
	}

	// 注销客户端
	success := p.clientManager.UnregisterClient(req.ClientID)

	w.Header().Set("Content-Type", "application/json")
	if success {
		json.NewEncoder(w).Encode(ClientResponse{
			Success: true,
			Message: "客户端注销成功",
			Count:   p.clientManager.GetClientCount(),
		})
	} else {
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "客户端未找到或已注销",
		})
	}
}

// handleClientHeartbeat 处理客户端心跳请求
func (p *ProxyServer) handleClientHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "仅支持 POST 请求",
		})
		return
	}

	var req ClientRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "请求数据格式错误: " + err.Error(),
		})
		return
	}

	// 更新心跳
	success := p.clientManager.UpdateHeartbeat(req.ClientID)

	w.Header().Set("Content-Type", "application/json")
	if success {
		json.NewEncoder(w).Encode(ClientResponse{
			Success: true,
			Message: "心跳更新成功",
			Count:   p.clientManager.GetClientCount(),
		})
	} else {
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "客户端未注册，请先注册",
		})
	}
}

// handleClientInfo 处理特定客户端信息查询请求
func (p *ProxyServer) handleClientInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "仅支持 GET 请求",
		})
		return
	}

	// 从URL路径中提取客户端ID
	clientID := strings.TrimPrefix(r.URL.Path, "/ccenv/client/")
	if clientID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "客户端ID不能为空",
		})
		return
	}

	// 获取客户端信息
	client := p.clientManager.GetClientInfo(clientID)
	if client == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "客户端未找到",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(client)
}

// handleClientStatus 处理客户端状态查询请求
func (p *ProxyServer) handleClientStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(ClientResponse{
			Success: false,
			Message: "仅支持 GET 请求",
		})
		return
	}

	clients := p.clientManager.GetAllClients()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ClientStatusResponse{
		TotalClients: len(clients),
		Clients:      clients,
		ServerTime:   time.Now(),
	})
}

// handleMessages 处理 /v1/messages 请求
func (p *ProxyServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	// 生成请求追踪ID
	requestID := logger.GenerateRequestID()

	// 只处理 POST 请求
	if r.Method != "POST" {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "仅支持 POST 请求")
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "仅支持 POST 请求")
		return
	}

	// 开始计时
	startTime := time.Now()

	// 获取下一个可用的 provider
	providerState, err := p.providerManager.GetNextProvider()
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "获取可用 provider 失败: %v", err)
		writeAnthropicError(w, http.StatusServiceUnavailable, "overloaded_error", "无可用的服务提供商")
		return
	}

	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "读取请求体失败: %v", err)
		writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败")
		p.providerManager.RecordFailure(providerState.Provider.Name)
		return
	}
	r.Body.Close()

	// 解析和修改请求体（如果需要模型映射）
	modifiedBody := bodyBytes
	targetModel := providerState.Provider.Env["ANTHROPIC_MODEL"]
	if targetModel != "" {
		modifiedBody, err = p.modifyRequestModel(bodyBytes, targetModel, providerState.Provider.Name)
		if err != nil {
			logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "修改请求模型失败: %v", err)
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "修改请求模型失败")
			p.providerManager.RecordFailure(providerState.Provider.Name)
			return
		}
	} else {
		// 没有配置模型映射，也要打印当前请求的模型
		p.logRequestModel(bodyBytes, providerState.Provider.Name, requestID)
	}

	// 构建目标 URL
	baseURL := providerState.Provider.Env["ANTHROPIC_BASE_URL"]
	targetURL := baseURL + "/v1/messages"

	// 创建代理请求
	proxyReq, err := http.NewRequest("POST", targetURL, bytes.NewReader(modifiedBody))
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "创建请求失败: %v", err)
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "创建请求失败")
		p.providerManager.RecordFailure(providerState.Provider.Name)
		return
	}

	// 复制所有请求头
	proxyReq.Header = r.Header.Clone()

	// 设置认证头：优先使用ANTHROPIC_AUTH_TOKEN，其次ANTHROPIC_API_KEY
	authToken := providerState.Provider.Env["ANTHROPIC_AUTH_TOKEN"]
	apiKey := providerState.Provider.Env["ANTHROPIC_API_KEY"]

	if authToken != "" {
		// 使用 Authorization header with Bearer prefix
		proxyReq.Header.Set("Authorization", "Bearer "+authToken)
	} else if apiKey != "" {
		// 使用 X-Api-Key header
		proxyReq.Header.Set("X-Api-Key", apiKey)
	}
	// 如果两个都没有，NewProviderManager已经将该provider标记为disabled了

	// 确保 Content-Length 正确
	proxyReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(modifiedBody)))

	// 发送请求
	resp, err := p.httpClient.Do(proxyReq)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "请求失败: %v", err)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "请求转发失败")
		p.providerManager.RecordFailure(providerState.Provider.Name)
		return
	}
	defer resp.Body.Close()

	// 检查响应状态码，5xx 错误视为 provider 失败
	if resp.StatusCode >= 500 {
		logger.Warn(logger.ModuleProxy, "Provider %s 返回服务器错误: %d", providerState.Provider.Name, resp.StatusCode)
		p.providerManager.RecordFailure(providerState.Provider.Name)
	} else {
		// 成功响应，重置失败计数
		p.providerManager.RecordSuccess(providerState.Provider.Name)
	}

	// 计算耗时并记录统一的HTTP请求日志
	duration := time.Since(startTime)
	logger.LogHTTPRequest(requestID, r.Method, r.URL.Path, resp.StatusCode, duration, providerState.Provider.Name)

	// 复制响应
	p.copyResponse(w, resp)
}

// modifyRequestModel 修改请求中的模型
func (p *ProxyServer) modifyRequestModel(bodyBytes []byte, targetModel, providerName string) ([]byte, error) {
	var requestBody map[string]interface{}

	err := json.Unmarshal(bodyBytes, &requestBody)
	if err != nil {
		return nil, fmt.Errorf("解析JSON失败: %v", err)
	}

	// 获取原始模型
	originalModel := ""
	if model, exists := requestBody["model"]; exists {
		if modelStr, ok := model.(string); ok {
			originalModel = modelStr
		}
	}

	// 替换为目标模型
	requestBody["model"] = targetModel

	if originalModel != "" {
		if originalModel != targetModel {
			logger.Info(logger.ModuleProxy, "[%s] 模型映射: %s -> %s", providerName, originalModel, targetModel)
		} else {
			logger.Info(logger.ModuleProxy, "[%s] 请求模型: %s", providerName, originalModel)
		}
	} else {
		logger.Info(logger.ModuleProxy, "[%s] 请求模型: %s", providerName, targetModel)
	}

	// 重新序列化
	modifiedBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("序列化JSON失败: %v", err)
	}

	return modifiedBytes, nil
}

// logRequestModel 记录请求模型（用于没有模型映射的情况）
func (p *ProxyServer) logRequestModel(bodyBytes []byte, providerName, requestID string) {
	var requestBody map[string]interface{}

	err := json.Unmarshal(bodyBytes, &requestBody)
	if err != nil {
		logger.DebugWithRequestID(logger.ModuleProxy, requestID, "解析请求体失败，无法获取模型信息: %v", err)
		return
	}

	// 获取请求模型
	if model, exists := requestBody["model"]; exists {
		if modelStr, ok := model.(string); ok {
			logger.Info(logger.ModuleProxy, "[%s] [%s] 请求模型: %s", providerName, requestID, modelStr)
		}
	}
}

// copyResponse 复制响应
func (p *ProxyServer) copyResponse(w http.ResponseWriter, resp *http.Response) {
	// 复制所有响应头
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 设置状态码
	w.WriteHeader(resp.StatusCode)

	// 复制响应体，支持流式响应
	if flusher, ok := w.(http.Flusher); ok {
		// 支持 SSE 流式响应的实时转发
		buffer := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(buffer)
			if n > 0 {
				w.Write(buffer[:n])
				flusher.Flush() // 立即发送到客户端
			}
			if err != nil {
				if err != io.EOF {
					fmt.Printf("[PROXY] 读取响应失败: %v\n", err)
				}
				break
			}
		}
	} else {
		io.Copy(w, resp.Body)
	}
}

// Start 启动代理服务器
func (p *ProxyServer) Start() error {
	logger.Info(logger.ModuleProxy, "启动代理服务器: http://%s:%d", p.host, p.port)
	logger.Info(logger.ModuleProxy, "多Provider透明代理模式")

	go func() {
		if err := p.server.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error(logger.ModuleProxy, "服务器启动失败: %v", err)
		}
	}()

	// 启动客户端清理任务
	go p.startClientCleanupTask()

	// 等待一小段时间确保服务器启动
	time.Sleep(100 * time.Millisecond)
	return nil
}

// startClientCleanupTask 启动客户端清理任务
func (p *ProxyServer) startClientCleanupTask() {
	cleanupTicker := time.NewTicker(30 * time.Second) // 每30秒清理一次，更及时
	statusTicker := time.NewTicker(3 * time.Minute)   // 每3分钟打印状态
	defer cleanupTicker.Stop()
	defer statusTicker.Stop()

	for {
		select {
		case <-cleanupTicker.C:
			// 清理300秒（5分钟）内无心跳的客户端
			cleaned := p.clientManager.CleanupInactiveClients(300 * time.Second)
			if cleaned > 0 {
				logger.Info(logger.ModuleProxy, "自动清理了 %d 个非活跃客户端", cleaned)
			}

		case <-statusTicker.C:
			// 定期打印客户端状态
			count := p.clientManager.GetClientCount()
			if count > 0 {
				logger.Info(logger.ModuleProxy, "服务状态: 当前活跃客户端数 %d", count)
			}
		}
	}
}

// Shutdown 关闭代理服务器
func (p *ProxyServer) Shutdown() error {
	logger.Info(logger.ModuleProxy, "关闭代理服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return p.server.Shutdown(ctx)
}

// handleV1Routes 处理除 /v1/messages 外的其他 /v1/* 路由
func (p *ProxyServer) handleV1Routes(w http.ResponseWriter, r *http.Request) {
	// 跳过 /v1/messages，它有专门的处理器
	if r.URL.Path == "/v1/messages" {
		p.handleMessages(w, r)
		return
	}

	// 生成请求追踪ID
	requestID := logger.GenerateRequestID()
	startTime := time.Now()

	// 记录请求
	logger.DebugWithRequestID(logger.ModuleProxy, requestID, "%s %s", r.Method, r.URL.Path)
	logger.DebugWithRequestID(logger.ModuleProxy, requestID, "请求头: %v", r.Header)

	// 获取下一个可用的 provider
	providerState, err := p.providerManager.GetNextProvider()
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "获取可用 provider 失败: %v", err)
		writeAnthropicError(w, http.StatusServiceUnavailable, "overloaded_error", "无可用的服务提供商")
		return
	}

	// 构建目标 URL
	baseURL := providerState.Provider.Env["ANTHROPIC_BASE_URL"]
	targetURL := baseURL + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	// 转发请求
	err = p.forwardRequest(w, r, targetURL, providerState, startTime, requestID)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "转发请求失败 %s %s: %v", r.Method, r.URL.Path, err)
	}
}

// handleHealth 处理 /health 端点
func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// 生成请求追踪ID
	requestID := logger.GenerateRequestID()
	startTime := time.Now()

	logger.DebugWithRequestID(logger.ModuleProxy, requestID, "%s %s", r.Method, r.URL.Path)

	// 获取下一个可用的 provider
	providerState, err := p.providerManager.GetNextProvider()
	if err != nil {
		logger.Warn(logger.ModuleProxy, "健康检查时无可用 provider: %v", err)
		// 对于健康检查，如果没有可用的 provider，返回 503
		writeAnthropicError(w, http.StatusServiceUnavailable, "overloaded_error", "无可用的服务提供商")
		return
	}

	// 构建目标 URL
	baseURL := providerState.Provider.Env["ANTHROPIC_BASE_URL"]
	targetURL := baseURL + "/health"

	// 转发请求
	err = p.forwardRequest(w, r, targetURL, providerState, startTime, requestID)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "健康检查请求转发失败: %v", err)
	}
}

// handleCatchAll 处理所有其他未匹配的请求
func (p *ProxyServer) handleCatchAll(w http.ResponseWriter, r *http.Request) {
	logger.Info(logger.ModuleProxy, "接收到未匹配的请求: %s %s", r.Method, r.URL.Path)
	logger.Debug(logger.ModuleProxy, "请求头: %v", r.Header)

	// 对于未知路径，返回 404
	writeAnthropicError(w, http.StatusNotFound, "invalid_request_error",
		fmt.Sprintf("路径 '%s' 不存在或不被支持", r.URL.Path))
}

// forwardRequest 通用的请求转发函数
func (p *ProxyServer) forwardRequest(w http.ResponseWriter, r *http.Request, targetURL string, providerState *provider.ProviderState, startTime time.Time, requestID string) error {
	// 读取请求体
	var bodyBytes []byte
	var err error
	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败")
			p.providerManager.RecordFailure(providerState.Provider.Name)
			return fmt.Errorf("读取请求体失败: %v", err)
		}
		r.Body.Close()
	}

	// 创建代理请求
	proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "创建请求失败")
		p.providerManager.RecordFailure(providerState.Provider.Name)
		return fmt.Errorf("创建请求失败: %v", err)
	}

	// 复制所有请求头
	proxyReq.Header = r.Header.Clone()

	// 设置认证头：优先使用ANTHROPIC_AUTH_TOKEN，其次ANTHROPIC_API_KEY
	authToken := providerState.Provider.Env["ANTHROPIC_AUTH_TOKEN"]
	apiKey := providerState.Provider.Env["ANTHROPIC_API_KEY"]

	if authToken != "" {
		// 使用 Authorization header with Bearer prefix
		proxyReq.Header.Set("Authorization", "Bearer "+authToken)
	} else if apiKey != "" {
		// 使用 X-Api-Key header
		proxyReq.Header.Set("X-Api-Key", apiKey)
	}

	// 确保 Content-Length 正确
	if len(bodyBytes) > 0 {
		proxyReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(bodyBytes)))
	}

	// 发送请求
	resp, err := p.httpClient.Do(proxyReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "请求转发失败")
		p.providerManager.RecordFailure(providerState.Provider.Name)
		return fmt.Errorf("请求转发失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码，5xx 错误视为 provider 失败
	if resp.StatusCode >= 500 {
		logger.Warn(logger.ModuleProxy, "Provider %s 返回服务器错误: %d", providerState.Provider.Name, resp.StatusCode)
		p.providerManager.RecordFailure(providerState.Provider.Name)
	} else {
		// 成功响应，重置失败计数
		p.providerManager.RecordSuccess(providerState.Provider.Name)
	}

	// 计算耗时并记录统一的HTTP请求日志
	duration := time.Since(startTime)
	logger.LogHTTPRequest(requestID, r.Method, r.URL.Path, resp.StatusCode, duration, providerState.Provider.Name)

	// 复制响应
	p.copyResponse(w, resp)
	return nil
}

// handleUI 处理 /ui/ 管理界面路由（预留）
func (p *ProxyServer) handleUI(w http.ResponseWriter, r *http.Request) {
	logger.Info(logger.ModuleProxy, "接收到管理界面请求: %s %s", r.Method, r.URL.Path)

	// 设置 HTML 响应头
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// 返回简单的占位页面
	html := `<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Claude Code Env - 管理界面</title>
    <style>
        body { 
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; 
            max-width: 800px; margin: 2rem auto; padding: 1rem; 
            background: #f5f5f5; color: #333;
        }
        .container { 
            background: white; padding: 2rem; border-radius: 8px; 
            box-shadow: 0 2px 10px rgba(0,0,0,0.1);
        }
        .header { color: #2563eb; border-bottom: 2px solid #e5e7eb; padding-bottom: 1rem; margin-bottom: 2rem; }
        .status { background: #f0f9ff; border: 1px solid #bae6fd; padding: 1rem; border-radius: 4px; margin: 1rem 0; }
        .future { background: #f9fafb; border: 1px solid #d1d5db; padding: 1rem; border-radius: 4px; }
        .version { color: #6b7280; font-size: 0.875rem; margin-top: 2rem; }
    </style>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚀 Claude Code Env 管理界面</h1>
            <p>透明代理服务管理控制台</p>
        </div>
        
        <div class="status">
            <h3>📊 当前状态</h3>
            <p>✅ 代理服务器正在运行</p>
            <p>✅ Provider 管理器已就绪</p>
            <p>✅ 支持 /v1/* 和 /health 端点转发</p>
        </div>
        
        <div class="future">
            <h3>🎯 即将推出的功能</h3>
            <ul>
                <li>📝 Provider 配置在线编辑</li>
                <li>📈 实时监控和统计</li>
                <li>📋 日志查看和搜索</li>
                <li>🔧 系统状态和健康检查</li>
                <li>⚙️ 动态配置热重载</li>
                <li>🔐 访问控制和安全设置</li>
            </ul>
        </div>
        
        <div class="future">
            <h3>📖 当前可用功能</h3>
            <p>• <strong>API 转发</strong>: 所有 /v1/* 请求已支持</p>
            <p>• <strong>配置管理</strong>: 编辑 ~/.claude-code-env/settings.json</p>
            <p>• <strong>日志查看</strong>: 使用 <code>ccenv logs</code> 命令</p>
            <p>• <strong>健康检查</strong>: 访问 /health 端点</p>
        </div>
        
        <div class="version">
            <p>Claude Code Env v2.0 | 请求路径: ` + r.URL.Path + `</p>
        </div>
    </div>
</body>
</html>`

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}
