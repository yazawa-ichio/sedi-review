package main

import (
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/yazawa-ichio/sedi-review/internal/gitx"
	"github.com/yazawa-ichio/sedi-review/internal/skill"
)

// skillFiles はエージェント向けの skill。実行ファイルと同じ版の手順を入れられるよう埋め込む。
//
//go:embed skills/sedi-review
var skillFiles embed.FS

func skillFS() fs.FS {
	sub, err := fs.Sub(skillFiles, "skills/"+skill.Name)
	if err != nil {
		panic(err)
	}
	return sub
}

func cmdSkill(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("install / uninstall / status のどれかを指定してください")
	}
	sub, rest := args[0], args[1:]
	flags := flag.NewFlagSet("skill "+sub, flag.ExitOnError)
	agents := flags.String("agent", "", "入れる先のエージェント（"+strings.Join(skill.Agents, " / ")+"。カンマで複数）")
	scope := flags.String("scope", skill.ScopeUser, "user（すべてのリポジトリ）か project（このリポジトリだけ）")
	force := flags.Bool("force", false, "違う中身の skill があっても置き換える・消す")
	repo := flags.String("repo", ".", "project のときのリポジトリ")
	flags.Parse(rest)

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
			var msg string
			if sub == "install" {
				msg, err = skill.Install(skillFS(), d.dir, *force)
			} else {
				msg, err = skill.Uninstall(skillFS(), d.dir, *force)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", d.agent, err)
			}
			fmt.Printf("%s: %s\n", d.agent, msg)
		}
		return nil
	case "status":
		names := skill.Agents
		if *agents != "" {
			names = strings.Split(*agents, ",")
		}
		dests, err := skillDirs(names, []string{skill.ScopeUser, skill.ScopeProject}, *repo)
		if err != nil {
			return err
		}
		for _, d := range dests {
			st, err := skill.Status(skillFS(), d.dir)
			if err != nil {
				return err
			}
			fmt.Printf("%-6s %-7s %s  %s\n", d.agent, d.scope, st, d.dir)
		}
		return nil
	}
	return fmt.Errorf("install / uninstall / status のどれかを指定してください: %s", sub)
}

type skillDest struct {
	agent, scope, dir string
}

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
			dir, err := skill.Dir(a, s, home, top)
			if err != nil {
				return nil, err
			}
			out = append(out, skillDest{agent: a, scope: s, dir: dir})
		}
	}
	return out, nil
}
