#!/bin/bash

# Claude Code Env 多平台构建脚本
# 使用方法:
#   ./build.sh         - 为当前系统编译
#   ./build.sh all     - 编译所有支持的平台
#   ./build.sh linux   - 只编译 Linux 版本
#   ./build.sh darwin  - 只编译 macOS 版本
#   ./build.sh windows - 只编译 Windows 版本

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 项目信息
APP_NAME="cce"
VERSION=${VERSION:-"1.0.0"}
BUILD_DIR="dist"

# 支持的平台和架构（使用普通数组替代关联数组）
PLATFORMS=(
    "darwin/amd64:cce-darwin-amd64"
    "darwin/arm64:cce-darwin-arm64"
    "linux/amd64:cce-linux-amd64"
    "windows/amd64:cce-windows-amd64.exe"
)

# 打印信息
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

# 检查依赖
check_dependencies() {
    if ! command -v go &> /dev/null; then
        print_error "Go 未安装或未在 PATH 中"
        exit 1
    fi
    
    print_info "Go 版本: $(go version)"
}

# 创建构建目录
create_build_dir() {
    if [ -d "$BUILD_DIR" ]; then
        print_info "清理旧的构建目录..."
        rm -rf "$BUILD_DIR"
    fi
    
    mkdir -p "$BUILD_DIR"
    print_info "创建构建目录: $BUILD_DIR"
}

# 编译单个平台
build_platform() {
    local platform_info=$1
    local platform=${platform_info%:*}
    local output=${platform_info#*:}
    
    local goos=$(echo $platform | cut -d'/' -f1)
    local goarch=$(echo $platform | cut -d'/' -f2)
    
    print_info "编译 $platform -> $output"
    
    # 设置环境变量并编译
    GOOS=$goos GOARCH=$goarch go build -ldflags "-X main.Version=$VERSION" -o "$BUILD_DIR/$output" ./cmd/cce
    
    if [ $? -eq 0 ]; then
        print_success "编译完成: $BUILD_DIR/$output"
        
        # 显示文件大小
        if command -v ls &> /dev/null; then
            local size=$(ls -lh "$BUILD_DIR/$output" | awk '{print $5}')
            print_info "文件大小: $size"
        fi
        return 0
    else
        print_error "编译失败: $platform"
        return 1
    fi
}

# 编译所有平台
build_all() {
    print_info "开始编译所有支持的平台..."
    
    local success=0
    local total=${#PLATFORMS[@]}
    
    for platform_info in "${PLATFORMS[@]}"; do
        echo
        if build_platform "$platform_info"; then
            ((success++))
        fi
    done
    
    echo
    print_info "编译统计: $success/$total 成功"
    
    if [ $success -eq $total ]; then
        print_success "所有平台编译完成！"
    else
        print_warning "部分平台编译失败"
    fi
}

# 编译指定操作系统
build_os() {
    local target_os=$1
    local built=0
    
    print_info "编译 $target_os 平台..."
    
    for platform_info in "${PLATFORMS[@]}"; do
        local platform=${platform_info%:*}
        local os=$(echo $platform | cut -d'/' -f1)
        if [ "$os" = "$target_os" ]; then
            echo
            build_platform "$platform_info"
            ((built++))
        fi
    done
    
    if [ $built -eq 0 ]; then
        print_error "未找到支持的平台: $target_os"
        print_info "支持的平台: darwin, linux, windows"
        return 1
    fi
    
    print_success "$target_os 平台编译完成！"
}

# 编译当前系统
build_current() {
    local current_os=$(go env GOOS)
    local current_arch=$(go env GOARCH)
    local platform="$current_os/$current_arch"
    
    print_info "检测到当前平台: $platform"
    
    # 查找匹配的平台
    local found=0
    local output=""
    for platform_info in "${PLATFORMS[@]}"; do
        local p=${platform_info%:*}
        local o=${platform_info#*:}
        if [ "$p" = "$platform" ]; then
            build_platform "$platform_info"
            found=1
            output="$o"
            break
        fi
    done
    
    if [ $found -eq 1 ]; then
        # 同时创建不带平台后缀的版本
        local simple_name="cce"
        if [ "$current_os" = "windows" ]; then
            simple_name="cce.exe"
        fi
        cp "$BUILD_DIR/$output" "$BUILD_DIR/$simple_name"
        print_info "创建当前平台版本: $BUILD_DIR/$simple_name"
    else
        print_error "当前平台 $platform 不在支持列表中"
        return 1
    fi
}

# 显示帮助信息
show_help() {
    echo "Claude Code Env 多平台构建脚本"
    echo
    echo "使用方法:"
    echo "  $0                  为当前系统编译"
    echo "  $0 all              编译所有支持的平台"
    echo "  $0 linux            只编译 Linux 版本"
    echo "  $0 darwin           只编译 macOS 版本"
    echo "  $0 windows          只编译 Windows 版本"
    echo "  $0 clean            清理构建目录"
    echo "  $0 help             显示此帮助信息"
    echo
    echo "支持的平台:"
    for platform_info in "${PLATFORMS[@]}"; do
        local platform=${platform_info%:*}
        local output=${platform_info#*:}
        echo "  $platform -> $output"
    done
    echo
    echo "环境变量:"
    echo "  VERSION             设置版本号 (默认: 1.0.0)"
}

# 清理构建目录
clean_build() {
    if [ -d "$BUILD_DIR" ]; then
        print_info "清理构建目录..."
        rm -rf "$BUILD_DIR"
        print_success "构建目录已清理"
    else
        print_info "构建目录不存在，无需清理"
    fi
}

# 主函数
main() {
    print_info "Claude Code Env 构建脚本 v$VERSION"
    
    # 检查依赖
    check_dependencies
    
    case "${1:-current}" in
        "all")
            create_build_dir
            build_all
            ;;
        "linux"|"darwin"|"windows")
            create_build_dir
            build_os "$1"
            ;;
        "current"|"")
            create_build_dir
            build_current
            ;;
        "clean")
            clean_build
            exit 0
            ;;
        "help"|"-h"|"--help")
            show_help
            exit 0
            ;;
        *)
            print_error "未知参数: $1"
            show_help
            exit 1
            ;;
    esac
    
    echo
    print_success "构建完成！输出目录: $BUILD_DIR"
    
    # 显示构建结果
    if [ -d "$BUILD_DIR" ] && [ "$(ls -A $BUILD_DIR)" ]; then
        print_info "构建结果:"
        ls -lh "$BUILD_DIR"
    fi
}

# 执行主函数
main "$@"