package downloader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"golang.org/x/net/html"
)

const (
	DefaultWorkers     = 6
	DefaultMaxDepth    = 30
	DefaultRetries     = 5
	DefaultDelay       = 2 * time.Second
	DefaultMaxFileSize = 15 << 20
	DefaultUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	StateFileExtension = ".state.json"
)

var (
	ErrInvalidURL     = errors.New("invalid URL")
	ErrDownloadFailed = errors.New("download failed after retries")
	ErrParseFailed    = errors.New("parsing failed")
)

type FileMetadata struct {
	URL         string
	ContentType string
	Hash        string
	Depth       int
}

type JobStats struct {
	TotalFiles      int64
	DownloadedBytes int64
	Failed          int64
	Skipped         int64
	Speed           float64
	ETA             time.Duration
	FileTypes       map[string]int64
	StartTime       time.Time
}

type JobState struct {
	ID          string
	RootURL     string
	PendingURLs []string
	DepthMap    map[string]int
	Stats       JobStats
	Config      Config
}

type Config struct {
	Workers     int
	MaxDepth    int
	Retries     int
	Delay       time.Duration
	MaxFileSize int64
	OutputDir   string
	UserAgent   string
}

type ContentParser interface {
	CanParse(contentType string) bool
	Parse(content []byte, baseURL string) ([]string, error)
}

type URLFilter interface {
	ShouldDownload(url string) bool
	FilterReason(url string) string
}

type ContentHandler interface {
	Priority() int
	Handle(content []byte, meta FileMetadata) ([]byte, error)
}

// HTMLParser для извлечения СЫРЫХ ссылок (без изменений)
type HTMLParser struct{}

func (p *HTMLParser) CanParse(ct string) bool { return strings.Contains(ct, "text/html") }

func (p *HTMLParser) Parse(content []byte, baseURL string) ([]string, error) {
	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, ErrParseFailed
	}
	var links []string
	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "a", "link":
				for _, a := range n.Attr {
					if a.Key == "href" {
						links = append(links, a.Val)
					}
				}
			case "img", "script", "source":
				for _, a := range n.Attr {
					if a.Key == "src" {
						links = append(links, a.Val)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	// Возвращаем СЫРЫЕ ссылки (без замены .php → .html)
	return resolveRawLinks(links, baseURL), nil
}

type CSSParser struct{}

func (p *CSSParser) CanParse(ct string) bool { return strings.Contains(ct, "text/css") }

func (p *CSSParser) Parse(content []byte, baseURL string) ([]string, error) {
	re := regexp.MustCompile(`(?i)url\s*\(\s*['"]?([^'")]+)['"]?\s*\)`)
	matches := re.FindAllSubmatch(content, -1)
	var links []string
	for _, m := range matches {
		if len(m[1]) > 0 {
			links = append(links, string(m[1]))
		}
	}
	return resolveRawLinks(links, baseURL), nil
}

// resolveRawLinks — разрешает ссылки БЕЗ изменений расширений
func resolveRawLinks(links []string, baseURL string) []string {
	var resolved []string
	base, _ := url.Parse(baseURL)
	bad := []string{"devnull", "410011174743222", "yoomoney", "t.me/metanitcom"}

	for _, l := range links {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "data:") || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "javascript:") {
			continue
		}
		// Handle protocol-relative URLs
		if strings.HasPrefix(l, "//") {
			l = "https:" + l
		}
		u, err := url.Parse(l)
		if err != nil {
			continue
		}
		res := base.ResolveReference(u).String()

		skip := false
		for _, p := range bad {
			if strings.Contains(res, p) {
				skip = true
				break
			}
		}
		if !skip {
			resolved = append(resolved, res)
			log.Printf("Resolved RAW link: %s", res)
		}
	}
	return resolved
}

