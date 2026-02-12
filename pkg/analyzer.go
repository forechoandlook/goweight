package pkg

import (
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/thoas/go-funk"
)

var moduleRegex = regexp.MustCompile("packagefile (.*)=(.*)")

type ModuleEntry struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Size      uint64 `json:"size"`
	SizeHuman string `json:"size_human"`
}

type GoWeight struct {
	BuildCmd []string
}

func NewGoWeight() *GoWeight {
	return &GoWeight{
		BuildCmd: []string{"go", "build", "-work", "-a"},
	}
}

func (g *GoWeight) BuildCurrent() string {
	d := strings.Split(strings.TrimSpace(run(g.BuildCmd)), "\n")[0]
	return strings.Split(strings.TrimSpace(d), "=")[1]
}

// AnalyzeBuildProcess 分析构建过程，显示编译时各包的大小
func (g *GoWeight) AnalyzeBuildProcess(packages ...string) []*ModuleEntry {
	// 使用 -work -a -x 标志来显示详细的构建过程
	buildCmd := []string{"go", "build", "-work", "-a", "-x"}
	
	// 添加其他可能的参数
	originalCmd := g.BuildCmd
	for i, arg := range originalCmd {
		if arg == "-o" && i+1 < len(originalCmd) {
			// 跳过 -o 参数，因为我们不需要实际输出文件
			i++
		} else if arg != "go" && arg != "build" {
			if arg != "-work" && arg != "-a" && arg != "-x" { // 避免重复添加标志
				buildCmd = append(buildCmd, arg)
			}
		}
	}

	// 添加一个临时输出文件名
	buildCmd = append(buildCmd, "-o", "temp_output_for_analysis")
	
	// 添加指定的包参数
	if len(packages) > 0 {
		buildCmd = append(buildCmd, packages...)
	} else {
		// 如果没有指定包，默认使用当前目录
		buildCmd = append(buildCmd, ".")
	}
	
	out, err := exec.Command(buildCmd[0], buildCmd[1:]...).CombinedOutput()
	if err != nil {
		log.Printf("Warning: Error during build analysis: %v\nOutput: %s", err, out)
		// 即使构建失败，我们也尝试分析输出
	}
	
	// 解析构建输出来获取包大小信息
	modules := parseBuildOutput(string(out))
	
	// 清理临时文件
	os.Remove("temp_output_for_analysis")
	
	return modules
}

// parseBuildOutput 解析构建输出来获取包信息
func parseBuildOutput(output string) []*ModuleEntry {
	var modules []*ModuleEntry
	
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		// 查找编译命令，如 "/path/to/compile -o $WORK/b001/_pkg_.a -trimpath [...]" 或 "compile -o $WORK/b001/_pkg_.a [...]"
		if strings.Contains(line, "/compile") && strings.Contains(line, "-o") && strings.Contains(line, "_pkg_.a") {
			// 提取输出文件路径
			parts := strings.Fields(line)
			var outputFile string
			for i, part := range parts {
				if part == "-o" && i+1 < len(parts) {
					outputFile = parts[i+1]
					break
				}
			}
			
			if outputFile != "" {
				// 获取文件大小
				// 由于 WORK 目录是临时的，我们无法直接访问文件
				// 因此，我们只记录包名，大小暂时设置为0
				packageName := extractPackageNameFromWorkDir(outputFile)
				
				// 检查是否已经存在相同的包
				exists := false
				for _, m := range modules {
					if m.Name == packageName {
						exists = true
						break
					}
				}
				
				if !exists {
					module := &ModuleEntry{
						Path:      outputFile,
						Name:      packageName,
						Size:      0, // 无法获取临时文件大小
						SizeHuman: "0 B", // 无法获取临时文件大小
					}
					modules = append(modules, module)
				}
			}
		}
		
		// 查找包文件链接命令，如 "pack r $WORK/b001/_pkg_.a [...]"
		if strings.HasPrefix(line, "pack r ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				packFile := parts[2]
				if strings.HasSuffix(packFile, "_pkg_.a") {
					// 无法获取临时文件大小
					packageName := extractPackageNameFromWorkDir(packFile)
					
					// 检查是否已经存在相同的包
					exists := false
					for _, m := range modules {
						if m.Name == packageName {
							exists = true
							break
						}
					}
					
					if !exists {
						module := &ModuleEntry{
							Path:      packFile,
							Name:      packageName,
							Size:      0, // 无法获取临时文件大小
							SizeHuman: "0 B", // 无法获取临时文件大小
						}
						modules = append(modules, module)
					}
				}
			}
		}
	}
	
	// 按大小排序
	sort.Slice(modules, func(i, j int) bool {
		return modules[i].Size > modules[j].Size
	})
	
	return modules
}

