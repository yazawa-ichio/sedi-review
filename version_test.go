package main

import (
	"runtime/debug"
	"testing"
)

func TestVersionString(t *testing.T) {
	settings := func(rev, modified string) []debug.BuildSetting {
		return []debug.BuildSetting{{Key: "vcs.revision", Value: rev}, {Key: "vcs.modified", Value: modified}}
	}
	cases := []struct {
		name     string
		override string
		info     *debug.BuildInfo
		ok       bool
		want     string
	}{
		{"go install の版", "", &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "v0.1.0"}}, true,
			"sedi-review v0.1.0 (go1.26.5)"},
		{"手元で作ったもの", "", &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "(devel)"},
			Settings: settings("0123456789abcdef0123", "false")}, true,
			"sedi-review (開発版 0123456789ab) (go1.26.5)"},
		{"変更を含む手元のもの", "", &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "(devel)"},
			Settings: settings("0123456789abcdef0123", "true")}, true,
			"sedi-review (開発版 0123456789ab（コミットしていない変更あり）) (go1.26.5)"},
		{"コミットが分からない", "", &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Version: "(devel)"}}, true,
			"sedi-review (開発版) (go1.26.5)"},
		{"ldflags で上書き", "v9.9.9", nil, false, "sedi-review v9.9.9"},
		{"情報なし", "", nil, false, "sedi-review (版が分かりません)"},
	}
	for _, c := range cases {
		if got := versionString(c.override, c.info, c.ok); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