func replacePhpToHtmlLinks(content []byte, baseURL string) ([]byte, error) {
	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return content, nil
	}

	baseParsed, _ := url.Parse(baseURL)

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for i := range n.Attr {
				attr := &n.Attr[i]
				if attr.Key == "href" || attr.Key == "src" || attr.Key == "action" {
					orig := attr.Val

					// Пропускаем специальные протоколы
					if strings.HasPrefix(orig, "data:") ||
						strings.HasPrefix(orig, "#") ||
						strings.HasPrefix(orig, "javascript:") ||
						strings.HasPrefix(orig, "mailto:") ||
						strings.HasPrefix(orig, "tel:") {
						continue
					}

					// Разбираем URL
					u, err := url.Parse(orig)
					if err != nil {
						continue
					}

					// Пропускаем внешние ссылки
					if u.Host != "" && u.Host != baseParsed.Host {
						continue
					}

					// Обрабатываем путь
					path := u.Path

					// Если путь пустой или корневой
					if path == "" || path == "/" {
						// Для корня оставляем как есть
						u.Path = "/"
						attr.Val = u.String()
						continue
					}

					// Пропускаем ссылки на ресурсы (CSS, JS, изображения)
					// Проверяем расширения файлов
					lowerPath := strings.ToLower(path)
					isResource := strings.HasSuffix(lowerPath, ".css") ||
						strings.HasSuffix(lowerPath, ".js") ||
						strings.HasSuffix(lowerPath, ".mjs") ||
						strings.HasSuffix(lowerPath, ".cjs") ||
						strings.HasSuffix(lowerPath, ".png") ||
						strings.HasSuffix(lowerPath, ".jpg") ||
						strings.HasSuffix(lowerPath, ".jpeg") ||
						strings.HasSuffix(lowerPath, ".gif") ||
						strings.HasSuffix(lowerPath, ".svg") ||
						strings.HasSuffix(lowerPath, ".ico") ||
						strings.HasSuffix(lowerPath, ".woff") ||
						strings.HasSuffix(lowerPath, ".woff2") ||
						strings.HasSuffix(lowerPath, ".ttf") ||
						strings.HasSuffix(lowerPath, ".eot") ||
						strings.HasSuffix(lowerPath, ".otf") ||
						strings.HasSuffix(lowerPath, ".mp4") ||
						strings.HasSuffix(lowerPath, ".webm") ||
						strings.HasSuffix(lowerPath, ".mp3") ||
						strings.HasSuffix(lowerPath, ".wav") ||
						strings.HasSuffix(lowerPath, ".ogg")

					if isResource {
						// Для ресурсов оставляем ссылки как есть
						continue
					}

					// Преобразуем .php ссылки (только для HTML страниц)
					if strings.HasSuffix(lowerPath, ".php") {
						// Убираем .php
						newPath := strings.TrimSuffix(path, ".php")

						// Если это был "index.php", преобразуем в "/"
						if strings.HasSuffix(strings.ToLower(newPath), "/index") {
							newPath = strings.TrimSuffix(newPath, "/index")
							if newPath == "" {
								newPath = "/"
							}
						} else if strings.EqualFold(newPath, "index") {
							// Если просто "index.php", тоже в "/"
							newPath = "/"
						}

						u.Path = newPath
						attr.Val = u.String()
						log.Printf("🔗 Rewrote PHP link: %s → %s", orig, attr.Val)
					} else if strings.HasSuffix(lowerPath, ".html") ||
						strings.HasSuffix(lowerPath, ".htm") {
						// Преобразуем .html ссылки
						// Убираем расширение
						newPath := strings.TrimSuffix(
							strings.TrimSuffix(path, ".html"), ".htm")

						// Если это был "index.html", преобразуем в "/"
						if strings.HasSuffix(strings.ToLower(newPath), "/index") {
							newPath = strings.TrimSuffix(newPath, "/index")
							if newPath == "" {
								newPath = "/"
							}
						} else if strings.EqualFold(newPath, "index") {
							// Если просто "index.html", тоже в "/"
							newPath = "/"
						}

						u.Path = newPath
						attr.Val = u.String()
						log.Printf("🔗 Rewrote HTML link: %s → %s", orig, attr.Val)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	var buf bytes.Buffer
	html.Render(&buf, doc)
	return buf.Bytes(), nil
}

// FileSaveStrategy - стратегия сохранения файлов
type FileSaveStrategy interface {
	ShouldSaveAsDirectory(url string, contentType string) bool
	GetSavePath(outputDir string, url string, contentType string) (string, string) // путь, имя файла
	RewriteLink(originalURL, baseURL string) string
}

// DirectoryIndexStrategy - стратегия "директория с index.html"
type DirectoryIndexStrategy struct{}

func (s *DirectoryIndexStrategy) ShouldSaveAsDirectory(urlStr string, contentType string) bool {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	path := parsed.Path

	// Для HTML контента сохраняем как директорию
	if strings.Contains(contentType, "text/html") {
		return true
	}

	// Для .php файлов тоже (даже если content-type не указан)
	if strings.HasSuffix(strings.ToLower(path), ".php") {
		return true
	}

	// Для путей без расширения, которые не являются ресурсами
	if !strings.Contains(path, ".") && path != "/" && path != "" {
		// Проверяем, не является ли это API endpoint или подобным
		if !strings.Contains(path, "/api/") &&
			!strings.Contains(path, "/ajax/") &&
			!strings.Contains(path, "/rest/") {
			return true
		}
	}

	return false
}

func (s *DirectoryIndexStrategy) GetSavePath(outputDir string, urlStr string, contentType string) (string, string) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		log.Printf("Parse error in GetSavePath: %v", err)
		return "", ""
	}
	host := parsed.Host
	path := parsed.Path

	// Нормализуем путь
	if path == "" || path == "/" {
		path = "/"
	}

	cleanPath := strings.TrimPrefix(path, "/")
	if cleanPath == "" {
		cleanPath = "index"
	}

	// Разделяем путь на части
	var parts []string
	if cleanPath != "" {
		parts = strings.Split(cleanPath, "/")
	}

	// Обрабатываем имя файла
	if len(parts) > 0 {
		lastPart := parts[len(parts)-1]

		// Убираем расширения для HTML страниц
		lowerLast := strings.ToLower(lastPart)
		if strings.HasSuffix(lowerLast, ".php") ||
			strings.HasSuffix(lowerLast, ".html") ||
			strings.HasSuffix(lowerLast, ".htm") ||
			strings.HasSuffix(lowerLast, ".asp") ||
			strings.HasSuffix(lowerLast, ".aspx") ||
			strings.HasSuffix(lowerLast, ".jsp") {

			// Убираем все возможные расширения
			ext := filepath.Ext(lastPart)
			newName := strings.TrimSuffix(lastPart, ext)

			if newName == "" || strings.EqualFold(newName, "index") {
				parts = parts[:len(parts)-1]
			} else {
				parts[len(parts)-1] = newName
			}
		} else if strings.EqualFold(lastPart, "index") {
			parts = parts[:len(parts)-1]
		}
	}

	// Строим путь сохранения
	basePath := filepath.Join(outputDir, host)

	var saveDir string
	if len(parts) > 0 {
		saveDir = filepath.Join(append([]string{basePath}, parts...)...)
	} else {
		saveDir = basePath
	}

	return saveDir, "index.html"
}

