#!/bin/bash

# 调试脚本：模拟 claude 命令，显示接收到的所有参数和输入
# 用法：在 executor.go 中临时替换 "claude" 为 "./tools/debug-claude.sh" 来调试参数传递

echo "========== DEBUG: 模拟 claude 命令 =========="
echo "脚本名称: $0"
echo "参数数量: $#"
echo "所有参数: $@"

echo ""
echo "逐个参数："
for i in $(seq 1 $#); do
    echo "  \$${i}: ${!i}"
done

echo ""
echo "标准输入内容："
if [ -t 0 ]; then
    echo "  (无管道输入，标准输入来自终端)"
else
    echo "  管道输入内容如下："
    while IFS= read -r line || [[ -n "$line" ]]; do
        echo "    |> $line"
    done
fi

echo ""
echo "环境变量（ANTHROPIC_*）："
env | grep "^ANTHROPIC_" | while read -r line; do
    echo "  $line"
done

echo ""
echo "如果这是真的 claude 命令，会执行："
echo "  claude $@"
echo "=============================================="

# 只有在终端模式下才提示按键
if [ -t 0 ]; then
    read -p "按回车键继续..."
fi