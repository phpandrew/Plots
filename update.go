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

	"plots/internal/updatesig"
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

// SigURL returns the download URL of the update's signature file.
func (r *Release) SigURL() string {
	for _, a := range r.Assets {
		if strings.EqualFold(a.Name, "plots-update.exe.sig") {
			return a.URL
		}
	}
	return ""
}

const updateName, updateSigName = "Plots-update.exe", "Plots-update.exe.sig"

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

// fetch downloads url into w, at most limit bytes.
func fetch(url string, w io.Writer, limit int64) (int64, error) {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Plots/"+version)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return 0, errors.New(Tf("download failed: %s", resp.Status))
	}
	n, err := io.Copy(w, io.LimitReader(resp.Body, limit+1))
	if err == nil && n > limit {
		err = errors.New(T("download was incomplete"))
	}
	return n, err
}

// checkUpdateFile verifies a downloaded or dropped-in update: the signature
// must be from the Plots release key, the file must match it, and the
// version must be newer than this one. It returns the signed version.
func checkUpdateFile(exePath string, sig []byte) (string, error) {
	pub, err := updatesig.PublicKey(updatePublicKey)
	if err != nil {
		return "", errors.New(T("this build can't check updates; download the new version from GitHub"))
	}
	v, err := updatesig.VerifyFile(pub, exePath, sig)
	if err != nil {
		return "", errors.New(T("the update isn't signed by Plots, so it wasn't installed"))
	}
	if !isNewer(v, version) {
		return "", errors.New(Tf("the update (%s) isn't newer than this version", v))
	}
	return v, nil
}

// downloadUpdate saves the release exe next to Plots.exe as Plots-update.exe,
// with its signature beside it, once both check out. The running app then
// verifies them again, swaps the exe in and restarts.
func downloadUpdate(r *Release) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	sigURL, exeURL := r.SigURL(), r.AssetURL()
	if sigURL == "" || exeURL == "" {
		return errors.New(T("this release isn't signed, so Plots won't install it; download it from GitHub instead"))
	}
	var sig strings.Builder
	if _, err := fetch(sigURL, &sig, 4096); err != nil {
		return err
	}
	tmp := filepath.Join(dir, updateName+".part")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := fetch(exeURL, f, 200<<20)
	f.Close()
	if err == nil && n < 1<<20 { // a real build is several MB
		err = errors.New(T("download was incomplete"))
	}
	if err == nil {
		var v string
		if v, err = checkUpdateFile(tmp, []byte(sig.String())); err == nil && strings.TrimPrefix(r.Tag, "v") != v {
			err = errors.New(T("the update isn't signed by Plots, so it wasn't installed"))
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	// signature first: the swap only happens once both files are there
	if err := os.WriteFile(filepath.Join(dir, updateSigName), []byte(sig.String()), 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, updateName))
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
