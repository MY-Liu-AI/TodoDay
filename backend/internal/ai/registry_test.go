package ai

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTestSkill 在 dir 下造一个最小合法技能目录。
func writeTestSkill(t *testing.T, dir, name string) {
	t.Helper()
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","version":"0.1.0","description":"测试技能","kind":"llm","entries":["web"],"permission":"read","context":"today_tasks","output":"quadrant_result"}`
	if err := os.WriteFile(filepath.Join(d, "skill.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "prompt.md"), []byte("你是测试技能。"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryLoadAndGet(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "alpha")
	writeTestSkill(t, dir, "beta")

	reg, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(reg.List()); got != 2 {
		t.Fatalf("应加载 2 个技能，得到 %d", got)
	}
	sk, ok := reg.Get("alpha")
	if !ok || sk.Prompt == "" || !hasEntry(sk.Entries, "web") {
		t.Fatalf("alpha 加载不完整：%+v", sk)
	}
	if _, ok := reg.Get("missing"); ok {
		t.Fatal("不存在的技能不应命中")
	}
	// List 不下发 prompt。
	for _, s := range reg.List() {
		if s.Prompt != "" {
			t.Fatal("List 不应包含 prompt")
		}
	}
}

func TestRegistryMissingDirIsEmpty(t *testing.T) {
	reg, err := LoadRegistry(filepath.Join(t.TempDir(), "不存在"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.List()) != 0 {
		t.Fatal("目录不存在应为空注册表")
	}
}

func TestRegistryRejectsBrokenSkill(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "good")
	bad := filepath.Join(dir, "bad")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	// permission 非法。
	if err := os.WriteFile(filepath.Join(bad, "skill.json"),
		[]byte(`{"name":"bad","kind":"llm","entries":["web"],"permission":"rw","context":"c","output":"o"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bad, "prompt.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadRegistry(dir); err == nil {
		t.Fatal("损坏的技能定义应让整体加载报错，而不是半加载")
	}
}

func TestRegistryReload(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "one")
	reg, err := LoadRegistry(dir)
	if err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, dir, "two")
	if err := reg.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Get("two"); !ok {
		t.Fatal("Reload 后应看到新技能")
	}
}
