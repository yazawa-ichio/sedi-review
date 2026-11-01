package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/review"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// start は小さなリポジトリのレビューでサーバーを立てる。
func start(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\n2\n3\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("\x00\x01"), 0o644)
	os.WriteFile(filepath.Join(dir, "s.wav"), []byte("RIFF\x00\x00\x00\x00WAVEold"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	git(t, dir, "checkout", "-q", "-b", "work")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("1\nTWO\n3\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "bin.dat"), []byte("\x00\x02"), 0o644)
	os.WriteFile(filepath.Join(dir, "s.wav"), []byte("RIFF\x00\x00\x00\x00WAVEnew"), 0o644)
	git(t, dir, "commit", "-q", "-am", "work")

	if _, err := review.Init(dir, review.InitOptions{}); err != nil {
		t.Fatal(err)
	}
	st, err := review.Create(store.Spec{Repo: dir, Base: "main", Target: "work", ThreeDot: true})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(st)
	ln, url, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { srv.Serve(ctx, ln); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return srv, strings.TrimSuffix(url, "/")
}

func do(t *testing.T, method, url string, body string, headers map[string]string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data)
}

func TestGuards(t *testing.T) {
	srv, base := start(t)
	page := map[string]string{"X-Sedi": "1"}
	cases := []struct {
		name    string
		method  string
		path    string
		headers map[string]string
		want    int
	}{
		{"summary", "GET", "/api/review", nil, 200},
		{"foreign host", "GET", "/api/review", map[string]string{"Host": "evil.example:80"}, 403},
		{"write without header", "POST", "/api/refresh", nil, 403},
		{"write from other origin", "POST", "/api/refresh", map[string]string{"X-Sedi": "1", "Origin": "http://evil.example"}, 403},
		{"write from page", "POST", "/api/refresh", page, 200},
		{"agent without token", "GET", "/api/agent/ping", nil, 403},
		{"agent with token", "GET", "/api/agent/ping", map[string]string{"X-Sedi-Token": srv.token}, 200},
		{"page header is not a token", "POST", "/api/agent/refresh", page, 403},
	}
	for _, c := range cases {
		if got, body := do(t, c.method, base+c.path, "", c.headers); got != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, got, c.want, body)
		}
	}
}

func TestSummaryHasNoNullLists(t *testing.T) {
	_, base := start(t)
	_, body := do(t, "GET", base+"/api/review", "", nil)
	for _, key := range []string{`"hunks":null`, `"files":null`, `"sections":null`, `"threads":null`, `"ranges":null`} {
		if strings.Contains(body, key) {
			t.Errorf("summary contains %s", key)
		}
	}
	var sum struct {
		Files []struct {
			Lines json.RawMessage
		}
	}
	if err := json.Unmarshal([]byte(body), &sum); err != nil || len(sum.Files) != 3 {
		t.Fatalf("summary: %v %s", err, body)
	}
}

func TestAudioBlob(t *testing.T) {
	_, base := start(t)
	_, body := do(t, "GET", base+"/api/review", "", nil)
	var sum struct {
		Files []struct {
			Path    string
			Audio   bool
			Image   bool
			OldBlob string
			NewBlob string
		}
	}
	if err := json.Unmarshal([]byte(body), &sum); err != nil {
		t.Fatal(err)
	}
	var newBlob string
	for _, f := range sum.Files {
		if f.Path == "s.wav" {
			if !f.Audio || f.Image || f.OldBlob == "" || f.NewBlob == "" {
				t.Fatalf("s.wav summary: %+v", f)
			}
			newBlob = f.NewBlob
		}
		if f.Path == "bin.dat" && f.Audio {
			t.Error("bin.dat is not audio")
		}
	}
	if newBlob == "" {
		t.Fatal("s.wav is missing from the summary")
	}
	req, _ := http.NewRequest("GET", base+"/api/blob?path=s.wav&id="+newBlob, nil)
	req.Header.Set("Range", "bytes=0-3")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusPartialContent || string(data) != "RIFF" || res.Header.Get("Content-Type") != "audio/wav" {
		t.Errorf("range: %d %q %s", res.StatusCode, data, res.Header.Get("Content-Type"))
	}
	if code, _ := do(t, "GET", base+"/api/blob?path=bin.dat&id="+newBlob, "", nil); code != 404 {
		t.Errorf("a blob id must not be served under another path, got %d", code)
	}
}

