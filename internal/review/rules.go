package review

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/yazawa-ichio/sedi-review/internal/gitx"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

// ConfigFile はリポジトリ直下に置く設定。追跡して共有するもの。
const ConfigFile = ".sedi-review.json"

const DefaultRule = "unity"

var builtinRules = map[string][]string{
	"unity": {":(exclude,glob)**/*.cs.meta"},
	"all":   {},
}

type Config struct {
	Base   string              `json:"base,omitempty"`
	TwoDot bool                `json:"twoDot,omitempty"`
	Rule   string              `json:"rule,omitempty"`
	Rules  map[string][]string `json:"rules,omitempty"`
}

// LoadConfig は設定を読み、組み込みのルールを足す。ファイルが無ければ既定の値になる。
func LoadConfig(repo string) (*Config, error) {
	c := &Config{}
	if _, err := store.ReadJSON(filepath.Join(repo, ConfigFile), c); err != nil {
		return nil, err
	}
	merged := map[string][]string{}
	for k, v := range builtinRules {
		merged[k] = v
	}
	for k, v := range c.Rules {
		merged[k] = v
	}
	c.Rules = merged
	if c.Rule == "" {
		c.Rule = DefaultRule
	}
	return c, nil
}

func (c *Config) Pathspecs(name string) ([]string, error) {
	if name == "" {
		name = c.Rule
	}
	specs, ok := c.Rules[name]
	if !ok {
		return nil, fmt.Errorf("ルール %q がありません（あるもの: %v）", name, c.RuleNames())
	}
	return append([]string(nil), specs...), nil
}

func (c *Config) RuleNames() []string {
	var names []string
	for k := range c.Rules {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

type InitOptions struct {
	Base   string
	Rule   string
	TwoDot bool
	Force  bool // 設定のファイルがあっても書き直す
}

// Init はリポジトリに保存のフォルダと設定を用意し、したことを返す。
func Init(repo string, o InitOptions) ([]string, error) {
	top, err := gitx.TopLevel(repo)
	if err != nil {
		return nil, err
	}
	var done []string
	root := store.Root(top)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	ignore := filepath.Join(root, ".gitignore")
	if _, err := os.Stat(ignore); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(ignore, []byte("*\n"), 0o644); err != nil {
			return nil, err
		}
		done = append(done, "作った: "+filepath.ToSlash(root)+"/（.gitignore で自分を git から外す）")
	} else {
		done = append(done, "既にある: "+filepath.ToSlash(root)+"/")
	}

	cfgPath := filepath.Join(top, ConfigFile)
	if _, err := os.Stat(cfgPath); err == nil && !o.Force {
		done = append(done, "既にある: "+filepath.ToSlash(cfgPath)+"（書き直すときは --force）")
		return done, nil
	}
	if o.Base == "" {
		o.Base = guessBase(top)
	}
	if o.Rule == "" {
		o.Rule = DefaultRule
	}
	cfg := Config{Base: o.Base, TwoDot: o.TwoDot, Rule: o.Rule, Rules: map[string][]string{"unity": builtinRules["unity"]}}
	if _, ok := builtinRules[o.Rule]; !ok {
		return nil, fmt.Errorf("ルール %q は組み込みにありません。%s に書き足してから選んでください（組み込み: unity, all）", o.Rule, ConfigFile)
	}
	if err := store.WriteJSON(cfgPath, cfg); err != nil {
		return nil, err
	}
	done = append(done, "書いた: "+filepath.ToSlash(cfgPath)+"（共有するならコミットする）")
	return done, nil
}

func guessBase(top string) string {
	for _, b := range []string{"main", "master"} {
		if _, err := gitx.ResolveCommit(top, b); err == nil {
			return b
		}
	}
	return ""
}
