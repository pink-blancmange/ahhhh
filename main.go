package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed web/*
var webFS embed.FS

const (
	listenAddr = "127.0.0.1:8745"
	feedURL    = "https://appimage.github.io/feed.json"
)

type CatalogLink struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type CatalogApp struct {
	Name            string        `json:"name"`
	Slug            string        `json:"slug"`
	Description     string        `json:"description"`
	Categories      []string      `json:"categories"`
	Links           []CatalogLink `json:"links"`
	Installed       bool          `json:"installed"`
	InstalledTag    string        `json:"installed_tag,omitempty"`
	LatestTag       string        `json:"latest_tag,omitempty"`
	UpdateAvailable bool          `json:"update_available"`
	Kind            string        `json:"kind,omitempty"`
	AssetName       string        `json:"asset_name,omitempty"`
}

type FeedPayload struct {
	Items []struct {
		Name        string        `json:"name"`
		Description string        `json:"description"`
		Categories  []string      `json:"categories"`
		Links       []CatalogLink `json:"links"`
	} `json:"items"`
}

var (
	catalogMu    sync.RWMutex
	catalogCache []CatalogApp
)

func openBrowser(url string) {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("xdg-open"); err == nil {
		cmd = exec.Command("xdg-open", url)
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

func loadCatalog() error {
	var payload FeedPayload
	loadedFromLive := false

	client := &http.Client{Timeout: 8 * time.Second}
	if resp, err := client.Get(feedURL); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			if decErr := json.NewDecoder(resp.Body).Decode(&payload); decErr == nil && len(payload.Items) > 0 {
				loadedFromLive = true
			}
		}
	}

	if !loadedFromLive {
		raw, err := webFS.ReadFile("web/fallback-feed.json")
		if err != nil {
			return fmt.Errorf("live feed unavailable and fallback read failed: %w", err)
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return fmt.Errorf("fallback feed decode failed: %w", err)
		}
	}

	items := make([]CatalogApp, 0, len(payload.Items))
	for _, it := range payload.Items {
		if strings.TrimSpace(it.Name) == "" {
			continue
		}
		items = append(items, CatalogApp{
			Name:        it.Name,
			Slug:        slugify(it.Name),
			Description: it.Description,
			Categories:  it.Categories,
			Links:       it.Links,
		})
	}

	catalogMu.Lock()
	catalogCache = items
	catalogMu.Unlock()
	return nil
}

func checkUpdatesForInstalled() {
	installed := listInstalledMeta()
	if len(installed) == 0 {
		return
	}

	catalogMu.RLock()
	catBySlug := make(map[string]CatalogApp, len(catalogCache))
	for _, c := range catalogCache {
		catBySlug[c.Slug] = c
	}
	catalogMu.RUnlock()

	for slug, meta := range installed {
		if meta.GitHubRepo == "" {
			continue
		}
		target, err := resolveGitHubTarget(meta.GitHubRepo, meta.Name)
		if err != nil || target == nil {
			continue
		}
		if compareVersionTags(meta.Tag, target.Tag) > 0 {
			catalogMu.Lock()
			for i := range catalogCache {
				if catalogCache[i].Slug == slug {
					catalogCache[i].LatestTag = target.Tag
					catalogCache[i].UpdateAvailable = true
				}
			}
			catalogMu.Unlock()
		}
	}
}

func buildMergedApps() []CatalogApp {
	installed := listInstalledMeta()

	catalogMu.RLock()
	out := make([]CatalogApp, 0, len(catalogCache)+len(installed))
	seen := make(map[string]bool, len(catalogCache))

	for _, c := range catalogCache {
		item := c
		if m, ok := installed[item.Slug]; ok {
			item.Installed = true
			item.InstalledTag = m.Tag
			item.Kind = m.Kind
			item.AssetName = m.AssetName
		}
		out = append(out, item)
		seen[item.Slug] = true
	}
	catalogMu.RUnlock()

	for slug, m := range installed {
		if seen[slug] {
			continue
		}
		out = append(out, CatalogApp{
			Name:         m.Name,
			Slug:         m.Slug,
			Description:  m.Description,
			Categories:   []string{"Installed"},
			Installed:    true,
			InstalledTag: m.Tag,
			Kind:         m.Kind,
			AssetName:    m.AssetName,
		})
	}

	return out
}

func lookupCatalogCategories(slug, repo string) []string {
	targetSlug := strings.ToLower(strings.TrimSpace(slug))
	targetRepo := strings.ToLower(strings.TrimSpace(repo))

	catalogMu.RLock()
	defer catalogMu.RUnlock()

	for _, item := range catalogCache {
		if targetSlug != "" && strings.ToLower(item.Slug) == targetSlug && len(item.Categories) > 0 {
			return append([]string(nil), item.Categories...)
		}
		if targetRepo != "" {
			for _, lnk := range item.Links {
				if strings.Contains(strings.ToLower(lnk.URL), targetRepo) && len(item.Categories) > 0 {
					return append([]string(nil), item.Categories...)
				}
			}
		}
	}
	return nil
}

var appVersion = "dev"

func resolveSelfTag(baseName string) string {
	tag := strings.TrimSpace(appVersion)
	if tag == "" || tag == "dev" {
		parts := strings.Split(baseName, "_")
		if len(parts) >= 2 && len(parts[1]) > 0 && parts[1][0] >= '0' && parts[1][0] <= '9' {
			tag = "v" + parts[1]
		} else {
			return ""
		}
	} else if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	return tag
}

func runSelfInstall() error {
	srcPath := strings.TrimSpace(os.Getenv("APPIMAGE"))
	if srcPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		srcPath = exe
	}
	baseName := filepath.Base(srcPath)
	if !strings.HasSuffix(strings.ToLower(baseName), ".appimage") {
		baseName = "ahhhh.AppImage"
	}
	return installSelfAppImage(srcPath, baseName, "pink-blancmange/ahhhh", resolveSelfTag(baseName))
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--self-install", "self-install":
			if err := runSelfInstall(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("Installed successfully.")
			return
		case "install":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: ahhhh install <owner/repo-or-url> [name]")
				os.Exit(1)
			}
			name := ""
			if len(os.Args) >= 4 {
				name = os.Args[3]
			}
			if err := installFromURL(name, os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Println("Installed successfully.")
			return
		}
	}

	// If already running on 8745, just open the browser tab and exit cleanly
	if conn, err := net.DialTimeout("tcp", listenAddr, 300*time.Millisecond); err == nil {
		conn.Close()
		openBrowser("http://" + listenAddr)
		return
	}

	if err := loadCatalog(); err != nil {
		log.Printf("warning: initial catalog load failed: %v", err)
	}

	go func() {
		checkUpdatesForInstalled()
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			checkUpdatesForInstalled()
		}
	}()

	mux := http.NewServeMux()

	webSub, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("/", http.FileServer(http.FS(webSub)))

	mux.HandleFunc("/api/apps", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("check_updates") == "1" {
			checkUpdatesForInstalled()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(buildMergedApps())
	})

	mux.HandleFunc("/api/github-resolve", func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repo")
		name := r.URL.Query().Get("name")
		res, err := resolveGitHubTarget(repo, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(res)
	})

	mux.HandleFunc("/api/app-details", func(w http.ResponseWriter, r *http.Request) {
		slug := strings.TrimSpace(r.URL.Query().Get("slug"))
		if slug == "" {
			http.Error(w, "missing slug", http.StatusBadRequest)
			return
		}
		meta, err := readAppMeta(slug)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(meta)
	})

	handleInstallURL := func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := installFromURL(req.Name, req.URL); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}

	mux.HandleFunc("/api/install", handleInstallURL)
	mux.HandleFunc("/api/install-url", handleInstallURL)

	mux.HandleFunc("/api/install-local", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(1024 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "missing file", http.StatusBadRequest)
			return
		}
		defer file.Close()

		tmp, err := os.CreateTemp("", "ahhhh-local-*")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)

		if _, err := io.Copy(tmp, file); err != nil {
			tmp.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tmp.Close()

		customName := strings.TrimSpace(r.FormValue("name"))
		if err := installLocalFile(tmpPath, filepath.Base(header.Filename), customName); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
			Slug string `json:"slug"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		target := strings.TrimSpace(req.Slug)
		if target == "" {
			target = strings.TrimSpace(req.Name)
		}
		if err := removeInstalledApp(target); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	mux.HandleFunc("/api/update-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Items []struct {
				Name string `json:"name"`
				URL  string `json:"url"`
			} `json:"items"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var stages []string
		for _, it := range req.Items {
			sd, err := prepareStagedInstallFromURL(it.Name, it.URL)
			if err != nil {
				for _, prev := range stages {
					_ = os.RemoveAll(prev)
				}
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			stages = append(stages, sd)
		}
		if err := commitStagedDirs(stages); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})

	url := "http://" + listenAddr
	log.Printf("Ahhhh listening on %s", url)
	go openBrowser(url)

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}