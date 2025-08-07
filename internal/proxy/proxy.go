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
	port            int // 服务端口
	httpClient      *http.Client
}

// NewProxyServer 创建新的代理服务器
func NewProxyServer(providerManager *provider.ProviderManager, port int, apiProxy string) *ProxyServer {
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
		port:            port,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/messages", proxy.handleMessages)

	proxy.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	return proxy
}

// handleMessages 处理 /v1/messages 请求
func (p *ProxyServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	// 只处理 POST 请求
	if r.Method != "POST" {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "invalid_request_error", "仅支持 POST 请求")
		return
	}

	// 开始计时
	startTime := time.Now()

	// 获取下一个可用的 provider
	providerState, err := p.providerManager.GetNextProvider()
	if err != nil {
		logger.Error(logger.ModuleProxy, "获取可用 provider 失败: %v", err)
		writeAnthropicError(w, http.StatusServiceUnavailable, "overloaded_error", "无可用的服务提供商")
		return
	}

	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error(logger.ModuleProxy, "读取请求体失败: %v", err)
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
			logger.Error(logger.ModuleProxy, "修改请求模型失败: %v", err)
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "修改请求模型失败")
			p.providerManager.RecordFailure(providerState.Provider.Name)
			return
		}
	} else {
		// 没有配置模型映射，也要打印当前请求的模型
		p.logRequestModel(bodyBytes, providerState.Provider.Name)
	}

	// 构建目标 URL
	baseURL := providerState.Provider.Env["ANTHROPIC_BASE_URL"]
	targetURL := baseURL + "/v1/messages"

	// 创建代理请求
	proxyReq, err := http.NewRequest("POST", targetURL, bytes.NewReader(modifiedBody))
	if err != nil {
		logger.Error(logger.ModuleProxy, "创建请求失败: %v", err)
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
		logger.Error(logger.ModuleProxy, "请求失败: %v", err)
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

	// 计算耗时并记录日志
	duration := time.Since(startTime)
	logger.Debug(logger.ModuleProxy, "%s %s -> %d (%v) [provider: %s]", 
		r.Method, r.URL.Path, resp.StatusCode, duration, providerState.Provider.Name)

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
			logger.Info(logger.ModuleProxy, "模型映射: %s -> %s (provider: %s)", originalModel, targetModel, providerName)
		} else {
			logger.Info(logger.ModuleProxy, "请求模型: %s (provider: %s)", originalModel, providerName)
		}
	} else {
		logger.Info(logger.ModuleProxy, "请求模型: %s (provider: %s)", targetModel, providerName)
	}

	// 重新序列化
	modifiedBytes, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("序列化JSON失败: %v", err)
	}

	return modifiedBytes, nil
}

// logRequestModel 记录请求模型（用于没有模型映射的情况）
func (p *ProxyServer) logRequestModel(bodyBytes []byte, providerName string) {
	var requestBody map[string]interface{}
	
	err := json.Unmarshal(bodyBytes, &requestBody)
	if err != nil {
		logger.Debug(logger.ModuleProxy, "解析请求体失败，无法获取模型信息: %v", err)
		return
	}

	// 获取请求模型
	if model, exists := requestBody["model"]; exists {
		if modelStr, ok := model.(string); ok {
			logger.Info(logger.ModuleProxy, "请求模型: %s (provider: %s)", modelStr, providerName)
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
	logger.Info(logger.ModuleProxy, "启动代理服务器: http://127.0.0.1:%d", p.port)
	logger.Info(logger.ModuleProxy, "多Provider透明代理模式")
	
	go func() {
		if err := p.server.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error(logger.ModuleProxy, "服务器启动失败: %v", err)
		}
	}()
	
	// 等待一小段时间确保服务器启动
	time.Sleep(100 * time.Millisecond)
	return nil
}

// Shutdown 关闭代理服务器
func (p *ProxyServer) Shutdown() error {
	logger.Info(logger.ModuleProxy, "关闭代理服务器...")
	
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	return p.server.Shutdown(ctx)
}