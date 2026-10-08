package rootrepair

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Bound only this package's diagnostic files, retaining recent before/final
// evidence together. Normal healthy starts never enter the logging path.
func trimReports(dir string) {
	entries, e := os.ReadDir(dir)
	if e != nil {
		return
	}
	type entry struct {
		name string
		at   time.Time
	}
	var files []entry
	for _, v := range entries {
		if v.IsDir() || !strings.HasSuffix(v.Name(), ".json") || !(strings.HasPrefix(v.Name(), "root-") || strings.HasPrefix(v.Name(), "before-")) {
			continue
		}
		if info, e := v.Info(); e == nil && info.Mode().IsRegular() {
			files = append(files, entry{v.Name(), info.ModTime()})
		}
	}
	sort.Slice(files, func(i, j int) bool { return files[i].at.After(files[j].at) })
	for i := 40; i < len(files); i++ {
		_ = os.Remove(filepath.Join(dir, files[i].name))
	}
}
