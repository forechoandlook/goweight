package main

import (
	"encoding/json"
	"fmt"

	"github.com/jondot/goweight/pkg"

	"sort"
	"strings"

	kingpin "github.com/alecthomas/kingpin/v2"
	
	"github.com/dustin/go-humanize"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var (
	jsonOutput = kingpin.Flag("json", "Output json").Short('j').Bool()
	buildTags  = kingpin.Flag("tags", "Build tags").String()
	packages   = kingpin.Arg("packages", "Packages to build").String()
	binaryFile = kingpin.Flag("binary", "Analyze a binary file instead of building").Short('b').String()
	verbose    = kingpin.Flag("verbose", "Detailed output showing all packages").Short('v').Bool()
	buildAnalysis = kingpin.Flag("build-analysis", "Analyze build process to show compilation sizes").Bool()
)

func main() {
	kingpin.Version(fmt.Sprintf("%s (%s)", version, commit))
	kingpin.Parse()
	weight := pkg.NewGoWeight()

	var modules []*pkg.ModuleEntry

	if *binaryFile != "" {
		modules = weight.ProcessBinary(*binaryFile)
	} else if *buildAnalysis {
		// 使用构建过程分析模式
		var pkgArgs []string
		if *packages != "" {
			pkgArgs = append(pkgArgs, *packages)
		}
		modules = weight.AnalyzeBuildProcess(pkgArgs...)
	} else {
		if *buildTags != "" {
			weight.BuildCmd = append(weight.BuildCmd, "-tags", *buildTags)
		}
		if *packages != "" {
			weight.BuildCmd = append(weight.BuildCmd, *packages)
		}

		// 使用新的方法分析最终的二进制文件，而不是中间构建产物
		modules = weight.BuildAndAnalyzeBinary()
	}

	if *jsonOutput {
		m, _ := json.Marshal(modules)
		fmt.Print(string(m))
	} else {
		if *verbose {
			// 详细输出 - 显示所有包
			for _, module := range modules {
				fmt.Printf("%8s %s\n", module.SizeHuman, module.Name)
			}
		} else {
			// 简略输出 - 合并相同顶级包
			aggregatedModules := aggregateByTopLevelPackage(modules)
			for _, module := range aggregatedModules {
				fmt.Printf("%8s %s\n", module.SizeHuman, module.Name)
			}
		}
	}
}

// aggregateByTopLevelPackage 将相同顶级包的模块合并
func aggregateByTopLevelPackage(modules []*pkg.ModuleEntry) []*pkg.ModuleEntry {
	// 创建映射来存储聚合结果
	aggregated := make(map[string]*pkg.ModuleEntry)
	
	for _, module := range modules {
		topLevel := getTopLevelPackage(module.Name)
		
		if existing, exists := aggregated[topLevel]; exists {
			// 如果已存在此顶级包，则累加大小
			existing.Size += module.Size
			existing.SizeHuman = humanize.Bytes(existing.Size)
		} else {
			// 否则创建新的聚合项
			aggregated[topLevel] = &pkg.ModuleEntry{
				Path:      topLevel,
				Name:      topLevel,
				Size:      module.Size,
				SizeHuman: humanize.Bytes(module.Size),
			}
		}
	}
	
	// 转换为切片并按大小排序
	var result []*pkg.ModuleEntry
	for _, module := range aggregated {
		result = append(result, module)
	}
	
	sort.Slice(result, func(i, j int) bool {
		return result[i].Size > result[j].Size
	})
	
	return result
}

// getTopLevelPackage 获取顶级包名
func getTopLevelPackage(fullPackageName string) string {
	// 处理标准库包
	if strings.HasPrefix(fullPackageName, "runtime") {
		return "runtime"
	}
	if strings.HasPrefix(fullPackageName, "internal/") {
		return "internal/*"
	}
	if strings.HasPrefix(fullPackageName, "vendor/") {
		return "vendor/*"
	}
	
	// 处理第三方包
	if strings.Contains(fullPackageName, ".") || strings.Contains(fullPackageName, "/") {
		parts := strings.Split(fullPackageName, "/")
		if len(parts) > 0 {
			// 如果是域名开头的包（如 github.com、golang.org 等）
			if strings.Contains(parts[0], ".") {
				// 对于域名，返回域名+用户名（如 github.com/user）
				if len(parts) >= 2 {
					return strings.Join(parts[:2], "/")
				}
				return parts[0]
			}
			
			// 对于非域名开头的标准格式包，返回前两部分
			if len(parts) >= 2 {
				return strings.Join(parts[:2], "/")
			}
			
			return parts[0]
		}
	}
	
	// 默认返回原名称
	return fullPackageName
}