func (s *DirectoryIndexStrategy) RewriteLink(originalURL, baseURL string) string {
	parsed, err1 := url.Parse(originalURL)
	baseParsed, err2 := url.Parse(baseURL)

	if err1 != nil || err2 != nil {
		return originalURL
	}

	// Пропускаем внешние ссылки и специальные протоколы
	if parsed.Host != "" && parsed.Host != baseParsed.Host {
		return originalURL
	}

	if strings.HasPrefix(originalURL, "#") ||
		strings.HasPrefix(originalURL, "javascript:") ||
		strings.HasPrefix(originalURL, "mailto:") ||
		strings.HasPrefix(originalURL, "tel:") ||
		strings.HasPrefix(originalURL, "data:") {
		return originalURL
	}

	// Получаем пути
	sourcePath := baseParsed.Path
	targetPath := parsed.Path

	// Если пути одинаковые или целевой путь пустой
	if targetPath == "" || targetPath == "/" {
		parsed.Path = "/"
		return parsed.String()
	}

	// Обрабатываем относительные пути
	if !strings.HasPrefix(targetPath, "/") {
		// Относительный путь - оставляем как есть
		return originalURL
	}

	// Преобразуем расширения страниц
	lowerTarget := strings.ToLower(targetPath)
	pageExtensions := []string{".php", ".html", ".htm", ".asp", ".aspx", ".jsp"}

	for _, ext := range pageExtensions {
		if strings.HasSuffix(lowerTarget, ext) {
			// Убираем расширение
			newPath := strings.TrimSuffix(targetPath, ext)

			// Обработка index страниц
			if strings.HasSuffix(strings.ToLower(newPath), "/index") {
				newPath = strings.TrimSuffix(newPath, "/index")
			} else if strings.EqualFold(newPath, "index") {
				newPath = "/"
			}

			if newPath == "" {
				newPath = "/"
			}

			// Теперь вычисляем относительный путь от sourcePath к newPath
			if sourcePath != "/" && newPath != "/" {
				relativePath := calculateRelativePath(sourcePath, newPath)
				if relativePath != "" {
					parsed.Path = relativePath
					return parsed.String()
				}
			}

			parsed.Path = newPath
			return parsed.String()
		}
	}

	// Для путей без расширения тоже вычисляем относительный путь
	if !strings.Contains(targetPath, ".") {
		relativePath := calculateRelativePath(sourcePath, targetPath)
		if relativePath != "" && relativePath != targetPath {
			parsed.Path = relativePath
			return parsed.String()
		}
	}

	return originalURL
}

// Вспомогательная функция для вычисления относительного пути
func calculateRelativePath(fromPath, toPath string) string {
	// Нормализуем пути
	if fromPath == "" || fromPath == "/" {
		fromPath = "/"
	} else if !strings.HasSuffix(fromPath, "/") {
		// Если fromPath не заканчивается на /, берем его директорию
		fromPath = filepath.Dir(fromPath)
		if fromPath == "." {
			fromPath = "/"
		} else {
			fromPath = fromPath + "/"
		}
	}

	if toPath == "" || toPath == "/" {
		toPath = "/"
	} else if !strings.HasSuffix(toPath, "/") {
		toPath = toPath + "/"
	}

	// Разбиваем пути на части
	fromParts := strings.Split(strings.Trim(fromPath, "/"), "/")
	toParts := strings.Split(strings.Trim(toPath, "/"), "/")

	// Находим общую часть
	common := 0
	for i := 0; i < len(fromParts) && i < len(toParts); i++ {
		if fromParts[i] == toParts[i] {
			common++
		} else {
			break
		}
	}

	// Строим относительный путь
	var result strings.Builder

	// Добавляем переходы наверх из fromPath
	for i := common; i < len(fromParts); i++ {
		if result.Len() > 0 {
			result.WriteString("/")
		}
		result.WriteString("..")
	}

	// Добавляем оставшуюся часть toPath
	for i := common; i < len(toParts); i++ {
		if result.Len() > 0 {
			result.WriteString("/")
		}
		result.WriteString(toParts[i])
	}

	if result.Len() == 0 {
		return "./"
	}

	return result.String()
}

// FileOnlyStrategy - стратегия "просто файл" для ресурсов
type FileOnlyStrategy struct{}

func (s *FileOnlyStrategy) ShouldSaveAsDirectory(urlStr string, contentType string) bool {
	// Всегда сохраняем как файл
	return false
}

func (s *FileOnlyStrategy) GetSavePath(outputDir string, urlStr string, contentType string) (string, string) {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		log.Printf("Parse error in FileOnlyStrategy: %v", err)
		return "", ""
	}
	host := parsed.Host
	path := parsed.Path

	if path == "" || path == "/" {
		path = "/index.html"
	}

	cleanPath := strings.TrimPrefix(path, "/")
	if cleanPath == "" {
		cleanPath = "index.html"
	}

	saveDir := filepath.Join(outputDir, host, filepath.Dir(cleanPath))
	fileName := filepath.Base(cleanPath)

	return saveDir, fileName
}

