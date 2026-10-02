package main

// 模块上下文定位：从任意目录向上找 go.mod，得到仓库根与 module 路径。
// 生成代码里的 import 路径 = <module>/internal/<域>/resources/<资源>/...，
// 所以 resgen 从不硬编码模块路径，派生项目（gonew 重写过 module）同样可用。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// moduleContext 从 dir 向上查找 go.mod，返回：
// rel——dir 相对仓库根的 POSIX 风格相对路径；root——仓库根；module——module 路径。
func moduleContext(dir string) (rel, root, module string, err error) {
	d := dir
	for {
		if _, e := os.Stat(filepath.Join(d, "go.mod")); e == nil {
			data, re := os.ReadFile(filepath.Join(d, "go.mod"))
			if re != nil {
				return "", "", "", re
			}
			m := parseModuleLine(string(data))
			if m == "" {
				return "", "", "", fmt.Errorf("%s 中未找到 module 声明", filepath.Join(d, "go.mod"))
			}
			r := strings.TrimPrefix(strings.TrimPrefix(dir, d), string(filepath.Separator))
			return filepath.ToSlash(r), d, m, nil
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", "", fmt.Errorf("从 %s 向上未找到 go.mod（请在模块内运行）", dir)
		}
		d = parent
	}
}

func parseModuleLine(data string) string {
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
	}
	return ""
}
