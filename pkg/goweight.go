package pkg

import (
	"debug/buildinfo"
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
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
var binaryModuleRegex = regexp.MustCompile(`^\s+(dep|mod)\s+([^\s]+)\s+([^\s]+)`)

func run(cmd []string) string {
	out, err := exec.Command(cmd[0], cmd[1:]...).CombinedOutput()
	if err != nil {
		log.Fatalf("Error running command %v: %v\nOutput: %s", cmd, err, out)
	}
	os.Remove("goweight-bin-target")
	return string(out)
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

func calculateDirSize(dir string) uint64 {
	var size uint64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// 如果某个文件无法访问，记录警告但继续处理其他文件
			log.Printf("Warning: could not access %s: %v", path, err)
			return nil // 继续遍历其他文件
		}
		if !d.IsDir() {
			fileInfo, err := d.Info()
			if err == nil {
				size += uint64(fileInfo.Size())
			} else {
				// 如果无法获取文件信息，记录警告
				log.Printf("Warning: could not get info for %s: %v", path, err)
			}
		}
		return nil
	})
	if err != nil {
		// 如果整个遍历过程失败，记录错误并返回0
		log.Printf("Error walking directory %s: %v", dir, err)
	}
	return size
}

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
		BuildCmd: []string{"go", "build", "-o", "goweight-bin-target", "-work", "-a"},
	}
}