func (s *FileOnlyStrategy) RewriteLink(originalURL, baseURL string) string {
	// Для ресурсов не переписываем ссылки
	return originalURL
}

// StrategyAnalyzer - анализатор для выбора стратегии
type StrategyAnalyzer struct {
	strategies []FileSaveStrategy
}

func NewStrategyAnalyzer() *StrategyAnalyzer {
	return &StrategyAnalyzer{
		strategies: []FileSaveStrategy{
			&DirectoryIndexStrategy{},
			&FileOnlyStrategy{},
		},
	}
}

func (a *StrategyAnalyzer) Analyze(urlStr string, contentType string, content []byte) FileSaveStrategy {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		// Если не можем распарсить URL, используем стратегию файлов
		return &FileOnlyStrategy{}
	}

	path := parsed.Path

	// Анализ 1: Проверяем расширение файла
	lowerPath := strings.ToLower(path)

	// Расширения ресурсов (сохраняем как файлы)
	resourceExtensions := []string{
		".css", ".js", ".mjs", ".cjs",
		".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp",
		".woff", ".woff2", ".ttf", ".eot", ".otf",
		".mp4", ".webm", ".mp3", ".wav", ".ogg", ".avi", ".mov",
		".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx",
		".zip", ".rar", ".7z", ".tar", ".gz",
		".json", ".xml", ".txt", ".csv",
	}

	for _, ext := range resourceExtensions {
		if strings.HasSuffix(lowerPath, ext) {
			return &FileOnlyStrategy{}
		}
	}

	// Анализ 2: Проверяем Content-Type
	if contentType != "" {
		// Ресурсные Content-Type
		resourceContentTypes := []string{
			"text/css",
			"application/javascript", "application/x-javascript",
			"image/", "font/", "audio/", "video/",
			"application/pdf", "application/zip",
			"application/json", "application/xml",
		}

		for _, ct := range resourceContentTypes {
			if strings.Contains(contentType, ct) {
				return &FileOnlyStrategy{}
			}
		}

		// HTML Content-Type
		if strings.Contains(contentType, "text/html") {
			return &DirectoryIndexStrategy{}
		}
	}

	// Анализ 3: Анализ содержимого (если Content-Type не указан)
	if contentType == "" || contentType == "application/octet-stream" {
		// Проверяем первые байты на наличие HTML тегов
		contentStr := string(content)
		if len(contentStr) > 100 {
			sample := strings.ToLower(contentStr[:100])
			if strings.Contains(sample, "<!doctype") ||
				strings.Contains(sample, "<html") ||
				strings.Contains(sample, "<head") ||
				strings.Contains(sample, "<body") {
				return &DirectoryIndexStrategy{}
			}
		}

		// Проверяем расширение в URL
		if strings.HasSuffix(lowerPath, ".php") ||
			strings.HasSuffix(lowerPath, ".html") ||
			strings.HasSuffix(lowerPath, ".htm") ||
			strings.HasSuffix(lowerPath, ".asp") ||
			strings.HasSuffix(lowerPath, ".aspx") ||
			strings.HasSuffix(lowerPath, ".jsp") {
			return &DirectoryIndexStrategy{}
		}
	}

	// Анализ 4: Паттерны путей
	// Если путь содержит типичные шаблоны для статических файлов
	staticPatterns := []string{
		"/static/", "/assets/", "/public/", "/resources/",
		"/css/", "/js/", "/images/", "/img/", "/fonts/",
		"/uploads/", "/media/", "/downloads/",
	}

	for _, pattern := range staticPatterns {
		if strings.Contains(path, pattern) {
			return &FileOnlyStrategy{}
		}
	}

	// Анализ 5: Пути без расширения
	if !strings.Contains(path, ".") && path != "/" && path != "" {
		// Это может быть либо страница (директория), либо API endpoint
		// Проверяем типичные паттерны API
		apiPatterns := []string{"/api/", "/ajax/", "/rest/", "/graphql", "/auth/"}
		for _, pattern := range apiPatterns {
			if strings.Contains(path, pattern) {
				return &FileOnlyStrategy{}
			}
		}

		// Если не API, то считаем страницей
		return &DirectoryIndexStrategy{}
	}

	// По умолчанию - стратегия директорий
	return &DirectoryIndexStrategy{}
}

type DefaultURLFilter struct {
	domain   string
	basePath string
}

func (f *DefaultURLFilter) ShouldDownload(u string) bool {
	parsed, err := url.Parse(u)
	if err != nil {
		return false
	}
	if parsed.Host != f.domain {
		return false
	}

	pathNoQ := strings.Split(parsed.Path, "?")[0]

	// Разрешаем файлы внутри BasePath
	if strings.HasPrefix(pathNoQ, f.basePath) {
		return true
	}

	// Разрешаем ресурсы из любых путей
	exts := []string{".css", ".js", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".ttf", ".webp", ".woff2"}
	for _, e := range exts {
		if strings.HasSuffix(strings.ToLower(parsed.Path), e) {
			return true
		}
	}

	// Разрешаем .php файлы из любых путей (для корректного скачивания)
	if strings.HasSuffix(strings.ToLower(parsed.Path), ".php") {
		return true
	}

	return false
}

