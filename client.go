package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/yazawa-ichio/sedi-review/internal/review"
	"github.com/yazawa-ichio/sedi-review/internal/store"
)

type client struct {
	base  string
	token string
	http  *http.Client
}

// connect は動いているサーバーにつなぐ。動いていなければ nil。
func connect(repo, id string) *client {
	top, err := review.Top(repo)
	if err != nil {
		return nil
	}
	dir, err := store.Dir(top, id)
	if err != nil {
		return nil
	}
	var info store.ServerInfo
	if ok, err := store.ReadJSON(filepath.Join(dir, "server.json"), &info); err != nil || !ok {
		return nil
	}
	c := &client{base: fmt.Sprintf("http://127.0.0.1:%d", info.Port), token: info.Token, http: &http.Client{}}
	ping := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("GET", c.base+"/api/agent/ping", nil)
	req.Header.Set("X-Sedi-Token", c.token)
	res, err := ping.Do(req)
	if err != nil {
		return nil
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil
	}
	return c
}

func (c *client) do(method, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Sedi-Token", c.token)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s", strings.TrimSpace(string(data)))
	}
	return data, nil
}

func (c *client) get(path string) ([]byte, error) { return c.do("GET", path, nil) }

func (c *client) post(path string, v any) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return c.do("POST", path, data)
}

func (c *client) postRaw(path string, data []byte) ([]byte, error) { return c.do("POST", path, data) }
