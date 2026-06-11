#!/bin/bash

# Protobuf 代码生成脚本

set -e

# 设置工作目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"
PROTO_DIR="$PROJECT_ROOT/api/proto"
GEN_DIR="$PROJECT_ROOT/api/gen"

# 创建生成目录
mkdir -p "$GEN_DIR"

echo "=== Protobuf Code Generation ==="
echo "Proto Dir: $PROTO_DIR"
echo "Gen Dir: $GEN_DIR"

# 安装 protoc 插件（如果需要）
# go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
# go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# 设置插件路径
export PATH="$PATH:$(go env GOPATH)/bin"

# 遍历 proto 文件并生成代码
for proto_file in $(find "$PROTO_DIR" -name "*.proto"); do
    echo "Processing: $proto_file"
    
    # 获取相对路径
    rel_path="${proto_file#$PROTO_DIR/}"
    dir_path=$(dirname "$rel_path")
    
    # 创建目标目录
    target_dir="$GEN_DIR/$dir_path"
    mkdir -p "$target_dir"
    
    # 构建 protoc 命令
    protoc_cmd="protoc \
        --go_out=$GEN_DIR \
        --go_opt=paths=source_relative \
        --go-grpc_out=$GEN_DIR \
        --go-grpc_opt=paths=source_relative \
        -I$PROTO_DIR"
    
    # 添加旧版 protobuf include 路径（如果存在）
    OLD_PROTO_PATH="$(go env GOPATH)/src/github.com/protocolbuffers/protobuf/src"
    if [ -d "$OLD_PROTO_PATH" ]; then
        protoc_cmd="$protoc_cmd -I$OLD_PROTO_PATH"
        echo "  Using legacy protobuf path: $OLD_PROTO_PATH"
    else
        echo "  Legacy protobuf path not found, using google.golang.org/protobuf"
    fi
    
    protoc_cmd="$protoc_cmd $proto_file"
    
    # 生成 Go 代码（包括 well-known types）
    eval $protoc_cmd
    
    echo "Generated: $target_dir"
done

echo "=== Code Generation Complete ==="