// extractPackageNameFromWorkDir 从工作目录路径中提取包名
func extractPackageNameFromWorkDir(path string) string {
	// Go 工作目录通常包含 b001, b002 等子目录
	// 我们需要查找 importcfg 文件来确定包名
	dir := filepath.Dir(path)
	
	// 查找同级目录下的 importcfg 文件
	importCfgPath := filepath.Join(filepath.Dir(dir), "importcfg")
	if content, err := os.ReadFile(importCfgPath); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "packagefile ") {
				parts := strings.Split(line, "=")
				if len(parts) == 2 {
					packagePath := strings.TrimSpace(parts[0])
					packagePath = strings.TrimPrefix(packagePath, "packagefile ")
					filePath := strings.TrimSpace(parts[1])
					
					// 检查是否是我们正在查找的文件
					if filePath == path {
						return packagePath
					}
				}
			}
		}
	}
	
	// 如果找不到确切的包名，返回基本名称
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func (g *GoWeight) Process(work string) []*ModuleEntry {

	var files []string
	err := filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "importcfg" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("Error walking directory: %v", err)
	}

	allLines := funk.Uniq(funk.FlattenDeep(funk.Map(files, func(file string) []string {
		f, err := os.ReadFile(file)
		if err != nil {
			return []string{}
		}
		return strings.Split(string(f), "\n")
	})))
	modules := funk.Compact(funk.Map(allLines, processModule)).([]*ModuleEntry)

	// 添加项目自身包的大小信息
	projectModules := g.calculateProjectModuleSizes(work)

	// 合并项目包和依赖包
	allModules := append(modules, projectModules...)

	sort.Slice(allModules, func(i, j int) bool { return allModules[i].Size > allModules[j].Size })

	return allModules
}

func processModule(line string) *ModuleEntry {
	captures := moduleRegex.FindAllStringSubmatch(line, -1)
	if captures == nil || len(captures[0]) < 3 {
		return nil
	}
	path := captures[0][2]

	// 检查路径是否存在
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// 如果文件不存在，尝试查找最接近的匹配文件
		dir := filepath.Dir(path)
		base := filepath.Base(path)

		// 遍历目录寻找相似名称的文件
		if dirInfo, err := os.ReadDir(dir); err == nil {
			for _, entry := range dirInfo {
				if strings.Contains(entry.Name(), strings.TrimSuffix(base, filepath.Ext(base))) {
					candidatePath := filepath.Join(dir, entry.Name())
					if stat, err := os.Stat(candidatePath); err == nil && !stat.IsDir() {
						path = candidatePath
						break
					}
				}
			}
		}
	}

	stat, err := os.Stat(path)
	if err != nil {
		// 如果仍然无法找到文件，返回nil
		return nil
	}
	sz := uint64(stat.Size())

	return &ModuleEntry{
		Path:      path,
		Name:      captures[0][1],
		Size:      sz,
		SizeHuman: humanize.Bytes(sz),
	}
}

// calculateProjectModuleSizes 计算项目自身模块的大小
func (g *GoWeight) calculateProjectModuleSizes(work string) []*ModuleEntry {
	var modules []*ModuleEntry

	// 查找项目根目录下的包
	// 遍历工作目录，查找编译后的包文件
	err := filepath.WalkDir(work, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// 查找 .a 文件（Go 归档文件，包含编译后的包）
		if !d.IsDir() && strings.HasSuffix(path, ".a") {
			// 检查是否是项目自身的包而不是依赖
			if isProjectPackage(path) {
				stat, err := os.Stat(path)
				if err != nil {
					return nil // 忽略错误，继续处理其他文件
				}

				// 从文件路径推断包名
				pkgName := extractPackageNameFromArchivePath(path, work)

				module := &ModuleEntry{
					Path:      path,
					Name:      pkgName,
					Size:      uint64(stat.Size()),
					SizeHuman: humanize.Bytes(uint64(stat.Size())),
				}

				modules = append(modules, module)
			}
		}

		return nil
	})

	if err != nil {
		log.Printf("Warning: Error walking work directory for project modules: %v", err)
	}

	return modules
}

