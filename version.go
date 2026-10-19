package main

import (
	"fmt"
	"runtime/debug"
)

// version は -ldflags "-X main.version=..." で上書きできる。空なら Go が記録した版を使う。
var version = ""

func cmdVersion(args []string) error {
	info, ok := debug.ReadBuildInfo()
	fmt.Println(versionString(version, info, ok))
	return nil
}

// versionString は、go install @vX.Y.Z で入れたものはその版を、手元で作ったものはコミットを返す。
func versionString(override string, info *debug.BuildInfo, ok bool) string {
	if override != "" {
		return "sedi-review " + override
	}
	if !ok || info == nil {
		return "sedi-review (版が分かりません)"
	}
	v := info.Main.Version
	if v != "" && v != "(devel)" {
		return fmt.Sprintf("sedi-review %s (%s)", v, info.GoVersion)
	}
	var rev, modified string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if rev == "" {
		return fmt.Sprintf("sedi-review (開発版) (%s)", info.GoVersion)
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if modified == "true" {
		rev += "（コミットしていない変更あり）"
	}
	return fmt.Sprintf("sedi-review (開発版 %s) (%s)", rev, info.GoVersion)
}
