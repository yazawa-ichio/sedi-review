// Package diff は git diff の標準の出力を読み、hunk に ID を付ける。
package diff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Kind は行の種類 ' ' '+' '-' '\\'。JSON では 1 文字の文字列にする。
type Kind byte

func (k Kind) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(rune(k)))
}

type Line struct {
	Kind Kind   `json:"k"`
	Text string `json:"t"`
	Old  int    `json:"o,omitempty"`
	New  int    `json:"n,omitempty"`
}

type Hunk struct {
	ID       string `json:"id"`
	Num      int    `json:"num"`
	Path     string `json:"path"`
	OldStart int    `json:"oldStart"`
	OldCount int    `json:"oldCount"`
	NewStart int    `json:"newStart"`
	NewCount int    `json:"newCount"`
	Context  string `json:"context,omitempty"`
	Adds     int    `json:"adds"`
	Dels     int    `json:"dels"`
	Lines    []Line `json:"lines,omitempty"`
}

type File struct {
	Path        string  `json:"path"`
	OldPath     string  `json:"oldPath,omitempty"`
	Status      string  `json:"status"` // added deleted modified renamed copied
	Binary      bool    `json:"binary"`
	OldBlob     string  `json:"oldBlob,omitempty"`
	NewBlob     string  `json:"newBlob,omitempty"`
	Fingerprint string  `json:"fingerprint"`
	Hunks       []*Hunk `json:"hunks"`
}

// Prefixes は差分のパスの前置き。diff.noprefix なら両方とも空。
type Prefixes struct {
	Src, Dst string
}

var mnemonic = []string{"a/", "b/", "c/", "i/", "w/", "o/", "1/", "2/"}

