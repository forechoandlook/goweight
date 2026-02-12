package pkg

import (
	"debug/dwarf"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"os"
	"strings"
)

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