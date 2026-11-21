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
	releaseMu    sync.RWMutex
	latestTags   = make(map[string]string)
)

func loadCatalog() error {
	client := &http.Client{Timeout: 25 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog HTTP %d", resp.StatusCode)
	}

	var payload FeedPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return err
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
		repo := meta.GitHubRepo
		if repo == "" {
			if c, ok := catBySlug[slug]; ok {
				for _, l := range c.Links {
					if r := parseGitHubRepo(l.URL); r != "" {
						repo = r
						break
					}
				}
			}
		}
		if repo == "" {
			continue
		}

		rel, err := fetchLatestGitHubRelease(repo)
		if err == nil && rel != nil && rel.TagName != "" {
			releaseMu.Lock()
			latestTags[slug] = rel.TagName
			releaseMu.Unlock()
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func buildMergedApps() []CatalogApp {
	installed := listInstalledMeta()

	catalogMu.RLock()
	base := make([]CatalogApp, len(catalogCache))
	copy(base, catalogCache)
	catalogMu.RUnlock()

	releaseMu.RLock()
	tagsSnap := make(map[string]string, len(latestTags))
	for k, v := range latestTags {
		tagsSnap[k] = v
	}
	releaseMu.RUnlock()

	seenSlugs := make(map[string]bool, len(base))
	for i := range base {
		slug := base[i].Slug
		seenSlugs[slug] = true
		if meta, ok := installed[slug]; ok {
			base[i].Installed = true
			base[i].InstalledTag = meta.Tag
			base[i].Kind = meta.Kind
			base[i].AssetName = meta.AssetName
			if meta.GitHubRepo != "" {
				hasGH := false
				for _, l := range base[i].Links {
					if parseGitHubRepo(l.URL) != "" {
						hasGH = true
						break
					}
				}
				if !hasGH {
					base[i].Links = append(base[i].Links, CatalogLink{
						Type: "GitHub",
						URL:  "https://github.com/" + meta.GitHubRepo,
					})
				}
			}
			if lt, ok := tagsSnap[slug]; ok && lt != "" {
				base[i].LatestTag = lt
				if meta.Tag != "" && compareVersionTags(meta.Tag, lt) > 0 {
					base[i].UpdateAvailable = true
				}
			}
		}
	}

	// Append installed apps that aren't in AppImageHub (e.g., direct GitHub Release installs or local files)
	for slug, meta := range installed {
		if seenSlugs[slug] {
			continue
		}
		var links []CatalogLink
		if meta.GitHubRepo != "" {
			links = append(links, CatalogLink{
				Type: "GitHub",
				URL:  "https://github.com/" + meta.GitHubRepo,
			})
		}
		desc := meta.Description
		if desc == "" {
			if meta.GitHubRepo != "" {
				desc = fmt.Sprintf("Installed from GitHub release (%s)", meta.AssetName)
			} else {
				desc = fmt.Sprintf("Installed from local file (%s)", meta.AssetName)
			}
		}
		app := CatalogApp{
			Name:         meta.Name,
			Slug:         slug,
			Description:  desc,
			Categories:   []string{"GitHub Release"},
			Links:        links,
			Installed:    true,
			InstalledTag: meta.Tag,
			Kind:         meta.Kind,
			AssetName:    meta.AssetName,
		}
		if lt, ok := tagsSnap[slug]; ok && lt != "" {
			app.LatestTag = lt
			if meta.Tag != "" && compareVersionTags(meta.Tag, lt) > 0 {
				app.UpdateAvailable = true
			}
		}
		base = append(base, app)
	}

	return base
}

func openBrowser(url string) {
	_ = exec.Command("xdg-open", url).Start()
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "remove":
			if len(os.Args) < 3 {
				fmt.Fprintln(os.Stderr, "Usage: ahhhh remove <app-name>")
				os.Exit(1)
			}
			if err := removeInstalledApp(os.Args[2]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			fmt.Printf("Removed %s.\n", os.Args[2])
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
		repo := parseGitHubRepo(r.URL.Query().Get("github"))
		if repo == "" {
			http.Error(w, "invalid github repo", http.StatusBadRequest)
			return
		}
		info, _ := fetchGitHubRepoInfo(repo)
		rel, _ := fetchLatestGitHubRelease(repo)
		out := map[string]any{}
		if info != nil {
			out["description"] = info.Description
			out["topics"] = info.Topics
			out["stars"] = info.Stars
		}
		if rel != nil {
			out["release_tag"] = rel.TagName
			out["release_notes"] = rel.Body
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})

	mux.HandleFunc("/api/install", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/update-all", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
				http.Error(w, fmt.Sprintf("%s: %v", it.Name, err), http.StatusInternalServerError)
				return
			}
			stages = append(stages, sd)
		}
		if err := commitStagedDirs(stages); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/remove", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := removeInstalledApp(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/api/upload-local", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(1024 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		defer file.Close()

		tmp, err := os.CreateTemp("", "ahhhh-upload-*"+filepath.Ext(header.Filename))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		tmpPath := tmp.Name()
		_, copyErr := io.Copy(tmp, file)
		tmp.Close()
		defer os.Remove(tmpPath)
		if copyErr != nil {
			http.Error(w, copyErr.Error(), http.StatusInternalServerError)
			return
		}

		customName := r.FormValue("name")
		if err := installLocalFile(tmpPath, header.Filename, customName); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("ahhhh running at http://%s", listenAddr)
	go func() {
		time.Sleep(250 * time.Millisecond)
		openBrowser("http://" + listenAddr)
	}()
	log.Fatal(http.Serve(ln, mux))
}