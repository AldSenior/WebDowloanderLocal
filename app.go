package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sitemvp/downloader"
	proccesor "sitemvp/processor"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx         context.Context
	server      *http.Server
	activeJobs  sync.Map // Map for tracking active adaptation jobs
	mu          sync.Mutex
	servingPath string // Path of the site currently being served
}

// SiteMeta represents a downloaded site
type SiteMeta struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	Icon      string `json:"icon"`      // Base64 icon data
	Domain    string `json:"domain"`    // Reconstructed visual path
	EntryPath string `json:"entryPath"` // Relative path to index.html
}

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

// DownloadSite starts the download process
func (a *App) DownloadSite(urlStr string, outputDir string) string {
	if urlStr == "" {
		return "Error: URL is empty"
	}
	if outputDir == "" {
		outputDir = "downloads"
	}

	normalizedURL, _ := downloader.NormalizeURL(urlStr)
	if _, busy := a.activeJobs.LoadOrStore("dl:"+normalizedURL, true); busy {
		return "Download already in progress"
	}

	cfg := downloader.Config{
		OutputDir:   outputDir,
		Workers:     10,
		Retries:     5,
		MaxDepth:    15,
		Delay:       200 * time.Millisecond,
		MaxFileSize: downloader.DefaultMaxFileSize,
		UserAgent:   downloader.DefaultUserAgent,
	}

	// The new go func block replaces the existing two go func blocks
	go func() {
		// Defensive cleanup
		defer func() {
			a.activeJobs.Delete("dl:" + normalizedURL)
			runtime.EventsEmit(a.ctx, "download:done", normalizedURL)
			runtime.EventsEmit(a.ctx, "library:refresh", "DONE") // Added this from original defer
			log.Printf("[System] Job for %s cleaned up", normalizedURL)
		}()

		runtime.EventsEmit(a.ctx, "download:start", normalizedURL)

		job, err := downloader.NewJob(urlStr, cfg)
		if err != nil {
			runtime.EventsEmit(a.ctx, "download:log", "[Error] "+err.Error())
			return
		}

		// Передаем логи в GUI
		go func() {
			for msg := range job.Events {
				runtime.EventsEmit(a.ctx, "download:log", msg)
			}
		}()

		job.Run()
		runtime.EventsEmit(a.ctx, "download:log", "[System] Download phase complete.")
	}()

	return "Download started"
}

// AnalyzeScripts returns a list of script sources from the site
func (a *App) AnalyzeScripts(path string) []string {
	host := a.extractHostFromPath(path)
	sourceDir := strings.TrimSuffix(path, "_processed")

	if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
		return []string{}
	}

	p := proccesor.NewProcessor(host)
	return p.AnalyzeScripts(sourceDir)
}

// AdaptPaths runs the post-processor with optional script removal
func (a *App) AdaptPaths(path string, scriptsToRemove []string) string {
	normalized := filepath.ToSlash(path)
	if _, busy := a.activeJobs.LoadOrStore(normalized, true); busy {
		return "Job already in progress"
	}

	host := a.extractHostFromPath(path)

	go func() {
		defer a.activeJobs.Delete(normalized)
		runtime.EventsEmit(a.ctx, "adapting:start", normalized)
		runtime.EventsEmit(a.ctx, "download:log", fmt.Sprintf("[System] Starting path adaptation for %s...", host))

		sourceDir := strings.TrimSuffix(path, "_processed")
		processedDir := sourceDir + "_processed"

		if _, err := os.Stat(sourceDir); os.IsNotExist(err) {
			runtime.EventsEmit(a.ctx, "download:log", "[Error] Source directory not found: "+sourceDir)
			runtime.EventsEmit(a.ctx, "adapting:done", normalized)
			return
		}

		os.RemoveAll(processedDir)

		p := proccesor.NewProcessor(host)
		p.OnLog = func(msg string) {
			msg = stripAnsi(msg)
			if msg != "" {
				if strings.Contains(msg, "[ANALYZING]") {
					runtime.EventsEmit(a.ctx, "adaptation:analyzing", normalized)
				}
				runtime.EventsEmit(a.ctx, "download:log", "[Processor] "+msg)
				processed := atomic.LoadInt64(&p.Stats.FilesProcessed)
				total := p.Stats.TotalFiles
				if total > 0 {
					runtime.EventsEmit(a.ctx, "adaptation:progress", map[string]interface{}{
						"path":    normalized,
						"current": processed,
						"total":   total,
					})
				}
			}
		}

		p.Process(sourceDir, scriptsToRemove)

		runtime.EventsEmit(a.ctx, "download:log", "[System] Adaptation sequence finished.")
		runtime.EventsEmit(a.ctx, "adapting:done", normalized)
		runtime.EventsEmit(a.ctx, "library:refresh", "DONE")
	}()

	return "Adaptation started"
}

func stripAnsi(msg string) string {
	msg = strings.ReplaceAll(msg, "\033[31m", "")
	msg = strings.ReplaceAll(msg, "\033[32m", "")
	msg = strings.ReplaceAll(msg, "\033[36m", "")
	msg = strings.ReplaceAll(msg, "\033[33m", "")
	msg = strings.ReplaceAll(msg, "\033[0m", "")
	return strings.TrimSpace(msg)
}

