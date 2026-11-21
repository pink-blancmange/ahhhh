package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	appDir     = "/opt/appimages"
	binDir     = "/usr/local/bin"
	desktopDir = "/usr/share/applications"
	iconDir    = "/usr/share/icons/hicolor/256x256/apps"
)

type AppMeta struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	GitHubRepo  string `json:"github_repo,omitempty"`
	InstalledAt string `json:"installed_at"`
	Tag         string `json:"tag,omitempty"`
	AssetName   string `json:"asset_name,omitempty"`
	Kind        string `json:"kind,omitempty"` // "appimage", "archive", "binary"
	ExecPath    string `json:"exec_path,omitempty"`
	Description string `json:"description,omitempty"`
}

type GitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type GitHubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	Body    string        `json:"body"`
	Assets  []GitHubAsset `json:"assets"`
}

type GitHubRepoInfo struct {
	FullName    string   `json:"full_name"`
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Topics      []string `json:"topics"`
	Stars       int      `json:"stargazers_count"`
}

type ResolvedGitHubTarget struct {
	Repo        string `json:"repo"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Tag         string `json:"tag"`
	AssetName   string `json:"asset_name"`
	DownloadURL string `json:"download_url"`
	Kind        string `json:"kind"`
	Size        int64  `json:"size"`
}

func newGitHubRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ahhhh-appimage-manager/0.4.1")
	req.Header.Set("Accept", "application/vnd.github+json")
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	} else if tok := strings.TrimSpace(os.Getenv("GH_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return "app"
	}
	return s
}

func parseGitHubRepo(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "https://github.com/")
	s = strings.TrimPrefix(s, "http://github.com/")
	s = strings.TrimPrefix(s, "https://www.github.com/")
	s = strings.TrimPrefix(s, "http://www.github.com/")
	s = strings.TrimSuffix(s, ".git")
	s = strings.Trim(s, "/")
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, repo := parts[0], parts[1]
	valid := regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	if !valid.MatchString(owner) || !valid.MatchString(repo) {
		return ""
	}
	return owner + "/" + repo
}

func classifyAssetKind(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".appimage"):
		return "appimage"
	case strings.HasSuffix(lower, ".tar.gz"),
		strings.HasSuffix(lower, ".tgz"),
		strings.HasSuffix(lower, ".tar.xz"),
		strings.HasSuffix(lower, ".txz"),
		strings.HasSuffix(lower, ".zip"):
		return "archive"
	default:
		return "binary"
	}
}

func scoreLinuxAsset(name string, hostArch string) int {
	lower := strings.ToLower(name)

	badExts := []string{
		".deb", ".rpm", ".dmg", ".pkg", ".exe", ".msi", ".apk", ".ipa",
		".sha256", ".sha512", ".sha1", ".md5", ".asc", ".sig", ".minisig",
		".sbom", ".json", ".txt", ".yml", ".yaml", ".zsync", ".blockmap",
	}
	for _, ext := range badExts {
		if strings.HasSuffix(lower, ext) {
			return -1000
		}
	}
	if strings.Contains(lower, "source") || strings.Contains(lower, "src.") {
		return -1000
	}

	nonLinuxOS := []string{
		"windows", "win32", "win64", "-win", "_win", "macos", "darwin", "osx",
		"apple", "freebsd", "openbsd", "netbsd", "android", "ios",
	}
	for _, osTok := range nonLinuxOS {
		if strings.Contains(lower, osTok) {
			return -1000
		}
	}

	isArm64 := strings.Contains(lower, "aarch64") || strings.Contains(lower, "arm64")
	isArm32 := strings.Contains(lower, "armv7") || strings.Contains(lower, "armv6") || strings.Contains(lower, "armhf")
	isX86_64 := strings.Contains(lower, "x86_64") || strings.Contains(lower, "amd64") || strings.Contains(lower, "x64")
	isI386 := strings.Contains(lower, "i386") || strings.Contains(lower, "i686") || strings.Contains(lower, "x86.") || strings.Contains(lower, "386")

	if hostArch == "arm64" {
		if isX86_64 || isI386 || isArm32 {
			return -1000
		}
	} else {
		if isArm64 || isArm32 || isI386 {
			return -1000
		}
	}

	score := 0
	if strings.Contains(lower, "linux") {
		score += 35
	}
	if hostArch == "arm64" && isArm64 {
		score += 40
	} else if hostArch != "arm64" && isX86_64 {
		score += 40
	}

	kind := classifyAssetKind(name)
	switch kind {
	case "appimage":
		score += 100
	case "archive":
		score += 65
	case "binary":
		score += 45
	}

	if strings.Contains(lower, "musl") {
		score -= 5
	}
	if strings.Contains(lower, "gnu") || strings.Contains(lower, "glibc") {
		score += 5
	}

	return score
}

func fetchLatestGitHubRelease(repo string) (*GitHubRelease, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := newGitHubRequest(apiURL)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		req2, err2 := newGitHubRequest(fmt.Sprintf("https://api.github.com/repos/%s/releases?per_page=5", repo))
		if err2 != nil {
			return nil, err2
		}
		resp2, err2 := client.Do(req2)
		if err2 != nil {
			return nil, err2
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("GitHub releases returned HTTP %d for %s", resp2.StatusCode, repo)
		}
		var list []GitHubRelease
		if err := json.NewDecoder(resp2.Body).Decode(&list); err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, fmt.Errorf("no releases found for %s", repo)
		}
		return &list[0], nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d for %s", resp.StatusCode, repo)
	}

	var rel GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

func fetchGitHubRepoInfo(repo string) (*GitHubRepoInfo, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := newGitHubRequest(fmt.Sprintf("https://api.github.com/repos/%s", repo))
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var info GitHubRepoInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

func resolveGitHubTarget(repoInput string, customName string) (*ResolvedGitHubTarget, error) {
	repo := parseGitHubRepo(repoInput)
	if repo == "" {
		return nil, fmt.Errorf("invalid GitHub repository %q (use owner/repo or https://github.com/owner/repo)", repoInput)
	}

	rel, err := fetchLatestGitHubRelease(repo)
	if err != nil {
		return nil, err
	}

	hostArch := runtime.GOARCH
	bestScore := -999
	var bestAsset *GitHubAsset
	for i := range rel.Assets {
		a := &rel.Assets[i]
		sc := scoreLinuxAsset(a.Name, hostArch)
		if sc > bestScore {
			bestScore = sc
			bestAsset = a
		}
	}

	if bestAsset == nil || bestScore < 0 {
		return nil, fmt.Errorf("no compatible Linux release asset found in %s (%s)", repo, rel.TagName)
	}

	parts := strings.Split(repo, "/")
	appName := strings.TrimSpace(customName)
	if appName == "" {
		appName = parts[1]
	}

	desc := ""
	if info, err := fetchGitHubRepoInfo(repo); err == nil && info != nil {
		desc = strings.TrimSpace(info.Description)
	}

	return &ResolvedGitHubTarget{
		Repo:        repo,
		Name:        appName,
		Description: desc,
		Tag:         rel.TagName,
		AssetName:   bestAsset.Name,
		DownloadURL: bestAsset.BrowserDownloadURL,
		Kind:        classifyAssetKind(bestAsset.Name),
		Size:        bestAsset.Size,
	}, nil
}

func parseSemverParts(tag string) ([]int, bool) {
	clean := strings.TrimSpace(tag)
	clean = strings.TrimPrefix(clean, "v")
	clean = strings.TrimPrefix(clean, "V")
	if idx := strings.IndexAny(clean, "+-"); idx != -1 {
		clean = clean[:idx]
	}
	if clean == "" {
		return nil, false
	}
	rawParts := strings.Split(clean, ".")
	nums := make([]int, 0, len(rawParts))
	for _, p := range rawParts {
		if p == "" {
			return nil, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		nums = append(nums, n)
	}
	return nums, len(nums) > 0
}

// compareVersionTags returns 1 if latest > installed, -1 if latest < installed, 0 if equal.
func compareVersionTags(installedTag, latestTag string) int {
	aNorm := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(installedTag), "v"), "V")
	bNorm := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(latestTag), "v"), "V")
	if aNorm == bNorm {
		return 0
	}

	aParts, aOk := parseSemverParts(installedTag)
	bParts, bOk := parseSemverParts(latestTag)
	if aOk && bOk {
		maxLen := len(aParts)
		if len(bParts) > maxLen {
			maxLen = len(bParts)
		}
		for i := 0; i < maxLen; i++ {
			av := 0
			if i < len(aParts) {
				av = aParts[i]
			}
			bv := 0
			if i < len(bParts) {
				bv = bParts[i]
			}
			if bv > av {
				return 1
			}
			if bv < av {
				return -1
			}
		}
		return 0
	}

	if bNorm > aNorm {
		return 1
	}
	return -1
}

func downloadFile(url, dest string) error {
	client := &http.Client{Timeout: 10 * time.Minute}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ahhhh-appimage-manager/0.4.1")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, resp.Body)
	return err
}

func isELFFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [4]byte
	n, err := io.ReadFull(f, hdr[:])
	if err != nil || n < 4 {
		return false
	}
	return bytes.Equal(hdr[:], []byte{0x7f, 'E', 'L', 'F'})
}

func extractArchive(archivePath, archiveName, destDir string) error {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	lower := strings.ToLower(archiveName)

	switch {
	case strings.HasSuffix(lower, ".zip"):
		r, err := zip.OpenReader(archivePath)
		if err != nil {
			return err
		}
		defer r.Close()
		for _, f := range r.File {
			cleanName := filepath.Clean(f.Name)
			if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
				continue
			}
			target := filepath.Join(destDir, cleanName)
			if f.FileInfo().IsDir() {
				_ = os.MkdirAll(target, 0o755)
				continue
			}
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			rc, err := f.Open()
			if err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode())
			if err != nil {
				rc.Close()
				return err
			}
			_, copyErr := io.Copy(out, rc)
			out.Close()
			rc.Close()
			if copyErr != nil {
				return copyErr
			}
		}
		return nil

	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		f, err := os.Open(archivePath)
		if err != nil {
			return err
		}
		defer f.Close()
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		return untarStream(gz, destDir)

	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		cmd := exec.Command("tar", "-xJf", archivePath, "-C", destDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tar -xJf failed: %v (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil

	default:
		return fmt.Errorf("unsupported archive format: %s", archiveName)
	}
}

func untarStream(r io.Reader, destDir string) error {
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		cleanName := filepath.Clean(hdr.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) || cleanName == "." {
			continue
		}
		target := filepath.Join(destDir, cleanName)
		switch hdr.Typeflag {
		case tar.TypeDir:
			_ = os.MkdirAll(target, 0o755)
		case tar.TypeReg:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			mode := os.FileMode(hdr.Mode)
			if mode == 0 {
				mode = 0o644
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return copyErr
			}
		case tar.TypeSymlink:
			_ = os.MkdirAll(filepath.Dir(target), 0o755)
			_ = os.Remove(target)
			_ = os.Symlink(hdr.Linkname, target)
		}
	}
	return nil
}

func findPrimaryBinaryInTree(rootDir, slug string) (string, string, error) {
	type cand struct {
		path  string
		score int
	}
	var cands []cand
	var bestIcon string

	_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		base := strings.ToLower(info.Name())
		if bestIcon == "" && (strings.HasSuffix(base, ".png") || strings.HasSuffix(base, ".svg")) {
			if strings.Contains(base, slug) || strings.Contains(base, "icon") || strings.Contains(base, "logo") {
				bestIcon = path
			}
		}

		if info.Mode()&0o111 != 0 || isELFFile(path) {
			if isELFFile(path) {
				sc := 10
				if base == slug {
					sc += 100
				} else if strings.Contains(base, slug) {
					sc += 50
				}
				rel, _ := filepath.Rel(rootDir, path)
				depth := strings.Count(rel, string(os.PathSeparator))
				sc -= depth * 5
				if strings.Contains(strings.ToLower(rel), "/bin/") {
					sc += 25
				}
				cands = append(cands, cand{path: path, score: sc})
			}
		}
		return nil
	})

	if len(cands) == 0 {
		return "", bestIcon, fmt.Errorf("no Linux ELF executable found inside archive")
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].score > cands[j].score
	})
	return cands[0].path, bestIcon, nil
}

func generateFallbackIcon(destPNG string, label string) {
	pngBytes := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x78, 0x9c, 0x63, 0xf8, 0x4f, 0x95, 0xf1,
		0x1f, 0x00, 0x05, 0x10, 0x02, 0x55, 0x18, 0x67,
		0x8a, 0x09, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
	_ = label
	_ = os.WriteFile(destPNG, pngBytes, 0o644)
}

func stagePackageFromFile(stagedFile, origFilename, appName, repo, tag, desc string) (string, error) {
	slug := slugify(appName)
	stageDir, err := os.MkdirTemp("", "ahhhh-stage-*")
	if err != nil {
		return "", err
	}

	kind := classifyAssetKind(origFilename)
	if kind == "binary" {
		if strings.HasSuffix(strings.ToLower(origFilename), ".appimage") {
			kind = "appimage"
		}
	}

	var execRelPath string
	stagedIcon := filepath.Join(stageDir, slug+".png")
	hasIcon := false

	switch kind {
	case "appimage":
		destBin := filepath.Join(stageDir, slug+".AppImage")
		data, err := os.ReadFile(stagedFile)
		if err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		if err := os.WriteFile(destBin, data, 0o755); err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		execRelPath = filepath.Join(appDir, slug+".AppImage")

		extractTmp, err := os.MkdirTemp("", "ahhhh-ai-ext-*")
		if err == nil {
			cmd := exec.Command(destBin, "--appimage-extract")
			cmd.Dir = extractTmp
			if err := cmd.Run(); err == nil {
				squash := filepath.Join(extractTmp, "squashfs-root")
				_ = filepath.Walk(squash, func(p string, info os.FileInfo, e error) error {
					if e != nil || info == nil || info.IsDir() || hasIcon {
						return nil
					}
					if strings.HasSuffix(strings.ToLower(info.Name()), ".png") {
						if b, rerr := os.ReadFile(p); rerr == nil && len(b) > 0 {
							_ = os.WriteFile(stagedIcon, b, 0o644)
							hasIcon = true
						}
					}
					return nil
				})
			}
			os.RemoveAll(extractTmp)
		}

	case "archive":
		bundleStage := filepath.Join(stageDir, "bundle")
		if err := extractArchive(stagedFile, origFilename, bundleStage); err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		binInTree, iconInTree, err := findPrimaryBinaryInTree(bundleStage, slug)
		if err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		_ = os.Chmod(binInTree, 0o755)
		relBin, err := filepath.Rel(bundleStage, binInTree)
		if err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		execRelPath = filepath.Join(appDir, slug, relBin)
		if iconInTree != "" {
			if b, err := os.ReadFile(iconInTree); err == nil && len(b) > 0 {
				_ = os.WriteFile(stagedIcon, b, 0o644)
				hasIcon = true
			}
		}

	case "binary":
		if !isELFFile(stagedFile) {
			os.RemoveAll(stageDir)
			return "", fmt.Errorf("downloaded file %q is not a valid Linux ELF binary or archive", origFilename)
		}
		destBin := filepath.Join(stageDir, slug)
		data, err := os.ReadFile(stagedFile)
		if err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		if err := os.WriteFile(destBin, data, 0o755); err != nil {
			os.RemoveAll(stageDir)
			return "", err
		}
		execRelPath = filepath.Join(appDir, slug)
	}

	if !hasIcon {
		generateFallbackIcon(stagedIcon, appName)
	}

	if repo == "" {
		if existing, err := readAppMeta(slug); err == nil && existing != nil && existing.GitHubRepo != "" {
			repo = existing.GitHubRepo
		}
	}

	// Keep display name consistent for ahhhh across AppImage, archive, and CLI/one-liner installs
	if slug == "ahhhh" && (strings.EqualFold(strings.TrimSpace(appName), "ahhhh") || strings.TrimSpace(appName) == "") {
		appName = "Apppp"
	}

	meta := AppMeta{
		Name:        appName,
		Slug:        slug,
		GitHubRepo:  repo,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		Tag:         tag,
		AssetName:   origFilename,
		Kind:        kind,
		ExecPath:    execRelPath,
		Description: desc,
	}
	metaBytes, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		os.RemoveAll(stageDir)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(stageDir, slug+".meta.json"), metaBytes, 0o644); err != nil {
		os.RemoveAll(stageDir)
		return "", err
	}

	desktopContent := fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=%s
Comment=%s
Exec=%s
Icon=%s
Terminal=false
Categories=Utility;
`, appName, strings.ReplaceAll(desc, "\n", " "), filepath.Join(binDir, slug), slug)

	if err := os.WriteFile(filepath.Join(stageDir, slug+".desktop"), []byte(desktopContent), 0o644); err != nil {
		os.RemoveAll(stageDir)
		return "", err
	}

	return stageDir, nil
}