func (f *DefaultURLFilter) FilterReason(u string) string {
	return "outside base path or not asset"
}

type LinkRewriterHandlerV2 struct {
	outputDir string
	analyzer  *StrategyAnalyzer
}

func (h *LinkRewriterHandlerV2) Priority() int { return 10 }

func (h *LinkRewriterHandlerV2) Handle(content []byte, meta FileMetadata) ([]byte, error) {
	// Пропускаем не-HTML контент
	if !strings.Contains(meta.ContentType, "text/html") {
		return content, nil
	}

	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		return content, nil
	}

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			for i := range n.Attr {
				attr := &n.Attr[i]
				if attr.Key == "href" || attr.Key == "src" || attr.Key == "action" {
					// Пропускаем пустые ссылки
					if attr.Val == "" {
						continue
					}

					// Для локальных файлов (file://) не трогаем
					if strings.HasPrefix(attr.Val, "file://") {
						continue
					}

					// Анализируем ссылку и выбираем стратегию
					strategy := h.analyzer.Analyze(attr.Val, "", nil)
					// Переписываем ссылку согласно стратегии
					newURL := strategy.RewriteLink(attr.Val, meta.URL)

					if newURL != attr.Val {
						// Дополнительная логика для относительных путей
						if !strings.Contains(newURL, "://") && !strings.HasPrefix(newURL, "/") {
							// Убедимся, что относительные пути правильные
							if strings.HasPrefix(newURL, "./") || strings.HasPrefix(newURL, "../") {
								attr.Val = newURL
							} else {
								// Добавляем ./ для локальных относительных ссылок
								attr.Val = "./" + newURL
							}
						} else {
							attr.Val = newURL
						}
						log.Printf("🔗 Rewrote link: %s → %s (from: %s)", attr.Val, newURL, meta.URL)
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			f(c)
		}
	}
	f(doc)

	var buf bytes.Buffer
	html.Render(&buf, doc)
	return buf.Bytes(), nil
}

// SaveFileV2 - универсальная функция сохранения с выбором стратегии
func SaveFileV2(outputDir, originalURL string, content []byte, ct string) (string, error) {
	analyzer := NewStrategyAnalyzer()
	strategy := analyzer.Analyze(originalURL, ct, content)

	// Получаем путь и имя файла от стратегии
	saveDir, fileName := strategy.GetSavePath(outputDir, originalURL, ct)
	if saveDir == "" || fileName == "" {
		return "", fmt.Errorf("failed to get save path for %s", originalURL)
	}

	// Создаем директорию
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		log.Printf("Mkdir error %s: %v", saveDir, err)
		return "", err
	}

	// Полный путь к файлу
	fullPath := filepath.Join(saveDir, fileName)

	// Сохраняем файл
	if err := ioutil.WriteFile(fullPath, content, 0644); err != nil {
		log.Printf("Write error %s: %v", fullPath, err)
		return "", err
	}

	log.Printf("✅ Saved [%T]: %s → %s", strategy, originalURL, fullPath)
	return fullPath, nil
}
func NormalizeURL(u string) (string, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return "", err
	}

	pu.Fragment = ""

	path := pu.Path
	if path == "" {
		path = "/"
	}

	// Normalize index.html/index.htm paths
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, "/index.html") || strings.HasSuffix(lower, "/index.htm") {
		path = strings.TrimSuffix(path, "/index.html")
		path = strings.TrimSuffix(path, "/index.htm")
		if path == "" {
			path = "/"
		}
	} else if strings.HasSuffix(lower, "index.html") || strings.HasSuffix(lower, "index.htm") {
		path = strings.TrimSuffix(path, "index.html")
		path = strings.TrimSuffix(path, "index.htm")
		if path == "" {
			path = "/"
		}
	}

	pu.Path = path

	result := pu.String()
	log.Printf("🔗 NormalizeURL: %s → %s", u, result)
	return result, nil
}

func ContentHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type Downloader struct {
	client    *http.Client
	retries   int
	delay     time.Duration
	maxSize   int64
	userAgent string
}

func NewDownloader(c Config) *Downloader {
	return &Downloader{
		client: &http.Client{
			Transport: &http.Transport{
				MaxIdleConns:    c.Workers * 2,
				IdleConnTimeout: 30 * time.Second,
			},
			CheckRedirect: func(r *http.Request, v []*http.Request) error {
				log.Printf("Redirect: %s → %s", v[len(v)-1].URL, r.URL)
				return nil
			},
			Timeout: 30 * time.Second,
		},
		retries:   c.Retries,
		delay:     c.Delay,
		maxSize:   c.MaxFileSize,
		userAgent: c.UserAgent,
	}
}

