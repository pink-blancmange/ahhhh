package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	optDir      = "/opt/appimages"
	binDir      = "/usr/local/bin"
	desktopDir  = "/usr/local/share/applications"
	iconBaseDir = "/usr/local/share/icons/hicolor"
)

type InstallMeta struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Source      string `json:"source"`
	GitHubRepo  string `json:"github_repo,omitempty"`
	ReleaseTag  string `json:"release_tag,omitempty"`
	InstalledAt string `json:"installed_at"`
}

func slugify(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	return strings.ToLower(strings.Trim(re.ReplaceAllString(name, "-"), "-"))
}

func readMeta(slug string) (*InstallMeta, bool) {
	data, err := os.ReadFile(filepath.Join(optDir, slug, "meta.json"))
	if err != nil {
		return nil, false
	}
	var m InstallMeta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return &m, true
}

func listInstalledSlugs() []string {
	entries, err := os.ReadDir(optDir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			binPath := filepath.Join(optDir, e.Name(), e.Name()+".AppImage")
			if _, err := os.Stat(binPath); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

func resolveGitHubRelease(repoOrURL string) (downloadURL, cleanRepo, tag string, err error) {
	trimmed := strings.TrimSpace(repoOrURL)
	if strings.HasSuffix(strings.ToLower(trimmed), ".appimage") {
		return trimmed, "", "", nil
	}

	repo := strings.Trim(strings.TrimPrefix(trimmed, "https://github.com/"), "/")
	if repo == "" {
		return "", "", "", fmt.Errorf("This app doesn't have a download link we can use automatically.")
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return "", "", "", fmt.Errorf("Couldn't reach GitHub to check for a download.")
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", "", "", fmt.Errorf("GitHub didn't have a release ready for this app (status %d).", resp.StatusCode)
	}

	var rel struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", "", "", fmt.Errorf("Couldn't read the release info from GitHub.")
	}

	arch := "x86_64"
	if runtime.GOARCH == "arm64" {
		arch = "aarch64"
	}

	var fallback string
	for _, a := range rel.Assets {
		lower := strings.ToLower(a.Name)
		if strings.HasSuffix(lower, ".appimage") {
			if strings.Contains(lower, arch) || strings.Contains(lower, "amd64") {
				return a.BrowserDownloadURL, repo, rel.TagName, nil
			}
			if fallback == "" {
				fallback = a.BrowserDownloadURL
			}
		}
	}
	if fallback != "" {
		return fallback, repo, rel.TagName, nil
	}
	return "", "", "", fmt.Errorf("This app doesn't publish an AppImage file in its latest release.")
}

func extractCategories(squashRoot string) string {
	matches, _ := filepath.Glob(filepath.Join(squashRoot, "*.desktop"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/applications/*.desktop"))
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Categories=") {
				cats := strings.TrimSpace(strings.TrimPrefix(trimmed, "Categories="))
				if cats != "" {
					return cats
				}
			}
		}
	}
	return "Utility;"
}

func extractDisplayName(squashRoot, fallback string) string {
	matches, _ := filepath.Glob(filepath.Join(squashRoot, "*.desktop"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/applications/*.desktop"))
	}
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Name=") {
				name := strings.TrimSpace(strings.TrimPrefix(trimmed, "Name="))
				if name != "" {
					return name
				}
			}
		}
	}
	return fallback
}

func installIcon(squashRoot, slug string) string {
	iconName := "ahhhh-" + slug

	svgs, _ := filepath.Glob(filepath.Join(squashRoot, "*.svg"))
	if len(svgs) == 0 {
		svgs, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/icons/hicolor/scalable/apps/*.svg"))
	}
	if len(svgs) > 0 {
		destDir := filepath.Join(iconBaseDir, "scalable/apps")
		_ = os.MkdirAll(destDir, 0755)
		dest := filepath.Join(destDir, iconName+".svg")
		if err := copyFileSimple(svgs[0], dest, 0644); err == nil {
			return iconName
		}
	}

	pngs, _ := filepath.Glob(filepath.Join(squashRoot, "*.png"))
	if len(pngs) == 0 {
		pngs, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/icons/hicolor/256x256/apps/*.png"))
	}
	if len(pngs) > 0 {
		destDir := filepath.Join(iconBaseDir, "256x256/apps")
		_ = os.MkdirAll(destDir, 0755)
		dest := filepath.Join(destDir, iconName+".png")
		if err := copyFileSimple(pngs[0], dest, 0644); err == nil {
			return iconName
		}
	}

	return ""
}

func installAppImageSystemWide(rawName, source, githubRepo, releaseTag string) error {
	tmpDir, err := os.MkdirTemp("", "ahhhh-dl-*")
	if err != nil {
		return fmt.Errorf("Couldn't create a temporary working folder.")
	}
	defer os.RemoveAll(tmpDir)

	isHTTP := strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://")
	tmpFile := filepath.Join(tmpDir, "payload.AppImage")

	if isHTTP {
		client := &http.Client{Timeout: 3 * time.Minute}
		resp, err := client.Get(source)
		if err != nil {
			return fmt.Errorf("Download failed while connecting.")
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return fmt.Errorf("Couldn't download the AppImage file (server returned %d).", resp.StatusCode)
		}
		out, err := os.Create(tmpFile)
		if err != nil {
			return fmt.Errorf("Couldn't save the downloaded file.")
		}
		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			return fmt.Errorf("Download got cut off before finishing.")
		}
		out.Close()
	} else {
		if err := copyFileSimple(source, tmpFile, 0755); err != nil {
			return fmt.Errorf("Couldn't read the local AppImage file.")
		}
	}

	if err := os.Chmod(tmpFile, 0755); err != nil {
		return fmt.Errorf("Couldn't make the app executable.")
	}

	extractCmd := exec.Command(tmpFile, "--appimage-extract")
	extractCmd.Dir = tmpDir
	_ = extractCmd.Run()
	squashRoot := filepath.Join(tmpDir, "squashfs-root")

	displayName := strings.TrimSpace(rawName)
	if displayName == "" {
		base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
		displayName = extractDisplayName(squashRoot, base)
	}

	slug := slugify(displayName)
	if slug == "" {
		return fmt.Errorf("Invalid app name.")
	}

	appDir := filepath.Join(optDir, slug)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return fmt.Errorf("Couldn't create /opt/appimages/%s.", slug)
	}

	destBin := filepath.Join(appDir, slug+".AppImage")
	if err := copyFileSimple(tmpFile, destBin, 0755); err != nil {
		_ = os.RemoveAll(appDir)
		return fmt.Errorf("Couldn't move the app into /opt/appimages.")
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		_ = os.RemoveAll(appDir)
		return fmt.Errorf("Couldn't create /usr/local/bin.")
	}

	symlink := filepath.Join(binDir, slug)
	_ = os.Remove(symlink)
	if err := os.Symlink(destBin, symlink); err != nil {
		_ = os.RemoveAll(appDir)
		return fmt.Errorf("Couldn't add a command shortcut in /usr/local/bin.")
	}

	iconName := installIcon(squashRoot, slug)
	categories := extractCategories(squashRoot)

	if err := os.MkdirAll(desktopDir, 0755); err != nil {
		return fmt.Errorf("Couldn't create the app menu folder.")
	}

	iconLine := ""
	if iconName != "" {
		iconLine = fmt.Sprintf("Icon=%s\n", iconName)
	}

	desktopEntry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\nExec=%s %%U\nTryExec=%s\n%sCategories=%s\nTerminal=false\n", displayName, symlink, symlink, iconLine, categories)
	desktopPath := filepath.Join(desktopDir, "ahhhh-"+slug+".desktop")
	if err := os.WriteFile(desktopPath, []byte(desktopEntry), 0644); err != nil {
		return fmt.Errorf("Couldn't add the app to your desktop menu.")
	}

	meta := InstallMeta{
		Name:        displayName,
		Slug:        slug,
		Source:      source,
		GitHubRepo:  githubRepo,
		ReleaseTag:  releaseTag,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if metaBytes, err := json.MarshalIndent(meta, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(appDir, "meta.json"), metaBytes, 0644)
	}

	_ = exec.Command("update-desktop-database", desktopDir).Run()
	_ = exec.Command("gtk-update-icon-cache", "-f", "-t", iconBaseDir).Run()
	return nil
}

func removeAppImageSystemWide(rawName string) error {
	slug := slugify(rawName)
	if slug == "" {
		return fmt.Errorf("Invalid app name.")
	}

	_ = os.RemoveAll(filepath.Join(optDir, slug))
	_ = os.Remove(filepath.Join(binDir, slug))
	_ = os.Remove(filepath.Join(desktopDir, "ahhhh-"+slug+".desktop"))
	_ = os.Remove(filepath.Join(iconBaseDir, "scalable/apps", "ahhhh-"+slug+".svg"))
	_ = os.Remove(filepath.Join(iconBaseDir, "256x256/apps", "ahhhh-"+slug+".png"))

	_ = exec.Command("update-desktop-database", desktopDir).Run()
	_ = exec.Command("gtk-update-icon-cache", "-f", "-t", iconBaseDir).Run()
	return nil
}

func copyFileSimple(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}