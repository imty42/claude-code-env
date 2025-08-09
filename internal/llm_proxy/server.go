package llm_proxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// LLMProxyServer LLM API代理服务器
type LLMProxyServer struct {
	server          *http.Server
	providerManager *provider.ProviderManager
	httpClient      *http.Client
	host            string
	port            int
}

// NewLLMProxyServer 创建新的LLM代理服务器
func NewLLMProxyServer(providerManager *provider.ProviderManager, host string, port int, apiProxy string, timeout time.Duration) *LLMProxyServer {
	// 创建带代理配置的 HTTP 客户端
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: false},
	}

	// 设置代理
	if apiProxy != "" {
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			return url.Parse(apiProxy)
		}
		logger.Info(logger.ModuleProxy, "LLM API服务器配置API代理: %s", apiProxy)
	}

	apiServer := &LLMProxyServer{
		providerManager: providerManager,
		host:            host,
		port:            port,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
	}

	// 创建路由器
	mux := http.NewServeMux()

	// 注册LLM API相关路由
	mux.HandleFunc("/v1/messages", apiServer.handleMessages)
	mux.HandleFunc("/v1/", apiServer.handleV1Routes)
	mux.HandleFunc("/health", apiServer.handleHealth)

	// 创建服务器
	apiServer.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: mux,
	}

	return apiServer
}

// Start 启动LLM代理服务器
func (s *LLMProxyServer) Start() error {
	logger.Info(logger.ModuleProxy, "启动LLM API服务器: http://%s:%d", s.host, s.port)

	go func() {
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error(logger.ModuleProxy, "LLM API服务器启动失败: %v", err)
		}
	}()

	// 等待一小段时间确保服务器启动
	time.Sleep(50 * time.Millisecond)
	return nil
}

// Shutdown 关闭LLM代理服务器
func (s *LLMProxyServer) Shutdown() error {
	logger.Info(logger.ModuleProxy, "关闭LLM API服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.server.Shutdown(ctx)
}

// handleMessages 处理 /v1/messages 请求
func (s *LLMProxyServer) handleMessages(w http.ResponseWriter, r *http.Request) {
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
	providerState, err := s.providerManager.GetNextProvider()
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
		s.providerManager.RecordFailure(providerState.Provider.Name)
		return
	}
	r.Body.Close()

	// 解析和修改请求体（如果需要模型映射）
	modifiedBody := bodyBytes
	targetModel := providerState.Provider.Env["ANTHROPIC_MODEL"]
	if targetModel != "" {
		modifiedBody, err = s.modifyRequestModel(bodyBytes, targetModel, providerState.Provider.Name)
		if err != nil {
			logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "修改请求模型失败: %v", err)
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "修改请求模型失败")
			s.providerManager.RecordFailure(providerState.Provider.Name)
			return
		}
	} else {
		// 没有配置模型映射，也要打印当前请求的模型
		s.logRequestModel(bodyBytes, providerState.Provider.Name, requestID)
	}

	// 构建目标 URL
	baseURL := providerState.Provider.Env["ANTHROPIC_BASE_URL"]
	targetURL := baseURL + "/v1/messages"

	// 创建代理请求
	proxyReq, err := http.NewRequest("POST", targetURL, bytes.NewReader(modifiedBody))
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "创建请求失败: %v", err)
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "创建请求失败")
		s.providerManager.RecordFailure(providerState.Provider.Name)
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

	// 确保 Content-Length 正确
	proxyReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(modifiedBody)))

	// 发送请求
	resp, err := s.httpClient.Do(proxyReq)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "请求失败: %v", err)
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "请求转发失败")
		s.providerManager.RecordFailure(providerState.Provider.Name)
		return
	}
	defer resp.Body.Close()

	// 检查响应状态码，5xx 错误视为 provider 失败
	if resp.StatusCode >= 500 {
		logger.Warn(logger.ModuleProxy, "Provider %s 返回服务器错误: %d", providerState.Provider.Name, resp.StatusCode)
		s.providerManager.RecordFailure(providerState.Provider.Name)
	} else {
		// 成功响应，重置失败计数
		s.providerManager.RecordSuccess(providerState.Provider.Name)
	}

	// 计算耗时并记录统一的HTTP请求日志
	duration := time.Since(startTime)
	logger.LogHTTPRequest(requestID, r.Method, r.URL.Path, resp.StatusCode, duration, providerState.Provider.Name)

	// 复制响应
	s.copyResponse(w, resp)
}

