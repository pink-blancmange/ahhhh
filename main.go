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

const backgroundUpdateInterval = 30 * time.Minute

type CatalogFeed struct {
	Items []AppItem `json:"items"`
}

type AppLink struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type AppItem struct {
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Categories      []string  `json:"categories"`
	Links           []AppLink `json:"links"`
	Slug            string    `json:"slug"`
	Installed       bool      `json:"installed"`
	InstalledTag    string    `json:"installed_tag,omitempty"`
	LatestTag       string    `json:"latest_tag,omitempty"`
	UpdateAvailable bool      `json:"update_available,omitempty"`
	LocalOnly       bool      `json:"local_only,omitempty"`
}

type AppDetails struct {
	Description  string   `json:"description,omitempty"`
	Homepage     string   `json:"homepage,omitempty"`
	Topics       []string `json:"topics,omitempty"`
	ReleaseTag   string   `json:"release_tag,omitempty"`
	ReleaseNotes string   `json:"release_notes,omitempty"`
}

type UpdateCheckEntry struct {
	LatestTag       string `json:"latest_tag,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
}

var (
	cachedApps      []AppItem
	cacheMu         sync.RWMutex
	detailsCache    = make(map[string]AppDetails)
	detailsCacheM   sync.RWMutex
	updateStatusMap = make(map[string]UpdateCheckEntry)
	updateStatusMu  sync.RWMutex
	updateCheckMu   sync.Mutex
)

func loadFeed() ([]AppItem, error) {
	cacheMu.RLock()
	hasCache := len(cachedApps) > 0
	cacheMu.RUnlock()

	if !hasCache {
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get("https://appimage.github.io/feed.json")
		if err != nil {
			return nil, fmt.Errorf("Couldn't load the app list right now.")
		}
		defer resp.Body.Close()

		var feed CatalogFeed
		if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
			return nil, fmt.Errorf("The app list came back in an unexpected format.")
		}

		for i := range feed.Items {
			feed.Items[i].Slug = slugify(feed.Items[i].Name)
		}

		cacheMu.Lock()
		cachedApps = feed.Items
		cacheMu.Unlock()
	}

	cacheMu.RLock()
	defer cacheMu.RUnlock()
	out := make([]AppItem, len(cachedApps))
	copy(out, cachedApps)
	return out, nil
}

func runBackgroundUpdateCheck() {
	updateCheckMu.Lock()
	defer updateCheckMu.Unlock()

	slugs := listInstalledSlugs()
	activeSlugs := make(map[string]bool, len(slugs))
	nextMap := make(map[string]UpdateCheckEntry)

	for _, slug := range slugs {
		activeSlugs[slug] = true
		meta, ok := readMeta(slug)
		if !ok || meta.GitHubRepo == "" {
			continue
		}
		_, _, latest, err := resolveGitHubRelease(meta.GitHubRepo)
		if err != nil || latest == "" {
			updateStatusMu.RLock()
			if prev, exists := updateStatusMap[slug]; exists {
				nextMap[slug] = prev
			}
			updateStatusMu.RUnlock()
			continue
		}
		avail := meta.ReleaseTag != "" && latest != meta.ReleaseTag
		nextMap[slug] = UpdateCheckEntry{
			LatestTag:       latest,
			UpdateAvailable: avail,
		}
	}

	updateStatusMu.Lock()
	for k := range updateStatusMap {
		if !activeSlugs[k] {
			delete(updateStatusMap, k)
		}
	}
	for k, v := range nextMap {
		updateStatusMap[k] = v
	}
	updateStatusMu.Unlock()
}

func startBackgroundUpdateLoop() {
	go func() {
		runBackgroundUpdateCheck()
		ticker := time.NewTicker(backgroundUpdateInterval)
		defer ticker.Stop()
		for range ticker.C {
			runBackgroundUpdateCheck()
		}
	}()
}

func fetchGitHubDetails(repo string) AppDetails {
	clean := strings.Trim(strings.TrimPrefix(strings.TrimSpace(repo), "https://github.com/"), "/")
	if clean == "" {
		return AppDetails{}
	}

	detailsCacheM.RLock()
	if cached, ok := detailsCache[clean]; ok {
		detailsCacheM.RUnlock()
		return cached
	}
	detailsCacheM.RUnlock()

	client := &http.Client{Timeout: 10 * time.Second}
	var out AppDetails

	if resp, err := client.Get("https://api.github.com/repos/" + clean); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var repoInfo struct {
				Description string   `json:"description"`
				Homepage    string   `json:"homepage"`
				Topics      []string `json:"topics"`
			}
			if json.NewDecoder(resp.Body).Decode(&repoInfo) == nil {
				out.Description = strings.TrimSpace(repoInfo.Description)
				out.Homepage = strings.TrimSpace(repoInfo.Homepage)
				out.Topics = repoInfo.Topics
			}
		}
	}

	if resp, err := client.Get("https://api.github.com/repos/" + clean + "/releases/latest"); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var rel struct {
				TagName string `json:"tag_name"`
				Body    string `json:"body"`
			}
			if json.NewDecoder(resp.Body).Decode(&rel) == nil {
				out.ReleaseTag = strings.TrimSpace(rel.TagName)
				out.ReleaseNotes = strings.TrimSpace(rel.Body)
			}
		}
	}

	detailsCacheM.Lock()
	detailsCache[clean] = out
	detailsCacheM.Unlock()
	return out
}

func handleAppDetails(w http.ResponseWriter, r *http.Request) {
	gh := r.URL.Query().Get("github")
	details := fetchGitHubDetails(gh)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(details)
}

func handleCheckUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Unsupported request.", http.StatusMethodNotAllowed)
		return
	}
	go runBackgroundUpdateCheck()
	w.WriteHeader(http.StatusAccepted)
}

func handleApps(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("check_updates") == "1" {
		runBackgroundUpdateCheck()
	}

	apps, err := loadFeed()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	updateStatusMu.RLock()
	snap := make(map[string]UpdateCheckEntry, len(updateStatusMap))
	for k, v := range updateStatusMap {
		snap[k] = v
	}
	updateStatusMu.RUnlock()

	seenSlugs := make(map[string]bool)
	for i := range apps {
		slug := apps[i].Slug
		if slug == "" {
			continue
		}
		seenSlugs[slug] = true
		if _, err := os.Stat(filepath.Join(optDir, slug, slug+".AppImage")); err == nil {
			apps[i].Installed = true
			if meta, ok := readMeta(slug); ok {
				apps[i].InstalledTag = meta.ReleaseTag
				if apps[i].Description == "" && meta.Description != "" {
					apps[i].Description = meta.Description
				}
				if entry, has := snap[slug]; has && entry.LatestTag != "" {
					apps[i].LatestTag = entry.LatestTag
					if meta.ReleaseTag != "" && entry.LatestTag != meta.ReleaseTag {
						apps[i].UpdateAvailable = true
					}
				}
			}
		}
	}

	// Include locally installed AppImages that aren't in the online catalog
	for _, slug := range listInstalledSlugs() {
		if seenSlugs[slug] {
			continue
		}
		name := slug
		tag := "local"
		desc := "Installed from a local AppImage file."
		var links []AppLink
		var latestTag string
		var updAvail bool
		if meta, ok := readMeta(slug); ok {
			if meta.Name != "" {
				name = meta.Name
			}
			if meta.ReleaseTag != "" {
				tag = meta.ReleaseTag
			}
			if meta.Description != "" {
				desc = meta.Description
			}
			if meta.GitHubRepo != "" {
				links = append(links, AppLink{Type: "GitHub", URL: "https://github.com/" + meta.GitHubRepo})
			}
			if meta.Homepage != "" {
				links = append(links, AppLink{Type: "Home", URL: meta.Homepage})
			}
			if entry, has := snap[slug]; has && entry.LatestTag != "" {
				latestTag = entry.LatestTag
				if meta.ReleaseTag != "" && entry.LatestTag != meta.ReleaseTag {
					updAvail = true
				}
			}
		}
		apps = append([]AppItem{{
			Name:            name,
			Description:     desc,
			Categories:      []string{"Local"},
			Links:           links,
			Slug:            slug,
			Installed:       true,
			InstalledTag:    tag,
			LatestTag:       latestTag,
			UpdateAvailable: updAvail,
			LocalOnly:       true,
		}}, apps...)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(apps)
}

func runPrivileged(args ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("Couldn't find the ahhhh program path.")
	}

	execPath := self
	if strings.Contains(self, "/.mount_") || os.Getenv("APPIMAGE") != "" {
		tmpBin, err := os.CreateTemp("/var/tmp", "ahhhh-priv-*")
		if err != nil {
			return fmt.Errorf("Couldn't prepare privileged helper binary.")
		}
		tmpBinPath := tmpBin.Name()
		tmpBin.Close()
		defer os.Remove(tmpBinPath)

		if err := copyFileSimple(self, tmpBinPath, 0755); err != nil {
			return fmt.Errorf("Couldn't copy helper binary for admin install.")
		}
		execPath = tmpBinPath
	}

	cmd := exec.Command("pkexec", append([]string{execPath}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = "Action was cancelled or didn't get admin permission."
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func selfInstallSystemWide() error {
	source := strings.TrimSpace(os.Getenv("APPIMAGE"))
	if source == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("Couldn't find the ahhhh binary path.")
		}
		source = exe
	}
	tag := detectVersionTag(filepath.Base(source), os.Getenv("ARGV0"))
	return runPrivileged("--sys-install", "ahhhh", source, selfRepo, tag)
}

func handleInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Unsupported request.", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Couldn't read the install request.", http.StatusBadRequest)
		return
	}

	downloadURL, repo, tag, err := resolveGitHubRelease(req.URL)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := runPrivileged("--sys-install", req.Name, downloadURL, repo, tag); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slug := slugify(req.Name)
	if slug != "" {
		updateStatusMu.Lock()
		delete(updateStatusMap, slug)
		updateStatusMu.Unlock()
	}

	w.WriteHeader(http.StatusOK)
}

func handleUpdateAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Unsupported request.", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Items []struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Couldn't read the update-all request.", http.StatusBadRequest)
		return
	}

	if len(req.Items) == 0 {
		w.WriteHeader(http.StatusOK)
		return
	}

	for _, item := range req.Items {
		downloadURL, repo, tag, err := resolveGitHubRelease(item.URL)
		if err != nil {
			http.Error(w, fmt.Sprintf("%s: %s", item.Name, err.Error()), http.StatusBadRequest)
			return
		}
		if err := runPrivileged("--sys-install", item.Name, downloadURL, repo, tag); err != nil {
			http.Error(w, fmt.Sprintf("%s: %s", item.Name, err.Error()), http.StatusInternalServerError)
			return
		}
		slug := slugify(item.Name)
		if slug != "" {
			updateStatusMu.Lock()
			delete(updateStatusMap, slug)
			updateStatusMu.Unlock()
		}
	}

	w.WriteHeader(http.StatusOK)
}

func handleUploadLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Unsupported request.", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(2 << 30); err != nil {
		http.Error(w, "Couldn't read the uploaded file.", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Please choose an .AppImage file first.", http.StatusBadRequest)
		return
	}
	defer file.Close()

	customName := strings.TrimSpace(r.FormValue("name"))
	if customName == "" {
		customName = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}

	tmpDir, err := os.MkdirTemp("", "ahhhh-upload-*")
	if err != nil {
		http.Error(w, "Couldn't create a temporary folder.", http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	tmpPath := filepath.Join(tmpDir, header.Filename)
	dst, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "Couldn't save the uploaded file.", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		dst.Close()
		http.Error(w, "File upload was interrupted.", http.StatusInternalServerError)
		return
	}
	dst.Close()

	if err := runPrivileged("--sys-install", customName, tmpPath, "", "local"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go runBackgroundUpdateCheck()
	w.WriteHeader(http.StatusOK)
}

func handleRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Unsupported request.", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Couldn't read the remove request.", http.StatusBadRequest)
		return
	}

	if err := runPrivileged("--sys-remove", req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slug := slugify(req.Name)
	if slug != "" {
		updateStatusMu.Lock()
		delete(updateStatusMap, slug)
		updateStatusMu.Unlock()
	}

	w.WriteHeader(http.StatusOK)
}

func runCLI(args []string) {
	switch args[0] {
	case "list":
		slugs := listInstalledSlugs()
		if len(slugs) == 0 {
			fmt.Println("No system-wide AppImages installed yet.")
			return
		}
		for _, s := range slugs {
			if m, ok := readMeta(s); ok && m.ReleaseTag != "" {
				fmt.Printf("%-24s (%s)\n", m.Name, m.ReleaseTag)
			} else {
				fmt.Println(s)
			}
		}

	case "install":
		if len(args) < 2 {
			log.Fatal("Usage: ahhhh install <app-name-or-local-file.AppImage>")
		}
		target := args[1]
		if _, err := os.Stat(target); err == nil {
			abs, _ := filepath.Abs(target)
			fmt.Printf("Installing local file %s...\n", filepath.Base(abs))
			if err := runPrivileged("--sys-install", "", abs, "", "local"); err != nil {
				log.Fatal(err)
			}
			fmt.Println("Done.")
			return
		}

		apps, err := loadFeed()
		if err != nil {
			log.Fatal(err)
		}
		needle := strings.ToLower(target)
		for _, a := range apps {
			if strings.ToLower(a.Name) == needle || a.Slug == slugify(target) {
				var gh string
				for _, l := range a.Links {
					if l.Type == "GitHub" {
						gh = l.URL
						break
					}
				}
				if gh == "" {
					log.Fatalf("No GitHub release link found for %s.", a.Name)
				}
				dl, repo, tag, err := resolveGitHubRelease(gh)
				if err != nil {
					log.Fatal(err)
				}
				fmt.Printf("Installing %s (%s)...\n", a.Name, tag)
				if err := runPrivileged("--sys-install", a.Name, dl, repo, tag); err != nil {
					log.Fatal(err)
				}
				fmt.Println("Done.")
				return
			}
		}
		log.Fatalf("Couldn't find %q in the catalog.", target)

	case "update":
		slugs := listInstalledSlugs()
		updated := 0
		for _, s := range slugs {
			m, ok := readMeta(s)
			if !ok || m.GitHubRepo == "" {
				continue
			}
			dl, repo, latest, err := resolveGitHubRelease(m.GitHubRepo)
			if err != nil {
				fmt.Printf("Skipping %s: %v\n", m.Name, err)
				continue
			}
			if latest != "" && latest != m.ReleaseTag {
				fmt.Printf("Updating %s: %s -> %s...\n", m.Name, m.ReleaseTag, latest)
				if err := runPrivileged("--sys-install", m.Name, dl, repo, latest); err != nil {
					log.Fatal(err)
				}
				updated++
			}
		}
		if updated == 0 {
			fmt.Println("All GitHub-tracked AppImages are up to date.")
		}

	case "remove":
		if len(args) < 2 {
			log.Fatal("Usage: ahhhh remove <app-name>")
		}
		fmt.Printf("Removing %s...\n", args[1])
		if err := runPrivileged("--sys-remove", args[1]); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Removed.")

	default:
		fmt.Println("Usage: ahhhh [list | install <name|file> | update | remove <name>]")
	}
}

func main() {
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "--self-install":
			if err := selfInstallSystemWide(); err != nil {
				log.Fatal(err)
			}
			fmt.Println("Installed ahhhh system-wide.")
			return

		case "--sys-install":
			if len(os.Args) < 4 {
				log.Fatal("Missing install arguments.")
			}
			repo, tag := "", ""
			if len(os.Args) >= 5 {
				repo = os.Args[4]
			}
			if len(os.Args) >= 6 {
				tag = os.Args[5]
			}
			if err := installAppImageSystemWide(os.Args[2], os.Args[3], repo, tag); err != nil {
				log.Fatal(err)
			}
			return

		case "--sys-remove":
			if len(os.Args) < 3 {
				log.Fatal("Missing app name.")
			}
			if err := removeAppImageSystemWide(os.Args[2]); err != nil {
				log.Fatal(err)
			}
			return

		case "list", "install", "update", "remove", "help", "--help", "-h":
			runCLI(os.Args[1:])
			return
		}
	}

	addr := "127.0.0.1:8745"

	if conn, err := net.DialTimeout("tcp", addr, 250*time.Millisecond); err == nil {
		conn.Close()
		_ = exec.Command("xdg-open", "http://"+addr).Start()
		return
	}

	startBackgroundUpdateLoop()

	sub, _ := fs.Sub(webFS, "web")
	http.Handle("/", http.FileServer(http.FS(sub)))
	http.HandleFunc("/api/apps", handleApps)
	http.HandleFunc("/api/app-details", handleAppDetails)
	http.HandleFunc("/api/check-updates", handleCheckUpdates)
	http.HandleFunc("/api/install", handleInstall)
	http.HandleFunc("/api/update-all", handleUpdateAll)
	http.HandleFunc("/api/upload-local", handleUploadLocal)
	http.HandleFunc("/api/remove", handleRemove)

	log.Printf("AppAppApp (ahhhh) listening on http://%s\n", addr)
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = exec.Command("xdg-open", "http://"+addr).Start()
	}()
	log.Fatal(http.ListenAndServe(addr, nil))
}