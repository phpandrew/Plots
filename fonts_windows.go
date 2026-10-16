//go:build windows

package main

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"golang.org/x/sys/windows/registry"
)

type fontEntry struct{ Name, File string }

var (
	fontListOnce sync.Once
	fontList     []fontEntry
	fontByName   = map[string]string{}
)

// installedFonts lists TrueType/OpenType fonts from the registry (machine + user).
func installedFonts() []fontEntry {
	fontListOnce.Do(func() {
		win := os.Getenv("WINDIR")
		if win == "" {
			win = `C:\Windows`
		}
		seen := map[string]bool{}
		for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
			k, err := registry.OpenKey(root, `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Fonts`, registry.QUERY_VALUE)
			if err != nil {
				continue
			}
			names, _ := k.ReadValueNames(-1)
			for _, n := range names {
				file, _, err := k.GetStringValue(n)
				if err != nil {
					continue
				}
				low := strings.ToLower(file)
				if !strings.HasSuffix(low, ".ttf") && !strings.HasSuffix(low, ".otf") && !strings.HasSuffix(low, ".ttc") {
					continue
				}
				if !filepath.IsAbs(file) {
					file = filepath.Join(win, "Fonts", file)
				}
				name := n
				for _, suf := range []string{" (TrueType)", " (OpenType)"} {
					name = strings.TrimSuffix(name, suf)
				}
				if i := strings.Index(name, " & "); i > 0 {
					name = name[:i] // collections: use the first face
				}
				if seen[name] {
					continue
				}
				seen[name] = true
				fontList = append(fontList, fontEntry{name, file})
				fontByName[name] = file
			}
			k.Close()
		}
		sort.Slice(fontList, func(i, j int) bool { return strings.ToLower(fontList[i].Name) < strings.ToLower(fontList[j].Name) })
	})
	return fontList
}

func fontFile(name string) string {
	if name == "" {
		return ""
	}
	installedFonts()
	return fontByName[name]
}
