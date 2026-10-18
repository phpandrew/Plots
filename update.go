package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	repoSlug   = "phpandrew/Plots"
	repoURL    = "https://github.com/" + repoSlug
	releaseURL = repoURL + "/releases"
	issuesURL  = repoURL + "/issues/new"
)

type Release struct {
	Tag     string `json:"tag_name"`
	Name    string `json:"name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// AssetURL returns the download URL for the self-update exe.
func (r *Release) AssetURL() string {
	for _, want := range []string{"plots-update.exe", "plots.exe"} {
		for _, a := range r.Assets {
			if strings.EqualFold(a.Name, want) {
				return a.URL
			}
		}
	}
	return ""
}

var httpClient = &http.Client{Timeout: 30 * time.Second}

func latestRelease() (*Release, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repoSlug+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Plots/"+version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errors.New(T("no releases published yet"))
	}
	if resp.StatusCode != 200 {
		return nil, errors.New(Tf("GitHub answered %s", resp.Status))
	}
	var r Release
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// downloadUpdate saves the release exe next to Plots.exe as Plots-update.exe,
// where the running app picks it up and restarts itself.
func downloadUpdate(url string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dst := filepath.Join(filepath.Dir(exe), "Plots-update.exe")
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Plots/"+version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New(Tf("download failed: %s", resp.Status))
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err != nil || n < 1<<20 { // a real build is several MB
		os.Remove(tmp)
		if err == nil {
			err = errors.New(T("download was incomplete"))
		}
		return err
	}
	return os.Rename(tmp, dst)
}

// parseVersion turns "v0.2.14" / "0.2.0-dev.1017" into numbers and a dev flag.
func parseVersion(v string) (nums [3]int, dev bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		dev = true
		v = v[:i]
	}
	for i, part := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(part)
		nums[i] = n
	}
	return
}

// isNewer reports whether release tag `remote` is newer than `local`.
// A dev build counts as older than a release with the same number.
func isNewer(remote, local string) bool {
	if strings.HasPrefix(local, "dev") {
		return true
	}
	r, _ := parseVersion(remote)
	l, ldev := parseVersion(local)
	for i := 0; i < 3; i++ {
		if r[i] != l[i] {
			return r[i] > l[i]
		}
	}
	return ldev
}