func commitStagedDirs(stageDirs []string) error {
	if len(stageDirs) == 0 {
		return nil
	}
	defer func() {
		for _, d := range stageDirs {
			_ = os.RemoveAll(d)
		}
	}()

	var script strings.Builder
	script.WriteString("set -e\n")
	script.WriteString(fmt.Sprintf("mkdir -p %q %q %q %q\n", appDir, binDir, desktopDir, iconDir))

	for _, sd := range stageDirs {
		metaFiles, err := filepath.Glob(filepath.Join(sd, "*.meta.json"))
		if err != nil || len(metaFiles) == 0 {
			return fmt.Errorf("missing staged metadata in %s", sd)
		}
		raw, err := os.ReadFile(metaFiles[0])
		if err != nil {
			return err
		}
		var meta AppMeta
		if err := json.Unmarshal(raw, &meta); err != nil {
			return err
		}
		slug := meta.Slug

		script.WriteString(fmt.Sprintf("rm -rf %q %q\n",
			filepath.Join(appDir, slug),
			filepath.Join(appDir, slug+".AppImage"),
		))

		switch meta.Kind {
		case "appimage":
			script.WriteString(fmt.Sprintf("install -m 0755 %q %q\n",
				filepath.Join(sd, slug+".AppImage"),
				filepath.Join(appDir, slug+".AppImage"),
			))
		case "archive":
			script.WriteString(fmt.Sprintf("cp -a %q %q\n",
				filepath.Join(sd, "bundle"),
				filepath.Join(appDir, slug),
			))
			script.WriteString(fmt.Sprintf("chmod 0755 %q\n", meta.ExecPath))
		case "binary":
			script.WriteString(fmt.Sprintf("install -m 0755 %q %q\n",
				filepath.Join(sd, slug),
				filepath.Join(appDir, slug),
			))
		}

		script.WriteString(fmt.Sprintf("ln -sf %q %q\n", meta.ExecPath, filepath.Join(binDir, slug)))
		script.WriteString(fmt.Sprintf("install -m 0644 %q %q\n",
			filepath.Join(sd, slug+".meta.json"),
			filepath.Join(appDir, slug+".meta.json"),
		))
		script.WriteString(fmt.Sprintf("install -m 0644 %q %q\n",
			filepath.Join(sd, slug+".desktop"),
			filepath.Join(desktopDir, slug+".desktop"),
		))
		script.WriteString(fmt.Sprintf("install -m 0644 %q %q\n",
			filepath.Join(sd, slug+".png"),
			filepath.Join(iconDir, slug+".png"),
		))
	}

	script.WriteString("command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -f -t /usr/share/icons/hicolor || true\n")
	script.WriteString("command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database /usr/share/applications || true\n")

	cmd := exec.Command("pkexec", "sh", "-c", script.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("admin install failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func installFromURL(appName, rawURL string) error {
	stageDir, err := prepareStagedInstallFromURL(appName, rawURL)
	if err != nil {
		return err
	}
	return commitStagedDirs([]string{stageDir})
}

func prepareStagedInstallFromURL(appName, rawURL string) (string, error) {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return "", fmt.Errorf("missing download URL")
	}

	repo := parseGitHubRepo(u)
	lowerU := strings.ToLower(u)
	isDirectAsset := strings.HasSuffix(lowerU, ".appimage") ||
		strings.HasSuffix(lowerU, ".tar.gz") ||
		strings.HasSuffix(lowerU, ".tgz") ||
		strings.HasSuffix(lowerU, ".tar.xz") ||
		strings.HasSuffix(lowerU, ".zip") ||
		strings.Contains(lowerU, "/releases/download/")

	var dlURL, assetName, tag, desc string
	if repo != "" && !isDirectAsset {
		resolved, err := resolveGitHubTarget(repo, appName)
		if err != nil {
			return "", err
		}
		if appName == "" {
			appName = resolved.Name
		}
		dlURL = resolved.DownloadURL
		assetName = resolved.AssetName
		tag = resolved.Tag
		desc = resolved.Description
	} else {
		dlURL = u
		assetName = filepath.Base(strings.Split(u, "?")[0])
		if appName == "" {
			appName = deriveAppNameFromFilename(assetName)
		}
	}

	tmpFile, err := os.CreateTemp("", "ahhhh-dl-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	if err := downloadFile(dlURL, tmpPath); err != nil {
		return "", err
	}

	return stagePackageFromFile(tmpPath, assetName, appName, repo, tag, desc)
}

func deriveAppNameFromFilename(origFilename string) string {
	name := strings.TrimSpace(origFilename)
	lower := strings.ToLower(name)

	exts := []string{
		".appimage", ".tar.gz", ".tgz", ".tar.xz", ".txz", ".zip", ".bin", ".run",
	}
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}

	archRe := regexp.MustCompile(`(?i)[._-](x86_64|amd64|x64|aarch64|arm64|armv7l?|armhf|i386|i686|386|musl|gnu|glibc|linux|appimage)$`)
	for {
		next := archRe.ReplaceAllString(name, "")
		if next == name || strings.TrimSpace(next) == "" {
			break
		}
		name = next
	}

	verRe := regexp.MustCompile(`(?i)[._-]v?\d+(?:\.\d+)+(?:[-._]?(?:rc|alpha|beta|dev|nightly|snapshot|b)[._-]?\d*)?$`)
	for {
		next := verRe.ReplaceAllString(name, "")
		if next == name || strings.TrimSpace(next) == "" {
			break
		}
		name = next
	}

	buildRe := regexp.MustCompile(`(?i)[._-]v?\d+$`)
	if next := buildRe.ReplaceAllString(name, ""); next != name && strings.TrimSpace(next) != "" {
		name = next
	}

	name = strings.Trim(name, "._- ")
	if name == "" {
		return "app"
	}
	return name
}

