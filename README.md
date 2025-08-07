# Claude Code Env 2.0

Claude Code 的智能代理启动器，提供透明代理、多provider支持、自动故障切换等实用功能。

## ✨ 核心特性

### 🔄 透明代理模式
- 自动启动HTTP代理服务器，无缝接管Claude Code API请求
- 支持模型映射，将请求模型自动转换为目标服务商支持的模型
- 完全兼容Claude Code原生体验

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
# 启动Claude Code交互模式（推荐）
./ccenv code

# 启动Claude Code并传递初始消息
./ccenv code "分析这个项目"

# 启动Claude Code并传递Claude参数
./ccenv code --help
./ccenv code --resume

# 支持管道输入
echo "代码内容" | ./ccenv code "请分析这段代码"

# 启动代理服务（测试用）
./ccenv start

# 查看配置信息
./ccenv config

# 显示帮助
./ccenv help
```

**参数透传说明**：
- `ccenv code` 之后的所有参数都会直接传递给 `claude` 命令
- 支持所有 Claude Code 的原生参数和功能
- 支持管道输入和标准输入重定向

## 📝 配置管理

### 配置文件位置
`~/.claude-code-env/settings.json`

### 完整配置示例

```json
{
  "version": "2.0",
  "CCENV_HOST": "localhost",
  "CCENV_PORT": 9999,
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
- `CCENV_HOST`: 代理服务器绑定主机（默认：localhost）
- `CCENV_PORT`: 代理服务器端口（默认：9999）
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

## 🔧 高级功能

### 模型映射
自动将Claude Code的模型请求转换为目标服务商支持的模型：

```
Claude请求: claude-3.5-sonnet
映射后: deepseek-ai/DeepSeek-V3
```

### 故障恢复
- 自动检测provider响应状态
- 5xx错误自动记录故障
- 累计5次故障则禁用5分钟
- 禁用期满自动重新启用

### 配置热重载
修改配置文件后自动：
1. 重新加载配置
2. 重启代理服务
3. 重置故障计数
4. 更新日志级别

### API代理支持
支持通过HTTP/HTTPS代理访问API服务：

```json
{
  "API_PROXY": "http://127.0.0.1:7890"
}
```

根据URL前缀自动设置相应的环境变量。

## 📊 监控和日志

### 日志输出示例
```
[INFO] PROVIDER 初始化 ProviderManager，策略: default，providers 数量: 2
[INFO] PROVIDER Provider: siliconflow-primary, State: on
[INFO] PROVIDER Provider: siliconflow-backup, State: off
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
代理主机: localhost
代理端口: 9999
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

### 常见错误类型
- `overloaded_error`: 无可用provider
- `api_error`: 请求转发失败
- `invalid_request_error`: 请求格式错误

## 🔨 开发和构建

### 项目结构
```
claude-code-env/
├── cmd/ccenv/           # 主程序入口
├── internal/
│   ├── config/          # 配置管理和文件监控
│   ├── executor/        # 核心执行逻辑
│   ├── logger/          # 统一日志系统
│   ├── provider/        # Provider管理和路由
│   └── proxy/           # HTTP代理服务器
├── tools/               # 开发工具
│   ├── build.sh         # 多平台构建脚本
│   └── debug-claude.sh  # Claude命令调试脚本
├── dist/                # 构建输出目录
├── go.mod
├── go.sum
└── README.md
```

### 构建命令

#### 使用构建脚本（推荐）
```bash
# 构建当前平台版本
./tools/build.sh

# 构建所有支持平台
./tools/build.sh all

# 构建特定平台
./tools/build.sh linux
./tools/build.sh darwin
./tools/build.sh windows

# 查看构建选项
./tools/build.sh help
```

#### 手动构建
```bash
# 开发构建
go build -o ccenv ./cmd/ccenv

# 生产构建（带版本信息）
go build -ldflags "-X main.Version=v2.0.0" -o ccenv ./cmd/ccenv
```

#### 开发调试
```bash
# 调试参数传递（在 executor.go 中临时替换 "claude" 为该脚本）
./tools/debug-claude.sh
```

## 📄 版本历史

### v2.0.0
- ✅ 透明代理模式重构
- ✅ 多Provider支持和路由策略
- ✅ 双认证方式支持
- ✅ 配置文件热重载
- ✅ 统一错误处理格式
- ✅ 完整的监控和日志系统
- ✅ **参数透传功能** - `ccenv code` 支持将参数完全透传给 Claude Code
- ✅ **工具脚本整理** - 构建脚本和调试工具移至 `tools/` 目录

### v1.0.0
- 基础环境变量传递模式
- 单一服务商支持

## 🤝 贡献指南

欢迎提交Issue和Pull Request！

## 📜 许可证

MIT License
