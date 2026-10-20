package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yazawa-ichio/sedi-review/internal/gitx"
	"github.com/yazawa-ichio/sedi-review/internal/skill"
)

// skillFiles はエージェント向けの skill。実行ファイルと同じ版の手順を入れられるよう埋め込む。
//
//go:embed skills
var skillFiles embed.FS

func skillRoot() fs.FS {
	sub, err := fs.Sub(skillFiles, "skills")
	if err != nil {
		panic(err)
	}
	return sub
}

func skillFS(name string) fs.FS {
	sub, err := fs.Sub(skillRoot(), name)
	if err != nil {
		panic(err)
	}
	return sub
}

func cmdSkill(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("install / uninstall / status のどれかを指定してください")
	}
	all, err := skill.Names(skillRoot())
	if err != nil {
		return err
	}
	sub, rest := args[0], args[1:]
	flags := flag.NewFlagSet("skill "+sub, flag.ExitOnError)
	agents := flags.String("agent", "", "入れる先のエージェント（"+strings.Join(skill.Agents, " / ")+"。カンマで複数）")
	only := flags.String("skill", "", "扱う skill（"+strings.Join(all, " / ")+"。カンマで複数。省くとすべて）")
	scope := flags.String("scope", skill.ScopeUser, "user（すべてのリポジトリ）か project（このリポジトリだけ）")
	force := flags.Bool("force", false, "違う中身の skill があっても置き換える・消す")
	repo := flags.String("repo", ".", "project のときのリポジトリ")
	flags.Parse(rest)

	names := all
	if *only != "" {
		names = nil
		for _, n := range strings.Split(*only, ",") {
			n = strings.TrimSpace(n)
			if !slices.Contains(all, n) {
				return fmt.Errorf("skill %q はありません（あるもの: %s）", n, strings.Join(all, " / "))
			}
			names = append(names, n)
		}
	}

	switch sub {
	case "install", "uninstall":
		if *agents == "" {
			return fmt.Errorf("--agent で入れる先を指定してください（%s）", strings.Join(skill.Agents, " / "))
		}
		dests, err := skillDirs(strings.Split(*agents, ","), []string{*scope}, *repo)
		if err != nil {
			return err
		}
		for _, d := range dests {
			for _, name := range names {
				dir := filepath.Join(d.dir, name)
				var msg string
				if sub == "install" {
					msg, err = skill.Install(skillFS(name), dir, *force)
				} else {
					msg, err = skill.Uninstall(skillFS(name), dir, *force)
				}
				if err != nil {
					return fmt.Errorf("%s %s: %w", d.agent, name, err)
				}
				fmt.Printf("%s %s: %s\n", d.agent, name, msg)
			}
		}
		return nil
	case "status":
		agentNames := skill.Agents
		if *agents != "" {
			agentNames = strings.Split(*agents, ",")
		}
		dests, err := skillDirs(agentNames, []string{skill.ScopeUser, skill.ScopeProject}, *repo)
		if err != nil {
			return err
		}
		for _, d := range dests {
			for _, name := range names {
				dir := filepath.Join(d.dir, name)
				st, err := skill.Status(skillFS(name), dir)
				if err != nil {
					return err
				}
				fmt.Printf("%-6s %-7s %-17s %s  %s\n", d.agent, d.scope, name, st, dir)
			}
		}
		return nil
	}
	return fmt.Errorf("install / uninstall / status のどれかを指定してください: %s", sub)
}

type skillDest struct {
	agent, scope, dir string
}

// skillDirs は、エージェントと scope ごとの、skill を並べるフォルダを返す。
func skillDirs(agents, scopes []string, repo string) ([]skillDest, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	var top string
	for _, s := range scopes {
		if s == skill.ScopeProject {
			if t, err := gitx.TopLevel(repo); err == nil {
				top = t
			} else if len(scopes) == 1 {
				return nil, fmt.Errorf("project に入れるにはリポジトリの中で実行するか --repo を指定してください: %w", err)
			}
		}
	}
	var out []skillDest
	for _, a := range agents {
		a = strings.TrimSpace(a)
		for _, s := range scopes {
			if s == skill.ScopeProject && top == "" {
				continue
			}
			dir, err := skill.SkillsDir(a, s, home, top)
			if err != nil {
				return nil, err
			}
			out = append(out, skillDest{agent: a, scope: s, dir: dir})
		}
	}
	return out, nil
}