func (p Prefixes) strip(path string, src bool) string {
	want := p.Dst
	if src {
		want = p.Src
	}
	if want != "" && strings.HasPrefix(path, want) {
		return path[len(want):]
	}
	if want == "" && p.Src == "" && p.Dst == "" {
		return path
	}
	for _, m := range mnemonic {
		if strings.HasPrefix(path, m) {
			return path[len(m):]
		}
	}
	return path
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@ ?(.*)$`)

// Parse は git diff の出力を読む。中身の差分が無いファイルは返さない。
func Parse(data []byte, pre Prefixes) ([]*File, error) {
	if bytes.IndexByte(data, 0x1b) >= 0 {
		return nil, fmt.Errorf("差分に色の制御文字が含まれています。color.ui か color.diff が always になっていないか確かめてください")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	if !bytes.HasPrefix(data, []byte("diff --git ")) {
		return nil, fmt.Errorf("差分が git の標準の形ではありません。diff.external か GIT_EXTERNAL_DIFF が設定されていないか確かめてください")
	}
	lines := strings.Split(string(data), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	p := &parser{lines: lines, pre: pre}
	var files []*File
	for p.i < len(p.lines) {
		f, err := p.file()
		if err != nil {
			return nil, err
		}
		if f != nil {
			files = append(files, f)
		}
	}
	assignIDs(files)
	return files, nil
}

type parser struct {
	lines []string
	i     int
	pre   Prefixes
}

func (p *parser) file() (*File, error) {
	head := p.lines[p.i]
	if !strings.HasPrefix(head, "diff --git ") {
		return nil, fmt.Errorf("%d 行目: diff --git の行がありません: %q", p.i+1, head)
	}
	p.i++
	f := &File{Status: "modified"}
	var renameFrom, renameTo, minus, plus string
	var haveMinus, havePlus bool
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if strings.HasPrefix(l, "diff --git ") || strings.HasPrefix(l, "@@ ") {
			break
		}
		p.i++
		switch {
		case strings.HasPrefix(l, "new file mode"):
			f.Status = "added"
		case strings.HasPrefix(l, "deleted file mode"):
			f.Status = "deleted"
		case strings.HasPrefix(l, "rename from "):
			f.Status = "renamed"
			renameFrom = strings.TrimPrefix(l, "rename from ")
		case strings.HasPrefix(l, "rename to "):
			renameTo = strings.TrimPrefix(l, "rename to ")
		case strings.HasPrefix(l, "copy from "):
			f.Status = "copied"
			renameFrom = strings.TrimPrefix(l, "copy from ")
		case strings.HasPrefix(l, "copy to "):
			renameTo = strings.TrimPrefix(l, "copy to ")
		case strings.HasPrefix(l, "index "):
			ids := strings.Fields(strings.TrimPrefix(l, "index "))
			if len(ids) > 0 {
				if a, b, ok := strings.Cut(ids[0], ".."); ok {
					f.OldBlob, f.NewBlob = a, b
				}
			}
		case strings.HasPrefix(l, "--- "):
			minus, haveMinus = strings.TrimPrefix(l, "--- "), true
		case strings.HasPrefix(l, "+++ "):
			plus, havePlus = strings.TrimPrefix(l, "+++ "), true
		case strings.HasPrefix(l, "Binary files ") && strings.HasSuffix(l, " differ"):
			f.Binary = true
		}
	}
	if err := p.paths(f, head, renameFrom, renameTo, minus, plus, haveMinus, havePlus); err != nil {
		return nil, err
	}
	for p.i < len(p.lines) && strings.HasPrefix(p.lines[p.i], "@@ ") {
		h, err := p.hunk(f.Path)
		if err != nil {
			return nil, err
		}
		f.Hunks = append(f.Hunks, h)
	}
	if len(f.Hunks) == 0 && !f.Binary {
		return nil, nil
	}
	return f, nil
}

func (p *parser) paths(f *File, head, renameFrom, renameTo, minus, plus string, haveMinus, havePlus bool) error {
	var oldPath, newPath string
	if renameFrom != "" && renameTo != "" {
		var err error
		if oldPath, err = unquote(renameFrom); err != nil {
			return err
		}
		if newPath, err = unquote(renameTo); err != nil {
			return err
		}
	} else if haveMinus && havePlus {
		var err error
		if minus != "/dev/null" {
			if oldPath, err = unquote(minus); err != nil {
				return err
			}
			oldPath = p.pre.strip(oldPath, true)
		}
		if plus != "/dev/null" {
			if newPath, err = unquote(plus); err != nil {
				return err
			}
			newPath = p.pre.strip(newPath, false)
		}
	} else {
		a, b, err := splitPathPair(strings.TrimPrefix(head, "diff --git "))
		if err != nil {
			return err
		}
		if a, err = unquote(a); err != nil {
			return err
		}
		if b, err = unquote(b); err != nil {
			return err
		}
		oldPath, newPath = p.pre.strip(a, true), p.pre.strip(b, false)
	}
	switch f.Status {
	case "added":
		f.Path = newPath
	case "deleted":
		f.Path = oldPath
	default:
		f.Path = newPath
		if oldPath != newPath {
			f.OldPath = oldPath
		}
	}
	if f.Path == "" {
		f.Path = oldPath
	}
	return nil
}

func (p *parser) hunk(path string) (*Hunk, error) {
	m := hunkHeader.FindStringSubmatch(p.lines[p.i])
	if m == nil {
		return nil, fmt.Errorf("%d 行目: hunk の見出しを読めません: %q", p.i+1, p.lines[p.i])
	}
	p.i++
	h := &Hunk{Path: path, Context: m[5]}
	h.OldStart, _ = strconv.Atoi(m[1])
	h.OldCount = countOr1(m[2])
	h.NewStart, _ = strconv.Atoi(m[3])
	h.NewCount = countOr1(m[4])
	oldLeft, newLeft := h.OldCount, h.NewCount
	oldNo, newNo := h.OldStart, h.NewStart
	for p.i < len(p.lines) {
		l := p.lines[p.i]
		if strings.HasPrefix(l, `\`) {
			h.Lines = append(h.Lines, Line{Kind: '\\', Text: l[1:]})
			p.i++
			continue
		}
		if oldLeft == 0 && newLeft == 0 {
			break
		}
		kind := byte(' ')
		text := ""
		if l != "" {
			kind, text = l[0], l[1:]
		}
		switch kind {
		case ' ':
			h.Lines = append(h.Lines, Line{Kind: ' ', Text: text, Old: oldNo, New: newNo})
			oldNo++
			newNo++
			oldLeft--
			newLeft--
		case '-':
			h.Lines = append(h.Lines, Line{Kind: '-', Text: text, Old: oldNo})
			oldNo++
			oldLeft--
			h.Dels++
		case '+':
			h.Lines = append(h.Lines, Line{Kind: '+', Text: text, New: newNo})
			newNo++
			newLeft--
			h.Adds++
		default:
			return nil, fmt.Errorf("%d 行目: hunk の行を読めません: %q", p.i+1, l)
		}
		if oldLeft < 0 || newLeft < 0 {
			return nil, fmt.Errorf("%d 行目: hunk の行数が見出しと合いません", p.i+1)
		}
		p.i++
	}
	if oldLeft != 0 || newLeft != 0 {
		return nil, fmt.Errorf("%s: hunk が途中で終わっています", path)
	}
	return h, nil
}

func countOr1(s string) int {
	if s == "" {
		return 1
	}
	n, _ := strconv.Atoi(s)
	return n
}

func assignIDs(files []*File) {
	num := 0
	for _, f := range files {
		seen := map[string]int{}
		for _, h := range f.Hunks {
			sum := hunkDigest(f.Path, h)
			if n := seen[sum]; n > 0 {
				seen[sum] = n + 1
				sum = hexSum(sum + "#" + strconv.Itoa(n))
			} else {
				seen[sum] = 1
			}
			h.ID = sum[:16]
			num++
			h.Num = num
		}
		f.Fingerprint = fileFingerprint(f)
	}
}

func hunkDigest(path string, h *Hunk) string {
	var b strings.Builder
	b.WriteString(path)
	b.WriteByte('\n')
	for _, l := range h.Lines {
		b.WriteByte(byte(l.Kind))
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return hexSum(b.String())
}

func fileFingerprint(f *File) string {
	if f.Binary {
		return hexSum("bin\n" + f.Path + "\n" + f.OldBlob + ".." + f.NewBlob)[:16]
	}
	ids := make([]string, len(f.Hunks))
	for i, h := range f.Hunks {
		ids[i] = h.ID
	}
	return hexSum("text\n" + f.Path + "\n" + strings.Join(ids, "\n"))[:16]
}

func hexSum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