func installLocalFile(tmpPath, origFilename, customName string) error {
	appName := strings.TrimSpace(customName)
	if appName == "" {
		appName = deriveAppNameFromFilename(origFilename)
	}
	stageDir, err := stagePackageFromFile(tmpPath, origFilename, appName, "", "", "")
	if err != nil {
		return err
	}
	return commitStagedDirs([]string{stageDir})
}

func removeInstalledApp(appName string) error {
	slug := slugify(appName)
	script := fmt.Sprintf(`set -e
rm -rf %q %q %q %q %q %q
command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -f -t /usr/share/icons/hicolor || true
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database /usr/share/applications || true
`,
		filepath.Join(appDir, slug),
		filepath.Join(appDir, slug+".AppImage"),
		filepath.Join(appDir, slug+".meta.json"),
		filepath.Join(binDir, slug),
		filepath.Join(desktopDir, slug+".desktop"),
		filepath.Join(iconDir, slug+".png"),
	)
	cmd := exec.Command("pkexec", "sh", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove failed: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readAppMeta(slug string) (*AppMeta, error) {
	raw, err := os.ReadFile(filepath.Join(appDir, slug+".meta.json"))
	if err != nil {
		return nil, err
	}
	var m AppMeta
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

func listInstalledMeta() map[string]AppMeta {
	out := make(map[string]AppMeta)
	files, err := filepath.Glob(filepath.Join(appDir, "*.meta.json"))
	if err != nil {
		return out
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var m AppMeta
		if err := json.Unmarshal(raw, &m); err == nil && m.Slug != "" {
			out[m.Slug] = m
		}
	}
	return out
}