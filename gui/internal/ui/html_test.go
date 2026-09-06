package ui

import (
	"os"
	"strings"
	"testing"
)

func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func TestBuildIndex(t *testing.T) {
	tpl := `<html><head><style>` + cssPlaceholder + `</style></head><body><script>` + jsPlaceholder + `</script></body></html>`
	got, err := BuildIndex(tpl, "body{color:red}", "alert(1);")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "body{color:red}") || !strings.Contains(got, "alert(1);") {
		t.Fatalf("占位符未替换: %s", got)
	}
	if strings.Contains(got, cssPlaceholder) || strings.Contains(got, jsPlaceholder) {
		t.Fatal("占位符残留")
	}
}

func TestBuildIndexMissing(t *testing.T) {
	if _, err := BuildIndex("<html></html>", "a", "b"); err == nil {
		t.Fatal("缺占位符应报错")
	}
	if _, err := BuildIndex(cssPlaceholder, "a", "b"); err == nil {
		t.Fatal("缺 JS 占位符应报错")
	}
}

func TestBuildIndexRealAssets(t *testing.T) {
	// 用真实资源验证组装（路径相对模块根；JS = session_map.js + param_state.js +
	// drag_order.js + app.js 拼接，与 main_windows.go 一致）
	index, err := readFile("../../web/index.html")
	if err != nil {
		t.Skip("web 资源不可读（非模块根运行）: " + err.Error())
	}
	css, _ := readFile("../../web/style.css")
	smap, _ := readFile("../../web/session_map.js")
	param, _ := readFile("../../web/param_state.js")
	drag, _ := readFile("../../web/drag_order.js")
	js, _ := readFile("../../web/app.js")
	got, err := BuildIndex(index, css, smap+"\n"+param+"\n"+drag+"\n"+js)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<!DOCTYPE html>") || !strings.Contains(got, "})();") {
		t.Fatal("真实资源组装失败")
	}
	if !strings.Contains(got, "SessionMap") {
		t.Fatal("session_map.js 未组装进页面（会话映射缺失）")
	}
	if !strings.Contains(got, "SCEZParamState") {
		t.Fatal("param_state.js 未组装进页面（浮窗状态机缺失）")
	}
	if !strings.Contains(got, "DragOrder") {
		t.Fatal("drag_order.js 未组装进页面（标签拖拽排序缺失）")
	}
	if !strings.Contains(got, "logbox-head") {
		t.Fatal("app.js 新版原始输出结构未组装进页面")
	}
}
