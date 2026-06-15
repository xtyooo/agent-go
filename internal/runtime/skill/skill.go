package skill

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const skillFileName = "SKILL.md"

// Metadata 是一个技能的轻量描述。
// 它只进入系统提示词；完整 SKILL.md 内容必须由 read_skill 工具按需读取。
type Metadata struct {
	// Name 是技能名称，默认取技能目录名。
	Name string `json:"name"`
	// Description 是技能摘要，优先来自 YAML front matter 的 description。
	Description string `json:"description"`
	// Directory 是技能目录绝对路径。
	Directory string `json:"directory"`
	// SkillFile 是 SKILL.md 文件绝对路径。
	SkillFile string `json:"skillFile"`
	// AllowedTools 是技能声明允许使用的工具列表；当前只做元数据保留。
	AllowedTools []string `json:"allowedTools,omitempty"`
}

// Config 是本地技能扫描配置。
type Config struct {
	// Directories 是技能根目录列表。每个根目录下的直接子目录都是候选技能目录。
	Directories []string
	// AutoReload=true 时，每次 List/Read 前都会重新扫描目录，便于开发调试。
	AutoReload bool
}

// Manager 管理本地 Skills。
// Java dodo-agent 中对应 SkillManager + FileSystemSkillRegistry。
type Manager struct {
	directories []string
	autoReload  bool
	logger      *slog.Logger

	mu     sync.RWMutex
	loaded bool
	skills map[string]Metadata
}

func NewManager(cfg Config, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	dirs := make([]string, 0, len(cfg.Directories))
	for _, dir := range cfg.Directories {
		dir = strings.TrimSpace(dir)
		if dir != "" {
			dirs = append(dirs, filepath.Clean(dir))
		}
	}
	return &Manager{
		directories: dirs,
		autoReload:  cfg.AutoReload,
		logger:      logger,
		skills:      make(map[string]Metadata),
	}
}

// Enabled 表示是否配置了至少一个技能根目录。
func (m *Manager) Enabled() bool {
	return m != nil && len(m.directories) > 0
}

// List 返回已发现的技能元数据，按名称稳定排序。
func (m *Manager) List(ctx context.Context) ([]Metadata, error) {
	if m == nil {
		return nil, nil
	}
	if err := m.ensureLoaded(ctx); err != nil {
		return nil, err
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Metadata, 0, len(m.skills))
	for _, metadata := range m.skills {
		out = append(out, metadata)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Read 读取指定技能完整内容。
func (m *Manager) Read(ctx context.Context, name string) (Metadata, string, error) {
	if m == nil {
		return Metadata{}, "", errors.New("skill manager is nil")
	}
	if err := m.ensureLoaded(ctx); err != nil {
		return Metadata{}, "", err
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return Metadata{}, "", errors.New("skill name is required")
	}

	m.mu.RLock()
	metadata, ok := m.skills[name]
	m.mu.RUnlock()
	if !ok {
		return Metadata{}, "", fmt.Errorf("skill %q not found", name)
	}

	payload, err := os.ReadFile(metadata.SkillFile)
	if err != nil {
		return Metadata{}, "", fmt.Errorf("read skill %q: %w", name, err)
	}
	return metadata, string(payload), nil
}

// Prompt 把技能列表格式化成系统提示词片段。
// 语义对齐 Java SkillPromptFormatter：技能不是工具，模型必须先调用 read_skill 加载完整指令。
func (m *Manager) Prompt(ctx context.Context) string {
	skills, err := m.List(ctx)
	if err != nil {
		m.logger.Warn("⚠️ Skills Runtime 技能列表加载失败", "error", err)
		return ""
	}
	return FormatPrompt(skills)
}

// Reload 主动清理缓存并重新扫描技能目录。
func (m *Manager) Reload(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	m.loaded = false
	m.skills = make(map[string]Metadata)
	m.mu.Unlock()
	return m.ensureLoaded(ctx)
}

func (m *Manager) ensureLoaded(ctx context.Context) error {
	if m.autoReload {
		return m.reload(ctx)
	}

	m.mu.RLock()
	loaded := m.loaded
	m.mu.RUnlock()
	if loaded {
		return nil
	}
	return m.reload(ctx)
}

func (m *Manager) reload(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	discovered := make(map[string]Metadata)
	for _, root := range m.directories {
		if err := scanRoot(ctx, root, discovered, m.logger); err != nil {
			return err
		}
	}

	m.mu.Lock()
	m.skills = discovered
	m.loaded = true
	m.mu.Unlock()

	m.logger.Info("🧩 Skills Runtime 扫描完成",
		"directory_count", len(m.directories),
		"skill_count", len(discovered),
		"auto_reload", m.autoReload,
	)
	return nil
}

func scanRoot(ctx context.Context, root string, discovered map[string]Metadata, logger *slog.Logger) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			logger.Warn("⚠️ Skills 目录不存在，已跳过", "directory", root)
			return nil
		}
		return fmt.Errorf("stat skills directory %q: %w", root, err)
	}
	if !info.IsDir() {
		logger.Warn("⚠️ Skills 路径不是目录，已跳过", "directory", root)
		return nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("read skills directory %q: %w", root, err)
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.IsDir() {
			continue
		}
		skillDir := filepath.Join(root, entry.Name())
		skillFile := filepath.Join(skillDir, skillFileName)
		payload, err := os.ReadFile(skillFile)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			logger.Warn("⚠️ 读取 SKILL.md 失败，已跳过", "skill_file", skillFile, "error", err)
			continue
		}
		metadata := parseMetadata(entry.Name(), skillDir, skillFile, string(payload))
		discovered[metadata.Name] = metadata
	}
	return nil
}

type frontMatter struct {
	Description  string   `yaml:"description"`
	AllowedTools []string `yaml:"allowedTools"`
}

func parseMetadata(name, skillDir, skillFile, content string) Metadata {
	metadata := Metadata{
		Name:        name,
		Description: "Skill: " + name,
		Directory:   skillDir,
		SkillFile:   skillFile,
	}
	if fm, ok := parseFrontMatter(content); ok {
		if strings.TrimSpace(fm.Description) != "" {
			metadata.Description = strings.TrimSpace(fm.Description)
		}
		metadata.AllowedTools = cleanStrings(fm.AllowedTools)
		return metadata
	}
	if desc := inferDescription(content); desc != "" {
		metadata.Description = desc
	}
	return metadata
}

func parseFrontMatter(content string) (frontMatter, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return frontMatter{}, false
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return frontMatter{}, false
	}
	raw := normalized[4 : 4+end]
	var fm frontMatter
	if err := yaml.Unmarshal([]byte(raw), &fm); err != nil {
		return frontMatter{}, false
	}
	return fm, true
}

func inferDescription(content string) string {
	body := stripFrontMatter(content)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func stripFrontMatter(content string) string {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(normalized, "---\n") {
		return content
	}
	end := strings.Index(normalized[4:], "\n---")
	if end < 0 {
		return content
	}
	rest := normalized[4+end+4:]
	return strings.TrimSpace(rest)
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