func (d *Downloader) Download(ctx context.Context, u string) ([]byte, string, error) {
	log.Printf("DOWNLOAD REQUEST: %s", u)

	for attempt := 1; attempt <= d.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			log.Printf("Request creation error for %s: %v", u, err)
			return nil, "", err
		}

		req.Header.Set("User-Agent", d.userAgent)
		req.Header.Set("Referer", "https://metanit.com/")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")

		resp, err := d.client.Do(req)
		if err != nil {
			log.Printf("HTTP error for %s (attempt %d): %v", u, attempt, err)
			if attempt == d.retries {
				return nil, "", ErrDownloadFailed
			}
			time.Sleep(d.delay + time.Duration(rand.Intn(1000))*time.Millisecond)
			continue
		}

		log.Printf("RESPONSE: %s → %d %s", u, resp.StatusCode, resp.Header.Get("Content-Type"))

		if resp.StatusCode != 200 {
			resp.Body.Close()
			if resp.StatusCode == 404 {
				log.Printf("❌ 404 Not Found: %s", u)
				return nil, "", fmt.Errorf("404 Not Found: %s", u)
			}
			log.Printf("HTTP error status %d for %s (attempt %d)", resp.StatusCode, u, attempt)

			if attempt == d.retries {
				return nil, "", fmt.Errorf("status %d", resp.StatusCode)
			}
			time.Sleep(d.delay + time.Duration(rand.Intn(1000))*time.Millisecond)
			continue
		}

		content, err := io.ReadAll(io.LimitReader(resp.Body, d.maxSize+1))
		resp.Body.Close()

		if err != nil {
			log.Printf("Read error for %s: %v", u, err)
			return nil, "", err
		}

		if len(content) > int(d.maxSize) {
			log.Printf("File too large: %s (%d bytes)", u, len(content))
			return nil, "", errors.New("file too large")
		}

		log.Printf("SUCCESS: Downloaded %s (%d bytes)", u, len(content))
		return content, resp.Header.Get("Content-Type"), nil
	}

	return nil, "", ErrDownloadFailed
}

type Job struct {
	ID         string
	RootURL    string
	Config     Config
	Filter     URLFilter
	Parsers    []ContentParser
	Handlers   []ContentHandler
	Downloader *Downloader
	BasePath   string

	mu           sync.Mutex
	pending      chan string
	visited      map[string]bool
	hashes       map[string]bool
	depths       map[string]int
	stats        JobStats
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	activeWG     sync.WaitGroup
	stateFile    string
	shutdownChan chan os.Signal
	Events       chan string
}

func (j *Job) progressReporter() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-j.ctx.Done():
			return
		case <-ticker.C:
			elapsed := time.Since(j.stats.StartTime).Seconds()
			speed := 0.0
			if elapsed > 0 {
				speed = float64(j.stats.DownloadedBytes) / elapsed
			}

			msg := fmt.Sprintf("Файлов: %d | Скорость: %.2f KB/s | В очереди: %d",
				j.stats.TotalFiles, speed/1024, len(j.pending))

			// Проверяем инициализацию канала (для совместимости с CLI)
			if j.Events != nil {
				select {
				case j.Events <- msg:
				default:
				}
			} else {
				log.Println(msg) // Fallback для CLI
			}
		}
	}
}
func NewJob(root string, cfg Config) (*Job, error) {
	parsed, err := url.Parse(root)
	if err != nil {
		return nil, err
	}

	id := ContentHash([]byte(root))[:8]
	stateFile := filepath.Join(cfg.OutputDir, id+StateFileExtension)

	filter := &DefaultURLFilter{
		domain:   parsed.Host,
		basePath: parsed.Path,
	}

	ctx, cancel := context.WithCancel(context.Background())

	job := &Job{
		ID:           id,
		RootURL:      root,
		Config:       cfg,
		Filter:       filter,
		Parsers:      []ContentParser{&HTMLParser{}, &CSSParser{}},
		Handlers:     []ContentHandler{&LinkRewriterHandlerV2{outputDir: cfg.OutputDir, analyzer: NewStrategyAnalyzer()}},
		Downloader:   NewDownloader(cfg),
		BasePath:     parsed.Path,
		pending:      make(chan string, 5000),
		visited:      make(map[string]bool),
		hashes:       make(map[string]bool),
		depths:       make(map[string]int),
		stats:        JobStats{FileTypes: make(map[string]int64), StartTime: time.Now()},
		ctx:          ctx,
		cancel:       cancel,
		stateFile:    stateFile,
		shutdownChan: make(chan os.Signal, 1),
		Events:       make(chan string, 100),
	}

	// Попытка загрузки состояния
	if err := job.loadState(); err == nil {
		log.Printf("✅ Resumed job %s from state file", id)
	} else {
		// Начинаем с корневого URL
		normalized, _ := NormalizeURL(root)
		job.activeWG.Add(1) // Добавляем в WaitGroup для rootURL
		job.pending <- normalized
		job.depths[normalized] = 0
		job.visited[normalized] = true
		log.Printf("🚀 New job started for %s", root)
	}

	return job, nil
}

func (j *Job) Run() {
	signal.Notify(j.shutdownChan, os.Interrupt, syscall.SIGTERM)

	// Обработчик завершения
	go func() {
		<-j.shutdownChan
		log.Println("⚠️  Shutdown signal received, saving state...")
		j.cancel()
	}()

	// ПЕРВЫМ запускаем репортер прогресса (для GUI)
	go j.progressReporter()

	// Запуск воркеров
	for i := 0; i < j.Config.Workers; i++ {
		j.wg.Add(1)
		go j.worker()
	}

	// Горутина закрытия канала при опустошении очереди
	go func() {
		j.activeWG.Wait()
		log.Println("📭 All tasks completed, closing pending channel")
		close(j.pending)
	}()

	// Ожидание завершения всех воркеров
	j.wg.Wait()

	// Отменяем контекст чтобы остановить progressReporter
	j.cancel()

	// Отправляем финальное сообщение в GUI
	if j.Events != nil {
		j.Events <- "✅ Download completed successfully!"
	}

	// Сохранение состояния
	if err := j.saveState(); err != nil {
		log.Printf("Error saving state: %v", err)
	}

	log.Println("✅ Download completed. All links rewritten for local viewing.")
}

