# goweight 使用文档

## 概述

goweight 是一个用于分析 Go 项目依赖大小的工具。它可以分析静态链接后的二进制文件，帮助开发者了解项目中各个依赖包对最终二进制文件大小的贡献。

## 原理

### 1. 静态链接后分析（默认模式）

goweight 通过以下步骤分析最终的二进制文件大小：

1. 使用 `go build` 构建项目，生成静态链接的二进制文件
2. 使用 `debug/buildinfo` 包读取二进制文件中的构建信息
3. 分析二进制文件的符号表来估算各包的大小
4. 支持多种二进制格式（ELF、Mach-O、PE）

### 2. 构建过程分析（实验性功能）

通过分析 Go 编译器的构建过程来了解各包的编译大小：

1. 使用 `go build -work -a -x` 命令显示详细的构建过程
2. 解析构建输出中的编译命令
3. 注意：由于 Go 的临时工作目录机制，此模式目前仅能显示包名，无法获取实际大小

## 安装

```bash
go install github.com/jondot/goweight@latest
```

或者克隆项目并构建：

```bash
git clone https://github.com/jondot/goweight.git
cd goweight
go build .
```

## 使用方法

### 基本用法

```bash
# 分析当前项目（默认模式）
goweight

# 分析指定的包
goweight github.com/user/project/cmd/app

# 详细输出（显示所有包）
goweight -v

# 输出 JSON 格式
goweight -j
```

### 分析现有二进制文件

```bash
# 分析已存在的二进制文件
goweight -b /path/to/binary

# 对现有二进制文件进行详细分析
goweight -b /path/to/binary -v
```

### 构建过程分析（实验性）

```bash
# 分析构建过程（实验性功能）
goweight --build-analysis

# 对指定包进行构建过程分析
goweight --build-analysis ./cmd/app

# 结合详细输出
goweight --build-analysis -v ./cmd/app
```

### 其他选项

```bash
# 使用构建标签
goweight --tags="tag1,tag2"

# 使用 JSON 输出
goweight -j

# 组合使用多个选项
goweight --tags="prod" -v -j ./cmd/app
```

## 输出说明

输出格式为：

```
  SIZE  PACKAGE_NAME
```

例如：
```
284 kB github.com/thoas/go-funk
196 kB github.com/alecthomas/kingpin/v2
 66 kB github.com/dustin/go-humanize
 20 kB github.com/xhit/go-str2duration/v2
 20 kB github.com/alecthomas/units
  0 B github.com/jondot/goweight
```

## 功能特点

1. **静态链接分析**：分析最终二进制文件，反映真实的大小贡献
2. **多平台支持**：支持 Linux (ELF)、macOS (Mach-O)、Windows (PE) 格式
3. **聚合显示**：默认按顶级包聚合显示，简洁明了
4. **详细模式**：可选择显示所有包的详细信息
5. **JSON 输出**：支持机器可读的 JSON 格式输出
6. **二进制文件分析**：可以直接分析已存在的二进制文件

## 限制和注意事项

1. **标准库分析**：由于 Go 编译器的优化，标准库的大小可能不会在最终二进制文件中明确分离
2. **构建过程分析**：由于 Go 的临时工作目录机制，实验性的构建过程分析功能目前无法显示实际文件大小
3. **符号表依赖**：二进制文件分析依赖于符号表信息，如果编译时去除了符号表，分析精度会降低
4. **交叉编译**：分析结果可能受编译目标平台影响

## 故障排除

### 常见问题

1. **权限错误**：确保对工作目录有适当的读写权限
2. **找不到包**：确保在正确的模块上下文中运行
3. **构建失败**：确保项目可以正常构建

### 调试技巧

- 使用 `-v` 标志获取更详细的输出
- 检查 `go env` 设置是否正确
- 确保 Go 版本兼容

## 贡献

欢迎提交 issue 和 pull request 来改进 goweight。