func (g *GoWeight) BuildCurrent() string {
	d := strings.Split(strings.TrimSpace(run(g.BuildCmd)), "\n")[0]
	return strings.Split(strings.TrimSpace(d), "=")[1]
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

// analyzeBinarySymbolTable 分析二进制文件的符号表来估算各包的大小
func analyzeBinarySymbolTable(binaryPath string) (map[string]uint64, error) {
	f, err := os.Open(binaryPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// 尝试解析不同类型的二进制文件
	var sections []Section
	var symbols []Symbol
	var archType string

	// 尝试 ELF 格式 (Linux)
	if elfFile, err := elf.NewFile(f); err == nil {
		archType = "ELF"
		
		// 获取符号表
		if elfFile.Symbols != nil {
			if syms, err := elfFile.Symbols(); err == nil {
				for _, sym := range syms {
					if sym.Section >= 0 && int(sym.Section) < len(elfFile.Sections) {
						section := elfFile.Sections[sym.Section]
						name := sym.Name
						if pkg := extractPackageFromSymbol(name); pkg != "" {
							symbols = append(symbols, Symbol{
								Name:    name,
								Size:    sym.Size,
								Address: sym.Value,
								Package: pkg,
								Section: section.Name,
							})
						}
					}
				}
			}
		}
		
		// 获取节信息用于后续分析
		for _, sec := range elfFile.Sections {
			sections = append(sections, Section{
				Name: sec.Name,
				Size: sec.Size,
				Type: sec.Type.String(),
			})
		}
		
		// 如果没有符号信息，尝试从动态符号表获取
		if len(symbols) == 0 && elfFile.DynamicSymbols != nil {
			if dynSyms, err := elfFile.DynamicSymbols(); err == nil {
				for _, sym := range dynSyms {
					if sym.Section >= 0 && int(sym.Section) < len(elfFile.Sections) {
						section := elfFile.Sections[sym.Section]
						name := sym.Name
						if pkg := extractPackageFromSymbol(name); pkg != "" {
							symbols = append(symbols, Symbol{
								Name:    name,
								Size:    sym.Size,
								Address: sym.Value,
								Package: pkg,
								Section: section.Name,
							})
						}
					}
				}
			}
		}
	} else {
		// 重置文件指针
		f.Seek(0, 0)
		
		// 尝试 Mach-O 格式 (macOS)
		if machoFile, err := macho.NewFile(f); err == nil {
			archType = "MachO"
			
			// Mach-O 符号表处理
			if machoFile.Symtab != nil {
				for _, sym := range machoFile.Symtab.Syms {
					name := sym.Name
					
					if pkg := extractPackageFromSymbol(name); pkg != "" {
						// 尝试估算 Mach-O 符号的大小
						estimatedSize := estimateMachOSymbolSize(machoFile, sym)
						symbols = append(symbols, Symbol{
							Name:    name,
							Size:    estimatedSize,
							Address: 0, // Mach-O 符号可能没有地址信息
							Package: pkg,
							Section: "", // Mach-O 符号没有直接的节信息
						})
					}
				}
			}
			
			// 获取 Mach-O 段信息
			for _, seg := range machoFile.Sections {
				sections = append(sections, Section{
					Name: seg.Name,
					Size: uint64(seg.Size),
					Type: "section",
				})
			}
		} else {
			// 重置文件指针
			f.Seek(0, 0)
			
			// 尝试 PE 格式 (Windows)
			if peFile, err := pe.NewFile(f); err == nil {
				archType = "PE"
				
				// PE 文件符号处理
				if peFile.Symbols != nil {
					// PE 符号处理较为复杂，这里简化处理
					// 获取节信息
					for _, sec := range peFile.Sections {
						sections = append(sections, Section{
							Name: sec.Name,
							Size: uint64(sec.Size),
							Type: "section",
						})
					}
				}
			}
		}
	}

	if len(symbols) == 0 {
		return nil, fmt.Errorf("no symbols found in %s binary", archType)
	}

	// 按包聚合符号大小
	pkgSizes := make(map[string]uint64)
	for _, sym := range symbols {
		pkgSizes[sym.Package] += sym.Size
	}

	// 如果符号大小总和为0，尝试基于符号数量进行粗略估计
	totalSymbols := len(symbols)
	if totalSymbols > 0 {
		// 计算每包符号数量
		pkgSymbolCounts := make(map[string]int)
		for _, sym := range symbols {
			pkgSymbolCounts[sym.Package]++
		}
		
		// 如果所有符号大小都是0，根据符号数量分配预估大小
		if sumMapValues(pkgSizes) == 0 {
			// 基于二进制文件大小和符号分布估算
			fileStat, err := os.Stat(binaryPath)
			if err == nil {
				totalBinarySize := uint64(fileStat.Size())
				// 假设代码段占二进制文件的一部分，按符号数量比例分配
				codeRatio := 0.7 // 假设70%是代码
				avgSymbolSize := uint64(float64(totalBinarySize) * codeRatio / float64(totalSymbols))
				
				for pkg, count := range pkgSymbolCounts {
					pkgSizes[pkg] = uint64(count) * avgSymbolSize
				}
			}
		}
	}

	return pkgSizes, nil
}

// estimateMachOSymbolSize 尝试估算 Mach-O 符号的大小
func estimateMachOSymbolSize(file *macho.File, sym macho.Symbol) uint64 {
	// Mach-O 符号本身不包含大小信息，但我们可以通过地址差来估算
	// 这是一个简化的估算方法
	return 100 // 返回一个默认估算值，实际实现需要更复杂的算法
}

// sumMapValues 计算映射中所有值的总和
func sumMapValues(m map[string]uint64) uint64 {
	var sum uint64
	for _, v := range m {
		sum += v
	}
	return sum
}

// getMachOSectionName 获取 Mach-O 文件的节名称
func getMachOSectionName(file *macho.File, sectionIndex uint8) string {
	if int(sectionIndex) <= 0 || int(sectionIndex) > len(file.Sections) {
		return ""
	}
	return file.Sections[sectionIndex-1].Name
}

// Section 表示二进制文件中的一个节
type Section struct {
	Name string
	Size uint64
	Type string
}

// Symbol 表示一个符号及其相关信息
type Symbol struct {
	Name    string
	Size    uint64
	Address uint64
	Package string
	Section string
}

// extractPackageFromSymbol 从符号名中提取包名
func extractPackageFromSymbol(symbolName string) string {
	// Go 符号通常以包路径开头
	// 例如 runtime·xxx 或 main.xxx 或 github.com/user/repo/pkg.funcName
	
	// 查找第一个点或中间的包分隔符
	if idx := strings.Index(symbolName, "."); idx > 0 {
		prefix := symbolName[:idx]
		
		// 处理运行时符号
		if prefix == "runtime" || prefix == "main" || prefix == "go" {
			return prefix
		}
		
		// 检查是否是有效的包路径（包含域名或常见模式）
		if strings.Contains(prefix, "/") || strings.Contains(prefix, ".") {
			// 提取完整的包路径
			// 移除符号名部分，保留包路径
			parts := strings.Split(symbolName, ".")
			if len(parts) > 1 {
				// 重构包路径部分
				for i := len(parts) - 1; i >= 0; i-- {
					packageName := strings.Join(parts[:i], ".")
					// 检查是否看起来像一个包路径
					if strings.Contains(packageName, "/") || packageName == "main" || packageName == "runtime" {
						return packageName
					}
				}
			}
		}
	}
	
	return ""
}

// parseDWARF 从 DWARF 调试信息中提取符号
func parseDWARF(dwarfData *dwarf.Data) []Symbol {
	var symbols []Symbol
	r := dwarfData.Reader()
	
	for {
		entry, err := r.Next()
		if err != nil || entry == nil {
			break
		}
		
		if entry.Tag == dwarf.TagSubprogram { // 函数定义
			name, ok := entry.Val(dwarf.AttrName).(string)
			if ok {
				if pkg := extractPackageFromSymbol(name); pkg != "" {
					// 从 DWARF 信息中获取更多细节
					symbols = append(symbols, Symbol{Name: name, Size: 0, Package: pkg})
				}
			}
		}
	}
	
	return symbols
}