// isProjectPackage 判断是否是项目自身的包而不是依赖
func isProjectPackage(archivePath string) bool {
	// 如果归档文件路径包含 "pkg/mod"，则是依赖包
	return !strings.Contains(archivePath, "pkg/mod")
}

// extractPackageNameFromArchivePath 从归档文件路径中提取包名
func extractPackageNameFromArchivePath(archivePath, workDir string) string {
	// 从工作目录路径中提取包名
	relativePath, err := filepath.Rel(workDir, archivePath)
	if err != nil {
		// 如果无法获取相对路径，尝试从文件路径中提取有意义的信息
		// Go 构建时会在临时目录中创建形如 b001/_pkg_.a 的文件
		dir := filepath.Dir(archivePath)
		baseDir := filepath.Base(dir)
		filename := strings.TrimSuffix(filepath.Base(archivePath), ".a")

		// 如果目录名是 bXXX 格式且文件名是 _pkg_，尝试从 importcfg 文件中获取包名
		if strings.HasPrefix(baseDir, "b") && strings.HasPrefix(baseDir[1:], "0") && filename == "_pkg_" {
			// 尝试查找对应的 importcfg 文件以获取实际包名
			actualPackageName := findActualPackageNameFromImportCfg(workDir, archivePath)
			if actualPackageName != "" {
				return actualPackageName
			}
		}

		return baseDir + "/" + filename
	}

	// 移除路径中的 .a 扩展名
	pkgPath := strings.TrimSuffix(relativePath, ".a")

	// 如果路径以特定模式结尾，可能是主包
	if strings.HasSuffix(pkgPath, "/main.a") || strings.HasSuffix(pkgPath, "/_main.a") {
		return "main"
	}

	// Go 构建时会在临时目录中创建形如 b001/_pkg_.a 的文件
	// 尝试从 importcfg 文件中获取实际包名
	if strings.Contains(pkgPath, "/_pkg_") {
		actualPackageName := findActualPackageNameFromImportCfg(workDir, archivePath)
		if actualPackageName != "" {
			return actualPackageName
		}
	}

	return strings.ReplaceAll(pkgPath, string(filepath.Separator), "/")
}

// findActualPackageNameFromImportCfg 从 importcfg 文件中查找实际的包名
func findActualPackageNameFromImportCfg(workDir, archivePath string) string {
	// 查找工作目录中的 importcfg 文件
	importCfgFiles, err := filepath.Glob(filepath.Join(workDir, "**/importcfg"))
	if err != nil {
		return ""
	}

	// 从归档文件名中提取构建 ID（如 b001）
	archiveDir := filepath.Dir(archivePath)
	buildID := filepath.Base(archiveDir)

	// 在 importcfg 文件中查找引用此构建 ID 的行
	for _, cfgFile := range importCfgFiles {
		content, err := os.ReadFile(cfgFile)
		if err != nil {
			continue
		}

		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			// importcfg 文件格式通常是 "packagefile packagename=/path/to/file.a"
			if strings.Contains(line, buildID) && strings.Contains(line, "_pkg_.a") {
				parts := strings.Split(line, "=")
				if len(parts) == 2 && strings.HasPrefix(parts[0], "packagefile ") {
					packageName := strings.TrimSpace(strings.TrimPrefix(parts[0], "packagefile "))
					// 如果包名不是 "command-line-arguments" 或 "_pqc_", 返回真实包名
					if packageName != "command-line-arguments" && !strings.HasPrefix(packageName, "_p") {
						return packageName
					}
				}
			}
		}
	}

	return ""
}

func run(cmd []string) string {
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		log.Fatalf("Error running command %v: %v\nOutput: %s", cmd, err, out)
	}
	return string(out)
}