// modifyRequestModel 修改请求中的模型
func (s *LLMProxyServer) modifyRequestModel(bodyBytes []byte, targetModel, providerName string) ([]byte, error) {
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
func (s *LLMProxyServer) logRequestModel(bodyBytes []byte, providerName, requestID string) {
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
func (s *LLMProxyServer) copyResponse(w http.ResponseWriter, resp *http.Response) {
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
					logger.Error(logger.ModuleProxy, "读取LLM API响应失败: %v", err)
				}
				break
			}
		}
	} else {
		io.Copy(w, resp.Body)
	}
}

// handleV1Routes 处理除 /v1/messages 外的其他 /v1/* 路由
func (s *LLMProxyServer) handleV1Routes(w http.ResponseWriter, r *http.Request) {
	// 跳过 /v1/messages，它有专门的处理器
	if r.URL.Path == "/v1/messages" {
		s.handleMessages(w, r)
		return
	}

	// 生成请求追踪ID
	requestID := logger.GenerateRequestID()
	startTime := time.Now()

	// 记录请求
	logger.DebugWithRequestID(logger.ModuleProxy, requestID, "%s %s", r.Method, r.URL.Path)

	// 获取下一个可用的 provider
	providerState, err := s.providerManager.GetNextProvider()
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
	err = s.forwardRequest(w, r, targetURL, providerState, startTime, requestID)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "转发请求失败 %s %s: %v", r.Method, r.URL.Path, err)
	}
}

// handleHealth 处理 LLM代理健康检查
func (s *LLMProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	// 生成请求追踪ID
	requestID := logger.GenerateRequestID()
	startTime := time.Now()

	logger.DebugWithRequestID(logger.ModuleProxy, requestID, "LLM API Health %s %s", r.Method, r.URL.Path)

	// 获取下一个可用的 provider
	providerState, err := s.providerManager.GetNextProvider()
	if err != nil {
		logger.Warn(logger.ModuleProxy, "LLM API健康检查时无可用 provider: %v", err)
		writeAnthropicError(w, http.StatusServiceUnavailable, "overloaded_error", "无可用的服务提供商")
		return
	}

	// 构建目标 URL
	baseURL := providerState.Provider.Env["ANTHROPIC_BASE_URL"]
	targetURL := baseURL + "/health"

	// 转发请求
	err = s.forwardRequest(w, r, targetURL, providerState, startTime, requestID)
	if err != nil {
		logger.ErrorWithRequestID(logger.ModuleProxy, requestID, "LLM API健康检查请求转发失败: %v", err)
	}
}

// forwardRequest 通用的请求转发函数
func (s *LLMProxyServer) forwardRequest(w http.ResponseWriter, r *http.Request, targetURL string, providerState *provider.ProviderState, startTime time.Time, requestID string) error {
	// 读取请求体
	var bodyBytes []byte
	var err error
	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, "invalid_request_error", "读取请求体失败")
			s.providerManager.RecordFailure(providerState.Provider.Name)
			return fmt.Errorf("读取请求体失败: %v", err)
		}
		r.Body.Close()
	}

	// 创建代理请求
	proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "创建请求失败")
		s.providerManager.RecordFailure(providerState.Provider.Name)
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
	resp, err := s.httpClient.Do(proxyReq)
	if err != nil {
		writeAnthropicError(w, http.StatusBadGateway, "api_error", "请求转发失败")
		s.providerManager.RecordFailure(providerState.Provider.Name)
		return fmt.Errorf("请求转发失败: %v", err)
	}
	defer resp.Body.Close()

	// 检查响应状态码，5xx 错误视为 provider 失败
	if resp.StatusCode >= 500 {
		logger.Warn(logger.ModuleProxy, "Provider %s 返回服务器错误: %d", providerState.Provider.Name, resp.StatusCode)
		s.providerManager.RecordFailure(providerState.Provider.Name)
	} else {
		// 成功响应，重置失败计数
		s.providerManager.RecordSuccess(providerState.Provider.Name)
	}

	// 计算耗时并记录统一的HTTP请求日志
	duration := time.Since(startTime)
	logger.LogHTTPRequest(requestID, r.Method, r.URL.Path, resp.StatusCode, duration, providerState.Provider.Name)

	// 复制响应
	s.copyResponse(w, resp)
	return nil
}