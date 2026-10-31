// Package update keeps the organism current: it polls GitHub releases in the
// background, announces newer versions live over the bus (banner pops into
// every open browser — no refresh), and powers the `evolv update` command
// that replaces the running binary in-place.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const repoAPI = "https://api.github.com/repos/dxn111/EVOLV/releases/latest"
const repoDownload = "https://github.com/dxn111/EVOLV/releases/latest/download"

// Info describes an available release.
type Info struct {
	Version string `json:"version"` // e.g. "0.3.0" (no leading v)
	URL     string `json:"url"`     // release page
	Notes   string `json:"notes"`   // release body (truncated)
	Current string `json:"current"`
}

var versionRe = regexp.MustCompile(`^v?(\d+\.\d+\.\d+)$`)

// Latest queries GitHub for the newest release. current is the running version.
func Latest(ctx context.Context, current string) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", repoAPI, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "evolv-organism/"+current)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("github api: %s", resp.Status)
	}
	var rel struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Body    string `json:"body"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	m := versionRe.FindStringSubmatch(strings.TrimSpace(rel.TagName))
	if m == nil {
		return nil, fmt.Errorf("unparsable tag %q", rel.TagName)
	}
	info := &Info{Version: m[1], URL: rel.HTMLURL, Current: current}
	if len(rel.Body) > 400 {
		rel.Body = rel.Body[:400] + "…"
	}
	info.Notes = rel.Body
	if Newer(current, info.Version) {
		return info, nil
	}
	return nil, nil // up to date
}

// Newer compares dotted versions semantically: is v newer than cur?
func Newer(cur, v string) bool {
	a, b := parse(cur), parse(v)
	for i := 0; i < 3; i++ {
		if b[i] > a[i] {
			return true
		}
		if b[i] < a[i] {
			return false
		}
	}
	return false
}

func parse(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	for i, p := range parts {
		if i > 2 {
			break
		}
		n := 0
		fmt.Sscanf(p, "%d", &n)
		out[i] = n
	}
	return out
}

// AssetName is the release asset filename for this platform.
func AssetName() string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("evolv_%s_%s.exe", runtime.GOOS, runtime.GOARCH)
	}
	return fmt.Sprintf("evolv_%s_%s", runtime.GOOS, runtime.GOARCH)
}

// AssetExists is a cheap HEAD check so the updater stays quiet when a release
// ships without this platform's binary.
func AssetExists(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, "HEAD", repoDownload+"/"+AssetName(), nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// SelfUpdate downloads the latest binary and swaps it over the running
// executable. Returns the new version.
func SelfUpdate(ctx context.Context) (string, error) {
	info, err := Latest(ctx, "0.0.0")
	if err != nil {
		return "", err
	}
	asset := repoDownload + "/" + AssetName()
	req, err := http.NewRequestWithContext(ctx, "GET", asset, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download %s: %s", asset, resp.Status)
	}
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	tmp := self + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	f.Close()
	old := self + ".old"
	_ = os.Remove(old)
	if err := os.Rename(self, old); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, self); err != nil {
		_ = os.Rename(old, self) // roll back
		os.Remove(tmp)
		return "", err
	}
	_ = os.Remove(old)
	return info.Version, nil
}