func TestLineKindIsAString(t *testing.T) {
	_, base := start(t)
	_, body := do(t, "GET", base+"/api/file?path=a.txt", "", nil)
	if !strings.Contains(body, `"k":"+"`) || !strings.Contains(body, `"k":"-"`) {
		t.Errorf("line kinds should be one-character strings: %s", body)
	}
}

func TestPollDeliversPending(t *testing.T) {
	srv, base := start(t)
	agent := map[string]string{"X-Sedi-Token": srv.token}
	page := map[string]string{"X-Sedi": "1", "Content-Type": "application/json"}

	code, body := do(t, "POST", base+"/api/threads", `{"kind":"question","anchor":{"type":"file","path":"a.txt"},"body":"なぜ?"}`, page)
	if code != 200 {
		t.Fatalf("new thread: %d %s", code, body)
	}
	var th store.Thread
	json.Unmarshal([]byte(body), &th)

	got := make(chan string, 1)
	go func() {
		_, b := do(t, "GET", base+"/api/agent/poll?timeout=10", "", agent)
		got <- b
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		srv.mu.Lock()
		n := srv.pollers
		srv.mu.Unlock()
		if n > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("poll did not attach")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code, body := do(t, "POST", base+"/api/threads/"+th.ID+"/send", "", page); code != 200 {
		t.Fatalf("send: %d %s", code, body)
	}
	select {
	case b := <-got:
		if !strings.Contains(b, `"status":"messages"`) || !strings.Contains(b, th.ID) {
			t.Errorf("poll payload: %s", b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("poll did not return")
	}
	srv.st.Lock()
	state := srv.st.Threads[th.ID].State
	srv.st.Unlock()
	if state != store.StateSent {
		t.Errorf("delivered thread should be sent, got %s", state)
	}

	reply := fmt.Sprintf(`{"thread":%q,"body":"理由です"}`, th.ID)
	if code, body := do(t, "POST", base+"/api/agent/reply", reply, agent); code != 200 {
		t.Fatalf("reply: %d %s", code, body)
	}
	if code, _ := do(t, "POST", base+"/api/agent/unmark", `{"kind":"file","key":"a.txt"}`, agent); code != 200 {
		t.Error("agent should be able to unmark")
	}
}

func TestPollTimesOut(t *testing.T) {
	srv, base := start(t)
	_, body := do(t, "GET", base+"/api/agent/poll?timeout=1", "", map[string]string{"X-Sedi-Token": srv.token})
	if !strings.Contains(body, `"status":"timeout"`) {
		t.Errorf("want timeout, got %s", body)
	}
}

func TestSendAllQueuesOnlyUnsent(t *testing.T) {
	srv, base := start(t)
	page := map[string]string{"X-Sedi": "1", "Content-Type": "application/json"}
	agent := map[string]string{"X-Sedi-Token": srv.token, "Content-Type": "application/json"}
	var ids []string
	for i := 0; i < 2; i++ {
		_, body := do(t, "POST", base+"/api/threads", `{"kind":"question","anchor":{"type":"review"},"body":"q"}`, page)
		var th store.Thread
		json.Unmarshal([]byte(body), &th)
		ids = append(ids, th.ID)
	}
	if code, body := do(t, "POST", base+"/api/agent/reply", fmt.Sprintf(`{"thread":%q,"body":"done"}`, ids[0]), agent); code != 200 {
		t.Fatalf("reply: %d %s", code, body)
	}
	_, body := do(t, "POST", base+"/api/send-all", "", page)
	if !strings.Contains(body, `"queued":1`) {
		t.Errorf("only the unsent thread should be queued: %s", body)
	}
}
