package pkg

import (
	"debug/buildinfo"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/dustin/go-humanize"
)

var binaryModuleRegex = regexp.MustCompile(`^\s+(dep|mod)\s+([^\s]+)\s+([^\s]+)`)

// BuildAndAnalyzeBinary 构建项目并分析生成的二进制文件
func (g *GoWeight) BuildAndAnalyzeBinary() []*ModuleEntry {
	// 修改构建命令以生成二进制文件
	binaryBuildCmd := []string{"go", "build", "-o", "goweight-temp-binary"}
	
	// 如果原始命令中有额外参数，也添加到新命令中
	originalCmd := g.BuildCmd
	for i, arg := range originalCmd {
		if arg == "-o" && i+1 < len(originalCmd) {
			// 替换输出文件名为临时文件名
			binaryBuildCmd = append(binaryBuildCmd, "-o", "goweight-temp-binary")
			i++ // 跳过下一个参数（原输出文件名）
		} else if arg != "go" && arg != "build" {
			// 添加其他参数（如 -tags 等）
			if arg != "-work" && arg != "-a" { // 排除 -work 和 -a 参数
				binaryBuildCmd = append(binaryBuildCmd, arg)
			}
		}
	}

	// 执行构建命令
	out, err := exec.Command(binaryBuildCmd[0], binaryBuildCmd[1:]...).CombinedOutput()
	if err != nil {
		log.Fatalf("Error building binary: %v\nOutput: %s", err, out)
	}

	// 分析生成的二进制文件
	defer os.Remove("goweight-temp-binary") // 清理临时文件
	return g.ProcessBinary("goweight-temp-binary")
}

func (g *GoWeight) ProcessBinary(binaryPath string) []*ModuleEntry {
	// 首先使用 buildinfo 获取模块依赖信息
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		log.Fatalf("Error reading build info from binary %s: %v", binaryPath, err)
	}

	// 然后分析二进制文件的符号表来估算各包的大小
	pkgSizes, err := analyzeBinarySymbolTable(binaryPath)
	if err != nil {
		log.Printf("Warning: Could not analyze symbol table: %v", err)
		// 如果无法分析符号表，则尝试从模块缓存估算大小
	}

	var modules []*ModuleEntry

	// 添加主模块信息
	if info.Main.Path != "" {
		size := uint64(0)
		if s, exists := pkgSizes[info.Main.Path]; exists && s > 0 {
			size = s
		} else {
			// 如果符号表分析没有提供大小，尝试从模块缓存获取
			size = estimateModuleSize(info.Main.Path, info.Main.Version)
		}

		mainModule := &ModuleEntry{
			Path:      info.Main.Path,
			Name:      info.Main.Path,
			Version:   info.Main.Version,
			Size:      size,
			SizeHuman: humanize.Bytes(size),
		}
		if info.Main.Path != "" {
			modules = append(modules, mainModule)
		}
	}

	// 添加依赖模块信息
	for _, dep := range info.Deps {
		if dep != nil {
			size := uint64(0)
			if s, exists := pkgSizes[dep.Path]; exists && s > 0 {
				size = s
			} else {
				// 如果符号表分析没有提供大小，尝试从模块缓存获取
				size = estimateModuleSize(dep.Path, dep.Version)
			}

			depModule := &ModuleEntry{
				Path:      dep.Path,
				Name:      dep.Path,
				Version:   dep.Version,
				Size:      size,
				SizeHuman: humanize.Bytes(size),
			}
			modules = append(modules, depModule)
		}
	}

	// 按大小降序排序
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Size > modules[j].Size
	})

	return modules
}

// processBinaryModule 处理二进制模块信息
func processBinaryModule(line string) *ModuleEntry {
	captures := binaryModuleRegex.FindStringSubmatch(line)
	if captures == nil || len(captures) < 4 {
		return nil
	}
	modType := captures[1]
	path := captures[2]
	version := captures[3]

	var sz uint64
	if modType == "dep" {
		// 尝试从 GOPATH 模块缓存计算大小
		goPath := os.Getenv("GOPATH")
		if goPath == "" {
			// 使用默认 GOPATH
			goPath = filepath.Join(os.Getenv("HOME"), "go")
		}
		goModCachePath := filepath.Join(goPath, "pkg", "mod", path+"@"+version)

		// 检查模块缓存路径是否存在
		if _, err := os.Stat(goModCachePath); os.IsNotExist(err) {
			// 如果标准模块缓存路径不存在，尝试查找替代路径
			// 可能是替换模块或其他情况
			lookedFor := goModCachePath
			log.Printf("Warning: module cache path does not exist: %s", lookedFor)

			// 尝试从 GOPROXY 路径查找
			goProxy := os.Getenv("GOPROXY")
			if goProxy != "" && goProxy != "direct" && goProxy != "off" {
				// 如果设置了代理，尝试从代理下载路径查找
				// 这里简化处理，实际可能需要更复杂的逻辑
			}

			// 返回一个大小为0的条目，而不是完全忽略它
			sz = 0
		} else {
			sz = calculateDirSize(goModCachePath)
		}
	} else if modType == "mod" {
		// 对于主模块，使用当前目录大小
		sz = calculateDirSize(".")
	}

	return &ModuleEntry{
		Path:      path,
		Name:      path,
		Version:   version,
		Size:      sz,
		SizeHuman: humanize.Bytes(sz),
	}
}

// estimateModuleSize 估算模块大小
func estimateModuleSize(modulePath, version string) uint64 {
	if version == "" || version == "(devel)" {
		// 对于开发版本，尝试查找本地路径
		return 0 // 无法估算本地开发模块的大小
	}

	// 尝试从 Go 模块缓存获取大小
	goPath := os.Getenv("GOPATH")
	if goPath == "" {
		goPath = filepath.Join(os.Getenv("HOME"), "go")
	}

	cachePath := filepath.Join(goPath, "pkg", "mod", modulePath+"@"+version)

	// 检查模块缓存路径是否存在
	if stat, err := os.Stat(cachePath); err == nil && stat.IsDir() {
		return calculateDirSize(cachePath)
	}

	return 0
}

// calculateDirSize 计算目录大小
func calculateDirSize(dir string) uint64 {
	var size uint64
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// 如果某个文件无法访问，记录警告但继续处理其他文件
			log.Printf("Warning: could not access %s: %v", path, err)
			return nil // 继续遍历其他文件
		}
		if !info.IsDir() {
			size += uint64(info.Size())
		}
		return nil
	})
	if err != nil {
		// 如果整个遍历过程失败，记录错误并返回0
		log.Printf("Error walking directory %s: %v", dir, err)
	}
	return size
}