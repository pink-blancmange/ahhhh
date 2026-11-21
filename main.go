package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

const listenAddr = "127.0.0.1:8745"

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if _, err := exec.LookPath("xdg-open"); err == nil {
			cmd = exec.Command("xdg-open", url)
		}
	}
	if cmd != nil {
		_ = cmd.Start()
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func serveFallbackFeed(w http.ResponseWriter, liveErr string) {
	raw, err := webFS.ReadFile("web/fallback-feed.json")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"error": liveErr, "items": []any{}})
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Ahhhh-Feed-Source", "bundled-fallback")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func handleFeed(w http.ResponseWriter, r *http.Request) {
	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest("GET", "https://appimage.github.io/feed.json", nil)
	if err != nil {
		serveFallbackFeed(w, err.Error())
		return
	}
	req.Header.Set("User-Agent", "ahhhh-appimage-manager/0.4.4")
	resp, err := client.Do(req)
	if err != nil {
		serveFallbackFeed(w, err.Error())
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		serveFallbackFeed(w, fmt.Sprintf("feed HTTP %d", resp.StatusCode))
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = io.Copy(w, resp.Body)
}

func handleInstalled(w http.ResponseWriter, r *http.Request) {
	metaMap := listInstalledMeta()
	apps := make([]AppMeta, 0, len(metaMap))
	for _, m := range metaMap {
		apps = append(apps, m)
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": apps})
}

func handleInspectGitHub(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	target, err := resolveGitHubTarget(repo, name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, target)
}

func handleInstallURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var body struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := installFromURL(body.Name, body.URL); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func handleInstallLocal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	if err := r.ParseMultipartForm(1024 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing file"})
		return
	}
	defer file.Close()

	customName := strings.TrimSpace(r.FormValue("name"))
	tmp, err := os.CreateTemp("", "ahhhh-local-*")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(tmp, file); err != nil {
		tmp.Close()
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	tmp.Close()

	if err := installLocalFile(tmpPath, filepath.Base(header.Filename), customName); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func handleRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "POST required"})
		return
	}
	var body struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	target := strings.TrimSpace(body.Slug)
	if target == "" {
		target = strings.TrimSpace(body.Name)
	}
	if err := removeInstalledApp(target); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func main() {
	subFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(subFS)))
	mux.HandleFunc("/api/feed", handleFeed)
	mux.HandleFunc("/api/installed", handleInstalled)
	mux.HandleFunc("/api/github/inspect", handleInspectGitHub)
	mux.HandleFunc("/api/install/url", handleInstallURL)
	mux.HandleFunc("/api/install/local", handleInstallLocal)
	mux.HandleFunc("/api/remove", handleRemove)

	url := "http://" + listenAddr
	log.Printf("Ahhhh (ahhhh) listening on %s", url)
	go openBrowser(url)

	if err := http.ListenAndServe(listenAddr, mux); err != nil {
		log.Fatal(err)
	}
}