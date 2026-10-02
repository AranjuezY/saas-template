package main

// .go-arch-lint.yml 标记区管理。
//
// 资源的 port/store/... 子包都落在已有通配路径（internal/*/resources/*/port 等）内，
// 无需登记；但资源根目录的 doc.go 不匹配任何组件，必须登记一个根组件，
// 否则 go-arch-lint 报 "not attached to any component"。
//
// 登记只发生在标记区之间——生成器永不触碰人手写的规则：
//
//	# resgen:begin:components（本区由 resgen 维护，勿手改）
//	  resgen-example-item: { in: internal/example/resources/item }
//	# resgen:end:components
//
// 条目按名字排序，保证同一组资源产出字节级相同的配置。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	archBegin = "# resgen:begin:components"
	archEnd   = "# resgen:end:components"
	// 尾注单独一行，保证标记行本身是裸标记——匹配用前缀，兼容历史格式。
	archNote   = "# （本区由 resgen 维护，排序稳定，勿手改）"
	archAnchor = "commonComponents:"
)

// isMarker 判断某行是否为指定标记（行首可能有缩进、行尾可能带尾注）。
func isMarker(line, marker string) bool {
	t := strings.TrimSpace(line)
	return t == marker || strings.HasPrefix(t, marker)
}

// rootComponentName 生成资源根组件名。
func rootComponentName(domain, name string) string {
	return fmt.Sprintf("resgen-root-%s-%s", domain, name)
}

// rootComponentLine 生成资源根组件条目（与手写条目同一风格）。
func rootComponentLine(domain, name string) string {
	return fmt.Sprintf("  %s: { in: internal/%s/resources/%s }", rootComponentName(domain, name), domain, name)
}

// refreshArchLint 保证标记区中存在该资源的根组件条目（幂等、排序稳定）。
func refreshArchLint(root, domain, name string) error {
	path := filepath.Join(root, ".go-arch-lint.yml")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读取 %s 失败：%w（resgen 依赖它登记资源根组件）", path, err)
	}
	text := string(data)

	mine := rootComponentLine(domain, name)
	mineKey := rootComponentName(domain, name)

	// 收集既有条目（保留其它资源的登记），替换/追加本资源的条目。
	entries := []string{}
	lines := strings.Split(text, "\n")
	inRegion := false
	for _, line := range lines {
		if isMarker(line, archBegin) {
			inRegion = true
			continue
		}
		if isMarker(line, archEnd) {
			inRegion = false
			continue
		}
		if !inRegion {
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if keyOfEntry(t) == mineKey {
			continue // 旧值被新值替换
		}
		entries = append(entries, t)
	}
	entries = append(entries, strings.TrimSpace(mine))
	sort.Strings(entries)

	indented := make([]string, 0, len(entries)+3)
	indented = append(indented, "  "+archBegin, "  "+archNote)
	for _, e := range entries {
		indented = append(indented, "  "+e)
	}
	indented = append(indented, "  "+archEnd)
	region := strings.Join(indented, "\n")

	var updated string
	if strings.Contains(text, archBegin) {
		updated = spliceRegion(text, region)
	} else {
		// 首次插入：锚在 commonComponents: 之前。
		anchorIdx := strings.Index(text, archAnchor)
		if anchorIdx < 0 {
			return fmt.Errorf("%s 中未找到锚点 %s，无法插入标记区（请手工添加）", path, archAnchor)
		}
		updated = text[:anchorIdx] + region + "\n\n" + text[anchorIdx:]
	}

	if updated != text {
		if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
			return err
		}
		fmt.Printf("  更新 %s（标记区）\n", mustRel(root, path))
	}
	return nil
}

// spliceRegion 用新区域整体替换旧的 begin..end 区间。
func spliceRegion(text, region string) string {
	lines := strings.Split(text, "\n")
	begin, end := -1, -1
	for i, line := range lines {
		if isMarker(line, archBegin) && begin < 0 {
			begin = i
		}
		if isMarker(line, archEnd) && begin >= 0 && end < 0 {
			end = i
		}
	}
	if begin < 0 || end < begin {
		return text
	}
	out := append([]string{}, lines[:begin]...)
	out = append(out, strings.Split(region, "\n")...)
	out = append(out, lines[end+1:]...)
	return strings.Join(out, "\n")
}

// keyOfEntry 从 "  name: { in: ... }" 中取出组件名。
func keyOfEntry(entry string) string {
	if i := strings.Index(entry, ":"); i >= 0 {
		return strings.TrimSpace(entry[:i])
	}
	return entry
}
