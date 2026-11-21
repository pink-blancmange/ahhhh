package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	optDir      = "/opt/appimages"
	binDir      = "/usr/local/bin"
	desktopDir  = "/usr/local/share/applications"
	iconBaseDir = "/usr/local/share/icons/hicolor"
	selfRepo    = "pink-blancmange/ahhhh"
)

type InstallMeta struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Source      string `json:"source"`
	GitHubRepo  string `json:"github_repo,omitempty"`
	ReleaseTag  string `json:"release_tag,omitempty"`
	Description string `json:"description,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	InstalledAt string `json:"installed_at"`
}

type appStreamXML struct {
	Summary     string `xml:"summary"`
	Description struct {
		Paragraphs []string `xml:"p"`
		Items      []string `xml:"ul>li"`
	} `xml:"description"`
	URLs []struct {
		Type string `xml:"type,attr"`
		Href string `xml:",chardata"`
	} `xml:"url"`
}

var semverTagRe = regexp.MustCompile(`v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`)

func slugify(name string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
	return strings.ToLower(strings.Trim(re.ReplaceAllString(name, "-"), "-"))
}

func normalizeTag(tag string) string {
	return strings.TrimPrefix(strings.TrimSpace(tag), "v")
}

func tagsEqual(a, b string) bool {
	na, nb := normalizeTag(a), normalizeTag(b)
	return na != "" && nb != "" && na == nb
}

func detectVersionTag(candidates ...string) string {
	for _, c := range candidates {
		if m := semverTagRe.FindString(c); m != "" {
			if !strings.HasPrefix(m, "v") {
				return "v" + m
			}
			return m
		}
	}
	return ""
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

func findDesktopFiles(squashRoot string) []string {
	matches, _ := filepath.Glob(filepath.Join(squashRoot, "*.desktop"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/applications/*.desktop"))
	}
	return matches
}

func extractCategories(squashRoot string) string {
	for _, path := range findDesktopFiles(squashRoot) {
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

func extractStartupWMClass(squashRoot string) string {
	for _, path := range findDesktopFiles(squashRoot) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "StartupWMClass=") {
				wmClass := strings.TrimSpace(strings.TrimPrefix(trimmed, "StartupWMClass="))
				if wmClass != "" {
					return wmClass
				}
			}
		}
	}
	return ""
}

func extractDisplayName(squashRoot, fallback string) string {
	for _, path := range findDesktopFiles(squashRoot) {
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

func extractAppDescription(squashRoot string) (desc, homepage string) {
	xmlMatches, _ := filepath.Glob(filepath.Join(squashRoot, "usr/share/metainfo/*.xml"))
	if len(xmlMatches) == 0 {
		xmlMatches, _ = filepath.Glob(filepath.Join(squashRoot, "*.appdata.xml"))
	}
	for _, path := range xmlMatches {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var doc appStreamXML
		if err := xml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		var parts []string
		for _, p := range doc.Description.Paragraphs {
			if t := strings.TrimSpace(p); t != "" {
				parts = append(parts, t)
			}
		}
		for _, li := range doc.Description.Items {
			if t := strings.TrimSpace(li); t != "" {
				parts = append(parts, "• "+t)
			}
		}
		for _, u := range doc.URLs {
			if u.Type == "homepage" && strings.TrimSpace(u.Href) != "" {
				homepage = strings.TrimSpace(u.Href)
				break
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n"), homepage
		}
		if s := strings.TrimSpace(doc.Summary); s != "" {
			return s, homepage
		}
	}

	for _, path := range findDesktopFiles(squashRoot) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Comment=") {
				if c := strings.TrimSpace(strings.TrimPrefix(trimmed, "Comment=")); c != "" {
					return c, homepage
				}
			}
		}
	}

	return "", homepage
}

func pickLargestFile(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	type candidate struct {
		path string
		size int64
	}
	var list []candidate
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			list = append(list, candidate{path: p, size: info.Size()})
		}
	}
	if len(list) == 0 {
		return ""
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].size > list[j].size
	})
	return list[0].path
}

func installIcon(squashRoot, slug, source string) string {
	iconName := "ahhhh-" + slug

	svgs, _ := filepath.Glob(filepath.Join(squashRoot, "*.svg"))
	if len(svgs) == 0 {
		svgs, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/icons/hicolor/scalable/apps/*.svg"))
	}
	if bestSVG := pickLargestFile(svgs); bestSVG != "" {
		destDir := filepath.Join(iconBaseDir, "scalable/apps")
		_ = os.MkdirAll(destDir, 0755)
		dest := filepath.Join(destDir, iconName+".svg")
		if err := copyFileSimple(bestSVG, dest, 0644); err == nil {
			return iconName
		}
	}

	pngs, _ := filepath.Glob(filepath.Join(squashRoot, "*.png"))
	if len(pngs) == 0 {
		pngs, _ = filepath.Glob(filepath.Join(squashRoot, "usr/share/icons/hicolor/256x256/apps/*.png"))
	}
	if len(pngs) == 0 {
		for _, size := range []string{"512x512", "128x128", "64x64", "48x48"} {
			sized, _ := filepath.Glob(filepath.Join(squashRoot, "usr/share/icons/hicolor", size, "apps/*.png"))
			if len(sized) > 0 {
				pngs = append(pngs, sized...)
				break
			}
		}
	}
	if len(pngs) == 0 {
		dirIcon := filepath.Join(squashRoot, ".DirIcon")
		if info, err := os.Stat(dirIcon); err == nil && !info.IsDir() {
			pngs = append(pngs, dirIcon)
		}
	}
	if len(pngs) == 0 && slug == "ahhhh" {
		candidates := []string{
			filepath.Join(squashRoot, "packaging/ahhhh.png"),
			"packaging/ahhhh.png",
		}
		if source != "" && !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
			sourceDir := filepath.Dir(source)
			candidates = append(candidates,
				filepath.Join(sourceDir, "packaging/ahhhh.png"),
				filepath.Join(filepath.Dir(sourceDir), "packaging/ahhhh.png"),
			)
		}
		if exe, err := os.Executable(); err == nil {
			exeDir := filepath.Dir(exe)
			candidates = append(candidates,
				filepath.Join(exeDir, "packaging/ahhhh.png"),
				filepath.Join(filepath.Dir(exeDir), "packaging/ahhhh.png"),
			)
		}
		for _, cand := range candidates {
			if info, err := os.Stat(cand); err == nil && !info.IsDir() {
				pngs = append(pngs, cand)
				break
			}
		}
	}
	if bestPNG := pickLargestFile(pngs); bestPNG != "" {
		destDir := filepath.Join(iconBaseDir, "256x256/apps")
		_ = os.MkdirAll(destDir, 0755)
		dest := filepath.Join(destDir, iconName+".png")
		if err := copyFileSimple(bestPNG, dest, 0644); err == nil {
			return iconName
		}
	}

	return ""
}

func filteredChildEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "APPIMAGE=") ||
			strings.HasPrefix(kv, "APPDIR=") ||
			strings.HasPrefix(kv, "ARGV0=") ||
			strings.HasPrefix(kv, "OWD=") {
			continue
		}
		env = append(env, kv)
	}
	return env
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

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	extractCmd := exec.CommandContext(ctx, tmpFile, "--appimage-extract")
	extractCmd.Dir = tmpDir
	extractCmd.Env = filteredChildEnv()
	_ = extractCmd.Run()
	squashRoot := filepath.Join(tmpDir, "squashfs-root")

	baseSource := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	displayName := strings.TrimSpace(rawName)
	if displayName == "" {
		displayName = extractDisplayName(squashRoot, baseSource)
	}

	slug := slugify(displayName)
	if slug == "" {
		return fmt.Errorf("Invalid app name.")
	}

	if slug == "ahhhh" || strings.HasPrefix(slug, "ahhhh-") || strings.HasPrefix(slug, "ahhhh_") {
		slug = "ahhhh"
		displayName = "ahhhh"
		if githubRepo == "" {
			githubRepo = selfRepo
		}
		if releaseTag == "" || releaseTag == "local" {
			if detected := detectVersionTag(rawName, baseSource); detected != "" {
				releaseTag = detected
			}
		}
	}

	appDir := filepath.Join(optDir, slug)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return fmt.Errorf("Couldn't create /opt/appimages/%s.", slug)
	}

	destBin := filepath.Join(appDir, slug+".AppImage")
	stageBin := destBin + ".new"
	if err := copyFileSimple(tmpFile, stageBin, 0755); err != nil {
		_ = os.Remove(stageBin)
		return fmt.Errorf("Couldn't stage the app in /opt/appimages.")
	}
	if err := os.Rename(stageBin, destBin); err != nil {
		_ = os.Remove(stageBin)
		return fmt.Errorf("Couldn't move the app into /opt/appimages.")
	}

	if err := os.MkdirAll(binDir, 0755); err != nil {
		return fmt.Errorf("Couldn't create /usr/local/bin.")
	}

	symlink := filepath.Join(binDir, slug)
	_ = os.Remove(symlink)
	if err := os.Symlink(destBin, symlink); err != nil {
		return fmt.Errorf("Couldn't add a command shortcut in /usr/local/bin.")
	}

	iconName := installIcon(squashRoot, slug, source)
	categories := extractCategories(squashRoot)
	if slug == "ahhhh" && (categories == "" || categories == "Utility;") {
		categories = "System;Utility;"
	}
	wmClass := extractStartupWMClass(squashRoot)
	description, homepage := extractAppDescription(squashRoot)
	if slug == "ahhhh" && description == "" {
		description = "Simple Linux AppImage Installer"
	}

	if err := os.MkdirAll(desktopDir, 0755); err != nil {
		return fmt.Errorf("Couldn't create the app menu folder.")
	}

	iconLine := ""
	if iconName != "" {
		iconLine = fmt.Sprintf("Icon=%s\n", iconName)
	}

	wmClassLine := ""
	if wmClass != "" {
		wmClassLine = fmt.Sprintf("StartupWMClass=%s\n", wmClass)
	}

	commentLine := ""
	if singleLine := strings.TrimSpace(strings.Split(description, "\n")[0]); singleLine != "" {
		commentLine = fmt.Sprintf("Comment=%s\n", singleLine)
	}

	desktopEntry := fmt.Sprintf("[Desktop Entry]\nType=Application\nName=%s\n%sExec=%s %%U\nTryExec=%s\n%s%sCategories=%s\nTerminal=false\n", displayName, commentLine, symlink, symlink, iconLine, wmClassLine, categories)
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
		Description: description,
		Homepage:    homepage,
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