func (j *Job) worker() {
	defer j.wg.Done()

	for urlStr := range j.pending {
		j.processURL(urlStr)
		j.activeWG.Done()
	}
}

func (j *Job) processURL(urlStr string) {
	depth := j.depths[urlStr]
	log.Printf("Processing: %s (depth %d)", urlStr, depth)

	if depth > j.Config.MaxDepth {
		atomic.AddInt64(&j.stats.Skipped, 1)
		log.Printf("Max depth reached for %s", urlStr)
		return
	}

	// Скачиваем файл - БЕЗ изменений URL!
	content, contentType, err := j.Downloader.Download(j.ctx, urlStr)
	if err != nil {
		log.Printf("Download failed for %s: %v", urlStr, err)
		atomic.AddInt64(&j.stats.Failed, 1)
		return
	}

	// Проверяем дубликаты по хешу
	hash := ContentHash(content)
	j.mu.Lock()
	if j.hashes[hash] {
		j.mu.Unlock()
		atomic.AddInt64(&j.stats.Skipped, 1)
		log.Printf("Duplicate content for %s", urlStr)
		return
	}
	j.hashes[hash] = true
	j.mu.Unlock()

	// Метаданные файла
	meta := FileMetadata{
		URL:         urlStr,
		ContentType: contentType,
		Hash:        hash,
		Depth:       depth,
	}

	// Переписываем ссылки в контенте для локального просмотра
	modifiedContent := content
	for _, handler := range j.sortedHandlers() {
		modified, err := handler.Handle(modifiedContent, meta)
		if err != nil {
			log.Printf("Handler error for %s: %v", urlStr, err)
		} else {
			modifiedContent = modified
		}
	}

	savedPath, err := SaveFileV2(j.Config.OutputDir, urlStr, modifiedContent, contentType)
	if err != nil {
		log.Printf("Save failed for %s: %v", urlStr, err)
		atomic.AddInt64(&j.stats.Failed, 1)
		return
	}

	// Обновляем статистику
	atomic.AddInt64(&j.stats.TotalFiles, 1)
	atomic.AddInt64(&j.stats.DownloadedBytes, int64(len(content)))

	j.mu.Lock()
	j.stats.FileTypes[contentType]++
	j.mu.Unlock()

	log.Printf("✅ Saved: %s → %s", urlStr, savedPath)

	// Парсим ссылки для дальнейшего скачивания (используем оригинальный контент!)
	if depth < j.Config.MaxDepth {
		j.parseAndQueueLinks(content, contentType, urlStr, depth)
	}
}

func (j *Job) parseAndQueueLinks(content []byte, contentType, baseURL string, depth int) {
	for _, parser := range j.Parsers {
		if parser.CanParse(contentType) {
			rawLinks, err := parser.Parse(content, baseURL)
			if err != nil {
				log.Printf("Parse error for %s: %v", baseURL, err)
				continue
			}

			log.Printf("Found %d raw links in %s", len(rawLinks), baseURL)

			for _, rawLink := range rawLinks {
				// Нормализуем URL (сохраняем оригинальные расширения)
				normalized, err := NormalizeURL(rawLink)
				if err != nil {
					continue
				}

				// Проверяем фильтры
				if !j.Filter.ShouldDownload(normalized) {
					reason := j.Filter.FilterReason(normalized)
					log.Printf("Filtered out: %s (%s)", normalized, reason)
					atomic.AddInt64(&j.stats.Skipped, 1)
					continue
				}

				// Добавляем в очередь
				j.mu.Lock()
				if !j.visited[normalized] {
					j.visited[normalized] = true
					j.depths[normalized] = depth + 1

					select {
					case j.pending <- normalized:
						j.activeWG.Add(1) // Увеличиваем счетчик только если успешно добавили в очередь
						log.Printf("Enqueued: %s (depth %d)", normalized, depth+1)
					default:
						// Если канал полон или уже закрыт, мы не добавляем в WaitGroup.
						// Нет нужды в j.activeWG.Done(), так как j.activeWG.Add(1) не вызывался.
						log.Printf("Queue full or closed, dropping: %s", normalized)
					}
				}
				j.mu.Unlock()
			}

			break // Обработали первый подходящий парсер
		}
	}
}

func (j *Job) sortedHandlers() []ContentHandler {
	handlers := make([]ContentHandler, len(j.Handlers))
	copy(handlers, j.Handlers)
	sort.Slice(handlers, func(i, k int) bool {
		return handlers[i].Priority() < handlers[k].Priority()
	})
	return handlers
}

func (j *Job) saveState() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	// Сливаем очередь в срез
	var pendingURLs []string
	for {
		select {
		case url := <-j.pending:
			pendingURLs = append(pendingURLs, url)
		default:
			// Пересоздаем канал после слива
			j.pending = make(chan string, 5000)
			for _, url := range pendingURLs {
				j.pending <- url
			}

			// Сохраняем состояние
			state := JobState{
				ID:          j.ID,
				RootURL:     j.RootURL,
				PendingURLs: pendingURLs,
				DepthMap:    j.depths,
				Stats:       j.stats,
				Config:      j.Config,
			}

			data, err := json.MarshalIndent(state, "", "  ")
			if err != nil {
				return err
			}

			return ioutil.WriteFile(j.stateFile, data, 0644)
		}
	}
}

