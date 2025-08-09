# Claude Code Env 2.1

Claude Code 的智能代理启动器，提供透明代理、多provider支持、双端口服务架构。

## ✨ 核心特性

### 🔄 透明代理模式
- 自动启动LLM API代理服务器，无缝接管Claude Code API请求
- 支持模型映射，将请求模型自动转换为目标服务商支持的模型
- 完全兼容Claude Code原生体验

### 🌐 双端口架构
- **LLM API服务** - 端口9999，处理所有LLM API请求和健康检查
- **管理服务** - 端口9998，提供Web管理界面
- **服务路由管理器** - 统一管理两个服务的生命周期，故障隔离

### 🎯 多Provider支持
- 支持配置多个API服务商（如SiliconFlow、官方API等）
- 两种路由策略：`default`（故障转移）、`robin`（轮询负载均衡）
- 智能故障检测：累计5次失败自动禁用5分钟

### 🔐 双认证方式
- `ANTHROPIC_AUTH_TOKEN`：Bearer Token认证（推荐）
- `ANTHROPIC_API_KEY`：X-Api-Key认证（Claude SDK兼容）
- 自动识别并使用正确的认证头

### 📊 实时监控
- 统一日志系统，支持DEBUG/INFO/WARN/ERROR级别
- 请求时间监控和provider使用统计
- 详细的错误信息和故障诊断
- **请求追踪ID** - 每个请求分配唯一ID，便于故障排查

### ⚡ 热重载
- 配置文件修改自动检测，无需重启服务
- 保持Claude Code会话连续性
- 自动重置provider故障计数

## 🚀 快速开始

### 安装

```bash
git clone https://github.com/imty42/claude-code-env.git
cd claude-code-env
go build -o ccenv ./cmd/ccenv
```

### 基础用法

```bash

# 启动代理服务（测试用）
./ccenv start

# 查看配置信息
./ccenv config

# 查看日志
./ccenv logs
./ccenv logs -f
```

## 📝 配置管理

### 配置文件位置
`~/.claude-code-env/settings.json`

### 完整配置示例

```json
{
  "version": "2.0",
  "CCENV_HOST": "127.0.0.1",
  "LLM_PROXY_PORT": 9999,
  "ADMIN_PORT": 9998,
  "API_PROXY": "http://127.0.0.1:7890",
  "LOGGING_LEVEL": "INFO",
  "API_TIMEOUT_MS": 600000,
  "providers": [
    {
      "name": "siliconflow-primary",
      "state": "on",
      "env": {
        "ANTHROPIC_BASE_URL": "https://api.siliconflow.cn",
        "ANTHROPIC_AUTH_TOKEN": "sk-your-token",
        "ANTHROPIC_MODEL": "deepseek-ai/DeepSeek-V3"
      }
    },
    {
      "name": "siliconflow-backup",
      "state": "on",
      "env": {
        "ANTHROPIC_BASE_URL": "https://api.siliconflow.cn",
        "ANTHROPIC_API_KEY": "sk-your-api-key",
        "ANTHROPIC_MODEL": "deepseek-ai/DeepSeek-V3"
      }
    }
  ],
  "routing": {
    "strategy": "default"
  }
}
```

### 配置说明

#### 基础配置
- `CCENV_HOST`: 服务器绑定主机（默认：127.0.0.1）
- `LLM_PROXY_PORT`: LLM API代理端口（默认：9999）
- `ADMIN_PORT`: 管理服务端口（默认：9998）
- `API_PROXY`: HTTP/HTTPS代理设置（可选）
- `LOGGING_LEVEL`: 日志级别（DEBUG/INFO/WARN/ERROR）
- `API_TIMEOUT_MS`: API请求超时时间（毫秒）

#### Provider配置
- `name`: Provider唯一标识符
- `state`: 启用状态（"on"/"off"）
- `env.ANTHROPIC_BASE_URL`: API服务地址
- `env.ANTHROPIC_AUTH_TOKEN`: Bearer认证Token（优先）
- `env.ANTHROPIC_API_KEY`: API Key认证（备选）
- `env.ANTHROPIC_MODEL`: 目标模型名称（用于模型映射）

#### 路由策略
- `default`: 按配置顺序故障转移，优先使用第一个可用provider
- `robin`: 轮询负载均衡，在可用providers间平均分配请求

## 🔧 服务接口

