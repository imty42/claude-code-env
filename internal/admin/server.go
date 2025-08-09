package admin

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/imty42/claude-code-env/internal/logger"
)

// AdminServer 管理服务器 - 简化版本，仅提供Web管理界面
type AdminServer struct {
	server *http.Server
	host   string
	port   int
}

// NewAdminServer 创建新的管理服务器
func NewAdminServer(host string, port int) *AdminServer {
	adminServer := &AdminServer{
		host: host,
		port: port,
	}

	// 创建路由器
	mux := http.NewServeMux()

	// 仅注册Web管理界面路由
	mux.HandleFunc("/", adminServer.handleUI)

	// 创建服务器
	adminServer.server = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", host, port),
		Handler: mux,
	}

	return adminServer
}

// Start 启动管理服务器
func (s *AdminServer) Start() error {
	logger.Info(logger.ModuleProxy, "启动管理服务器: http://%s:%d", s.host, s.port)

	go func() {
		if err := s.server.ListenAndServe(); err != http.ErrServerClosed {
			logger.Error(logger.ModuleProxy, "管理服务器启动失败: %v", err)
		}
	}()

	// 等待一小段时间确保服务器启动
	time.Sleep(50 * time.Millisecond)
	return nil
}

// Shutdown 关闭管理服务器
func (s *AdminServer) Shutdown() error {
	logger.Info(logger.ModuleProxy, "关闭管理服务器...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return s.server.Shutdown(ctx)
}

// handleUI 处理管理界面路由
func (s *AdminServer) handleUI(w http.ResponseWriter, r *http.Request) {
	logger.Info(logger.ModuleProxy, "接收到管理界面请求: %s %s", r.Method, r.URL.Path)

	// 设置 HTML 响应头
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// 返回简化的管理界面
	html := fmt.Sprintf(`<!DOCTYPE html>
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
        .refresh { float: right; background: #2563eb; color: white; padding: 0.5rem 1rem; text-decoration: none; border-radius: 4px; }
        .refresh:hover { background: #1d4ed8; }
    </style>
    <script>
        function refreshPage() {
            location.reload();
        }
        // 自动刷新页面
        setInterval(refreshPage, 60000); // 60秒刷新一次
    </script>
</head>
<body>
    <div class="container">
        <div class="header">
            <h1>🚀 Claude Code Env 管理界面</h1>
            <p>LLM代理服务管理控制台</p>
            <a href="javascript:refreshPage()" class="refresh">刷新</a>
        </div>
        
        <div class="status">
            <h3>📊 服务状态</h3>
            <p>✅ 管理服务器正在运行 (端口: %d)</p>
            <p>✅ LLM代理服务正在运行</p>
            <p>✅ 服务时间: %s</p>
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
            <p>• <strong>LLM API代理</strong>: 端口9999，支持所有 /v1/* 请求</p>
            <p>• <strong>管理界面</strong>: 端口9998，Web管理控制台</p>
            <p>• <strong>配置管理</strong>: 编辑 ~/.claude-code-env/settings.json</p>
            <p>• <strong>日志查看</strong>: 使用 <code>ccenv logs</code> 命令</p>
            <p>• <strong>服务管理</strong>: 使用 <code>ccenv start/stop</code> 命令</p>
        </div>
        
        <div class="version">
            <p>Claude Code Env v2.0 Admin Server | 请求路径: %s</p>
            <p>页面将每60秒自动刷新</p>
        </div>
    </div>
</body>
</html>`, s.port, time.Now().Format("2006-01-02 15:04:05"), r.URL.Path)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(html))
}