// extractHostFromPath tries to find the host part from a folder name
func (a *App) extractHostFromPath(path string) string {
	folder := filepath.Base(strings.TrimSuffix(path, "_processed"))
	return folder
}

// GetDownloads scans the downloads directory and returns a list of sites
func (a *App) GetDownloads() []SiteMeta {
	outputDir := "downloads"
	var sites []SiteMeta

	files, err := os.ReadDir(outputDir)
	if err != nil {
		return sites
	}

	sitesMap := make(map[string]SiteMeta)
	for _, f := range files {
		if !f.IsDir() {
			continue
		}
		name := f.Name()
		isProcessed := strings.HasSuffix(name, "_processed")
		baseName := strings.TrimSuffix(name, "_processed")
		path := filepath.Join(outputDir, name)

		icon := a.getSiteIcon(path)
		entryPath := a.getEntryPath(path)

		// If entryPath is in a sub-folder (like /ru/), the domain name should reflect that
		domain := strings.ReplaceAll(baseName, "_", "/")
		if entryPath != "" && entryPath != "index.html" {
			subPath := filepath.Dir(entryPath)
			if subPath != "." {
				domain = domain + "/" + subPath
			}
		}

		if prev, exists := sitesMap[baseName]; exists {
			if isProcessed {
				sitesMap[baseName] = SiteMeta{Name: baseName, Path: path, Icon: icon, Domain: domain, EntryPath: entryPath}
			} else if prev.Icon == "" && icon != "" {
				p := sitesMap[baseName]
				p.Icon = icon
				sitesMap[baseName] = p
			}
		} else {
			sitesMap[baseName] = SiteMeta{Name: baseName, Path: path, Icon: icon, Domain: domain, EntryPath: entryPath}
		}
	}

	for _, meta := range sitesMap {
		sites = append(sites, meta)
	}
	return sites
}

// getEntryPath finds the relative path to the best index.html with depth limit
func (a *App) getEntryPath(dir string) string {
	// 1. Fast path: check root
	if _, err := os.Stat(filepath.Join(dir, "index.html")); err == nil {
		return "index.html"
	}

	var bestEntry string
	minDepth := 999

	// 2. Limited search
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		depth := strings.Count(rel, string(os.PathSeparator))

		if d.IsDir() {
			if depth > 3 {
				return filepath.SkipDir
			}
			return nil
		}

		if strings.ToLower(d.Name()) == "index.html" && !strings.Contains(strings.ToLower(rel), "404") {
			if depth < minDepth {
				minDepth = depth
				bestEntry = filepath.ToSlash(rel)
				// If we found something at level 1 or 2, good enough
				if depth <= 1 {
					return fmt.Errorf("stop")
				}
			}
		}
		return nil
	})
	return bestEntry
}

// getSiteIcon searches for favicon with depth limit
func (a *App) getSiteIcon(path string) string {
	iconFiles := []string{
		"favicon.ico", "favicon.png", "favicon.svg", "apple-touch-icon.png", "icon.png",
		"img/favicon.ico", "img/favicon.png", "img/favicon.svg",
		"assets/favicon.ico", "assets/img/favicon.ico",
	}

	for _, f := range iconFiles {
		fullPath := filepath.Join(path, f)
		if _, err := os.Stat(fullPath); err == nil {
			data, err := os.ReadFile(fullPath)
			if err == nil {
				return encodeBase64Icon(f, data)
			}
		}
	}

	var foundPath string
	filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(path, p)
		depth := strings.Count(rel, string(os.PathSeparator))

		if d.IsDir() {
			if depth > 2 {
				return filepath.SkipDir
			}
			return nil
		}

		name := strings.ToLower(d.Name())
		if strings.Contains(name, "favicon") || strings.Contains(name, "apple-touch-icon") {
			ext := filepath.Ext(name)
			if ext == ".ico" || ext == ".png" || ext == ".svg" || ext == ".jpg" {
				foundPath = p
				return fmt.Errorf("found")
			}
		}
		return nil
	})

	if foundPath != "" {
		data, err := os.ReadFile(foundPath)
		if err == nil {
			return encodeBase64Icon(filepath.Base(foundPath), data)
		}
	}
	return ""
}

func encodeBase64Icon(filename string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(filename))
	mime := "image/x-icon"
	switch ext {
	case ".png":
		mime = "image/png"
	case ".svg":
		mime = "image/svg+xml"
	case ".jpg", ".jpeg":
		mime = "image/jpeg"
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data))
}

// DeleteSite removes a site folder
func (a *App) DeleteSite(path string) string {
	outputDir := "downloads"
	absDownloads, _ := filepath.Abs(outputDir)
	absPath, err := filepath.Abs(path)
	if err != nil || !strings.HasPrefix(absPath, absDownloads) {
		return "Error"
	}

	basePath := strings.TrimSuffix(path, "_processed")
	processedPath := basePath + "_processed"
	os.RemoveAll(basePath)
	os.RemoveAll(processedPath)

	return "Deleted"
}