### LLM API服务 (端口9999)
- `GET /health` - 健康检查
- `GET /v1/*` - API代理路由
- `POST /v1/messages` - Claude消息接口（支持模型映射）

### 管理服务 (端口9998)
- `GET /` - Web管理界面

## 📊 监控和日志

### 日志查看
```bash
# 查看最近的日志
./ccenv logs

# 实时跟踪日志输出
./ccenv logs -f

# 查看最后50行日志
./ccenv logs -n 50
```

### 日志输出示例
```
[INFO] PROXY 启动服务路由管理器: LLM代理端口=9999, 管理端口=9998
[INFO] PROXY 启动LLM API服务器: http://127.0.0.1:9999
[INFO] PROXY 启动管理服务器: http://127.0.0.1:9998
[INFO] PROXY 模型映射: claude-3-5-sonnet -> deepseek-ai/DeepSeek-V3 (provider: siliconflow-primary)
[DEBUG] PROXY POST /v1/messages -> 200 (1.2s) [provider: siliconflow-primary]
```

### 配置查看
```bash
./ccenv config
```
显示完整配置信息，敏感信息自动打码：
```
=== Claude Code Env 配置信息 ===
版本: 2.0
代理主机: 127.0.0.1
LLM代理端口: 9999
管理端口: 9998
日志级别: INFO

=== Provider 配置 (2个) ===
[1] siliconflow-primary
  状态: on
  环境变量:
    ANTHROPIC_AUTH_TOKEN: sk-1****
    ANTHROPIC_MODEL: deepseek-ai/DeepSeek-V3
```

## 🚨 错误处理

### 标准化错误响应
所有错误都返回Anthropic API兼容的JSON格式：

```json
{
  "type": "error",
  "error": {
    "type": "overloaded_error",
    "message": "CCENV 无可用的服务提供商"
  }
}
```

### 端口冲突处理
当端口被占用时，系统会显示详细信息：

```
错误: 端口 [9999, 9998] 已被占用，无法启动代理服务
解决方案:
  1. 修改配置文件中的端口配置 (LLM_PROXY_PORT: 9999, ADMIN_PORT: 9998)
  2. 或使用 'lsof -i' 查找并停止占用进程
```

## 🔨 项目结构

```
claude-code-env/
├── cmd/ccenv/                   # 主程序入口
├── internal/
│   ├── admin/                   # 管理服务器
│   ├── config/                  # 配置管理和文件监控
│   ├── executor/                # 核心执行逻辑
│   ├── llm_proxy/               # LLM API代理服务器
│   ├── logger/                  # 统一日志系统
│   ├── provider/                # Provider管理和路由
│   └── server_routing_manager/  # 服务路由管理器
├── tools/                       # 开发工具
│   ├── build.sh                 # 多平台构建脚本
│   └── debug-claude.sh          # Claude命令调试脚本
├── go.mod
├── go.sum
└── README.md
```

## 🔨 构建

### 使用构建脚本（推荐）
```bash
# 构建当前平台版本
./tools/build.sh

# 构建所有支持平台
./tools/build.sh all
```

### 手动构建
```bash
# 开发构建
go build -o ccenv ./cmd/ccenv

# 生产构建（带版本信息）
go build -ldflags "-X main.Version=v2.0.0" -o ccenv ./cmd/ccenv
```

## 📄 版本历史

### v2.0.0
- ✅ 透明代理模式重构
- ✅ 多Provider支持和路由策略
- ✅ 双认证方式支持
- ✅ 配置文件热重载
- ✅ 统一错误处理格式
- ✅ **参数透传功能** - `ccenv code` 支持将参数完全透传给 Claude Code
- ✅ **日志查看命令** - 新增 `ccenv logs` 命令，支持实时跟踪和参数透传
- ✅ **双端口架构** - LLM API服务(9999)和管理服务(9998)完全分离
- ✅ **Web管理界面** - 提供简洁的Web界面用于服务状态查看
- ✅ **服务路由管理器** - 统一管理LLM代理和管理服务的生命周期
- ✅ **架构简化** - 移除复杂的客户端管理，专注于核心代理功能
- ✅ **日志系统增强** - 新增请求追踪ID、错误调用栈、减少日志噪音

## 🤝 贡献指南

欢迎提交Issue和Pull Request！

## 📜 许可证

MIT License