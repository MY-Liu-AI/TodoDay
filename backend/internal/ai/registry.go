package ai

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Skill 是一个 AI 功能的声明式定义，全部内容来自 skills 目录下的文件
// （<dir>/<name>/skill.json + prompt.md），不在 Go 代码里写死。
// 新增/修改技能 = 改文件 + reload，不改代码、不重新编译。
type Skill struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Kind        string   `json:"kind"`                  // "llm"：prompt 驱动的模型调用
	Entries     []string `json:"entries"`               // 允许入口："web" / "chat"
	Permission  string   `json:"permission"`            // "read"（只读分析）/ "write"（有副作用，调用前需确认环）
	Context     string   `json:"context"`               // 上下文供给键，对应 Invoker.providers 的注册键
	Output      string   `json:"output"`                // 产出校验键，对应 Invoker.validators 的注册键
	Prompt      string   `json:"-"`                     // 从 prompt.md 加载的提示词模板
}

// manifest 是 skill.json 的磁盘格式，与 Skill 同构。
type manifest = Skill

// Registry 从目录加载技能，运行时可用 Reload 热更新（owner 触发）。
// 读多写少：Invoke/List 走读锁，Reload 整表替换。
type Registry struct {
	dir    string
	mu     sync.RWMutex
	skills map[string]Skill
}

// LoadRegistry 扫描 dir 下一级子目录，读取每个含 skill.json 的目录。
// 目录不存在返回空注册表 + nil error（技能是可选能力，不该阻止启动）；
// 单个技能文件损坏则整体报错——半加载的注册表会让路由行为不可预期。
func LoadRegistry(dir string) (*Registry, error) {
	r := &Registry{dir: dir, skills: map[string]Skill{}}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// Dir 返回技能目录（管理端展示用）。
func (r *Registry) Dir() string { return r.dir }

// Reload 重新扫描目录并整表替换。
func (r *Registry) Reload() error {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		if os.IsNotExist(err) {
			r.mu.Lock()
			r.skills = map[string]Skill{}
			r.mu.Unlock()
			return nil
		}
		return fmt.Errorf("读取技能目录 %s 失败：%w", r.dir, err)
	}
	next := map[string]Skill{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sk, err := loadSkill(filepath.Join(r.dir, e.Name()))
		if err != nil {
			return err
		}
		if _, dup := next[sk.Name]; dup {
			return fmt.Errorf("技能名重复：%s", sk.Name)
		}
		next[sk.Name] = sk
	}
	r.mu.Lock()
	r.skills = next
	r.mu.Unlock()
	return nil
}

func loadSkill(dir string) (Skill, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "skill.json"))
	if err != nil {
		return Skill{}, fmt.Errorf("读取 %s/skill.json 失败：%w", dir, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Skill{}, fmt.Errorf("解析 %s/skill.json 失败：%w", dir, err)
	}
	if m.Name == "" || m.Kind == "" || m.Context == "" || m.Output == "" {
		return Skill{}, fmt.Errorf("%s/skill.json 缺少必填字段（name/kind/context/output）", dir)
	}
	if len(m.Entries) == 0 {
		return Skill{}, fmt.Errorf("%s/skill.json 未声明任何入口（entries）", dir)
	}
	if m.Permission != "read" && m.Permission != "write" {
		return Skill{}, fmt.Errorf("%s/skill.json permission 必须是 read 或 write", dir)
	}
	prompt, err := os.ReadFile(filepath.Join(dir, "prompt.md"))
	if err != nil {
		return Skill{}, fmt.Errorf("读取 %s/prompt.md 失败：%w", dir, err)
	}
	if strings.TrimSpace(string(prompt)) == "" {
		return Skill{}, fmt.Errorf("%s/prompt.md 为空", dir)
	}
	m.Prompt = string(prompt)
	return m, nil
}

// Get 按名取技能。适配层一律经此查找，不许自带技能清单。
func (r *Registry) Get(name string) (Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sk, ok := r.skills[name]
	return sk, ok
}

// List 返回全部技能（按名排序），供管理端展示。
func (r *Registry) List() []Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Skill, 0, len(r.skills))
	for _, sk := range r.skills {
		sk.Prompt = "" // 提示词不下发
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