// findFreePort returns a free port starting from the given port
func (a *App) findFreePort(startPort int) int {
	for port := startPort; port < startPort+10; port++ {
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err == nil {
			ln.Close()
			return port
		}
	}
	return 0
}

// StartServer starts a static file server with dynamic port fallback
func (a *App) StartServer(dir string, portStr string) string {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.server != nil {
		// Stop the existing server before starting a new one
		a.stopServerNoLock()
	}

	port := 8080
	if portStr != "" {
		if p, err := strconv.Atoi(portStr); err == nil {
			port = p
		}
	}

	// Dynamic port selection
	actualPort := a.findFreePort(port)
	if actualPort == 0 {
		runtime.EventsEmit(a.ctx, "server:error", "No free ports available")
		return "Error"
	}

	portStr = strconv.Itoa(actualPort)

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		runtime.EventsEmit(a.ctx, "server:error", "Missing: "+dir)
		return "Error"
	}

	a.server = &http.Server{
		Addr:    ":" + portStr,
		Handler: http.FileServer(http.Dir(dir)),
	}
	a.servingPath = filepath.ToSlash(dir)

	go func() {
		runtime.EventsEmit(a.ctx, "server:status", fmt.Sprintf("http://localhost:%s", portStr))
		runtime.EventsEmit(a.ctx, "server:started", map[string]string{
			"url":  fmt.Sprintf("http://localhost:%s", portStr),
			"path": a.servingPath,
		})
		if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			runtime.EventsEmit(a.ctx, "server:error", err.Error())
			a.mu.Lock()
			a.server = nil
			a.servingPath = ""
			a.mu.Unlock()
			runtime.EventsEmit(a.ctx, "server:stopped", "ERROR")
		}
	}()

	return fmt.Sprintf("http://localhost:%s", portStr)
}

// StopServer stops the running server
func (a *App) StopServer() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stopServerNoLock()
}

func (a *App) stopServerNoLock() string {
	if a.server != nil {
		s := a.server
		a.server = nil
		serving := a.servingPath
		a.servingPath = ""

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			s.Close()
			runtime.EventsEmit(a.ctx, "server:status", "Forced stop")
			runtime.EventsEmit(a.ctx, "server:stopped", serving)
			return "Forced stop"
		}
		runtime.EventsEmit(a.ctx, "server:status", "Stopped")
		runtime.EventsEmit(a.ctx, "server:stopped", serving)
		return "Stopped"
	}
	return "Not running"
}

// LaunchSite starts server and opens browser
func (a *App) LaunchSite(path string) string {
	// Мы хотим всегда запускать сервер от корня хоста (например, downloads/wails.io),
	// чтобы работали абсолютные ссылки от корня (/assets/...)

	absPath, _ := filepath.Abs(path)
	downloadsDir, _ := filepath.Abs("downloads")

	// Находим папку хоста (первый уровень внутри downloads)
	rel, err := filepath.Rel(downloadsDir, absPath)
	if err == nil && !strings.HasPrefix(rel, "..") {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if len(parts) > 0 {
			hostDir := filepath.Join(downloadsDir, parts[0])
			serverUrl := a.StartServer(hostDir, "")
			if serverUrl != "Error" {
				// Теперь вычисляем путь входа относительно КОРНЯ ХОСТА
				entryPath := a.getEntryPath(absPath)
				// Если мы запускаем подпапку (например, /ru),
				// то entryPath будет относительным к /ru. Нам нужен относительный к хосту.
				fullRelEntry, _ := filepath.Rel(hostDir, filepath.Join(absPath, entryPath))

				finalUrl := strings.TrimSuffix(serverUrl, "/") + "/" + strings.TrimPrefix(filepath.ToSlash(fullRelEntry), "/")
				runtime.BrowserOpenURL(a.ctx, finalUrl)
				return "Launched " + finalUrl
			}
		}
	}

	// Fallback если что-то пошло не так
	urlStr := a.StartServer(path, "")
	if urlStr != "Error" {
		entryPath := a.getEntryPath(path)
		if entryPath != "" {
			urlStr = strings.TrimSuffix(urlStr, "/") + "/" + strings.TrimPrefix(entryPath, "/")
		}
		runtime.BrowserOpenURL(a.ctx, urlStr)
	}
	return "Launched " + urlStr
}

// OpenFolder opens the system file explorer
func (a *App) OpenFolder(path string) {
	absPath, _ := filepath.Abs(path)
	var cmd *exec.Cmd
	switch runtime.Environment(a.ctx).Platform {
	case "darwin":
		cmd = exec.Command("open", absPath)
	case "windows":
		cmd = exec.Command("explorer", absPath)
	default:
		cmd = exec.Command("xdg-open", absPath)
	}
	cmd.Run()
}

// SelectFolder opens a directory selection dialog
func (a *App) SelectFolder() string {
	folder, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select Site Directory",
	})
	if err != nil {
		return ""
	}
	return folder
}