func (j *Job) loadState() error {
	data, err := ioutil.ReadFile(j.stateFile)
	if err != nil {
		return err
	}

	var state JobState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}

	j.ID = state.ID
	j.RootURL = state.RootURL
	j.stats = state.Stats
	j.Config = state.Config

	j.mu.Lock()
	defer j.mu.Unlock()

	// Восстанавливаем глубину и посещенные URL
	j.depths = make(map[string]int)
	j.visited = make(map[string]bool)
	j.hashes = make(map[string]bool)

	for url, depth := range state.DepthMap {
		j.depths[url] = depth
		j.visited[url] = true
	}

	// Восстанавливаем очередь
	j.pending = make(chan string, 5000)
	for _, url := range state.PendingURLs {
		j.pending <- url
		j.activeWG.Add(1) // Добавляем в activeWG для каждого восстановленного URL
	}

	// Пересоздаем фильтр и парсеры
	parsed, _ := url.Parse(j.RootURL)
	j.Filter = &DefaultURLFilter{
		domain:   parsed.Host,
		basePath: parsed.Path,
	}
	j.BasePath = parsed.Path

	// ИСПРАВЛЕНО: Используем LinkRewriterHandlerV2 вместо LinkRewriterHandler
	j.Handlers = []ContentHandler{&LinkRewriterHandlerV2{
		outputDir: j.Config.OutputDir,
		analyzer:  NewStrategyAnalyzer(),
	}}
	j.Parsers = []ContentParser{&HTMLParser{}, &CSSParser{}}

	return nil
}

func drainChannel(ch chan string) []string {
	var urls []string
	for {
		select {
		case url := <-ch:
			urls = append(urls, url)
		default:
			return urls
		}
	}
}

// CLI команды
var rootCmd = &cobra.Command{
	Use:   "downloader",
	Short: "Website Downloader with .php to .html conversion",
}

var downloadCmd = &cobra.Command{
	Use:   "download <url>",
	Short: "Download a website",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfig()

		// Создаем выходную директорию
		if err := os.MkdirAll(cfg.OutputDir, 0755); err != nil {
			log.Fatalf("Failed to create output directory: %v", err)
		}

		job, err := NewJob(args[0], cfg)
		if err != nil {
			log.Fatalf("Failed to create job: %v", err)
		}

		job.Run()
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume <job-id>",
	Short: "Resume a previous download job",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		cfg := loadConfig()

		job := &Job{
			ID:        args[0],
			Config:    cfg,
			stateFile: filepath.Join(cfg.OutputDir, args[0]+StateFileExtension),
		}

		if err := job.loadState(); err != nil {
			log.Fatalf("Failed to load job state: %v", err)
		}

		// Восстанавливаем контекст и каналы
		job.ctx, job.cancel = context.WithCancel(context.Background())
		job.shutdownChan = make(chan os.Signal, 1)

		// Пересоздаем загрузчик
		job.Downloader = NewDownloader(cfg)

		// ДОБАВЬТЕ: Восстанавливаем обработчики
		job.Handlers = []ContentHandler{&LinkRewriterHandlerV2{
			outputDir: cfg.OutputDir,
			analyzer:  NewStrategyAnalyzer(),
		}}

		log.Printf("Resuming job %s for %s", job.ID, job.RootURL)
		job.Run()
	},
}

func loadConfig() Config {
	// Значения по умолчанию
	viper.SetDefault("workers", DefaultWorkers)
	viper.SetDefault("max_depth", DefaultMaxDepth)
	viper.SetDefault("retries", DefaultRetries)
	viper.SetDefault("delay", DefaultDelay)
	viper.SetDefault("max_file_size", DefaultMaxFileSize)
	viper.SetDefault("output_dir", "./downloads")
	viper.SetDefault("user_agent", DefaultUserAgent)

	// Чтение конфигурационного файла
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.ReadInConfig() // Игнорируем ошибку если файла нет

	return Config{
		Workers:     viper.GetInt("workers"),
		MaxDepth:    viper.GetInt("max_depth"),
		Retries:     viper.GetInt("retries"),
		Delay:       viper.GetDuration("delay"),
		MaxFileSize: viper.GetInt64("max_file_size"),
		OutputDir:   viper.GetString("output_dir"),
		UserAgent:   viper.GetString("user_agent"),
	}
}

func init() {
	// Флаги для команды download
	downloadCmd.Flags().Int("workers", DefaultWorkers, "Number of concurrent workers")
	downloadCmd.Flags().Int("max-depth", DefaultMaxDepth, "Maximum recursion depth")
	downloadCmd.Flags().Int("retries", DefaultRetries, "Retry attempts per URL")
	downloadCmd.Flags().Duration("delay", DefaultDelay, "Delay between requests")
	downloadCmd.Flags().Int64("max-file-size", DefaultMaxFileSize, "Maximum file size in bytes")
	downloadCmd.Flags().String("output-dir", "./downloads", "Output directory")
	downloadCmd.Flags().String("user-agent", DefaultUserAgent, "HTTP User-Agent header")

	// Привязка флагов к viper
	viper.BindPFlags(downloadCmd.Flags())

	// Добавление команд
	rootCmd.AddCommand(downloadCmd, resumeCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
