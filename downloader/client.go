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
	"path"
    "path/filepath"
	"net/url"
	"os"
	"os/signal"
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
	DefaultWorkers     = 5 // Снижаем с 10 до 5 для экономии памяти
	DefaultMaxDepth    = 10
	DefaultRetries     = 3
	DefaultDelay       = 500 * time.Millisecond
	DefaultMaxFileSize = 10 * 1024 * 1024 // 10MB
	DefaultUserAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"
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

    // ВАЖНО: calculateRelativePath теперь вызывается для ПОЛНЫХ URL
    // Это позволит функции внутри понимать, где файлы лежат на диске.

    // Формируем абсолютный URL для цели, если он относительный
    targetURL := originalURL
    if !strings.HasPrefix(originalURL, "http") {
        resolved := baseParsed.ResolveReference(parsed)
        targetURL = resolved.String()
    }

    // 1. Вычисляем относительный путь
    relPath, err := calculateRelativePath(baseURL, targetURL)
    if err != nil {
        return originalURL
    }

    // 2. Дополнительная логика для DirectoryIndex (убираем .html/index.html из ссылок)
    // Чтобы ссылки в браузере выглядели как href="../assets/" вместо "../assets/index.html"

    // Убираем "index.html" из конца, если он там есть
    if strings.HasSuffix(relPath, "index.html") {
        relPath = strings.TrimSuffix(relPath, "index.html")
    }

    // Если путь стал пустым (ссылка на ту же папку), ставим "./"
    if relPath == "" {
        relPath = "./"
    }

    // Сохраняем только путь, сохраняя Query-параметры (?v=1.2), если они были
    parsed.Path = relPath
    parsed.Scheme = "" // Делаем ссылку относительной
    parsed.Host = ""

    return parsed.String()
}

// Функция для вычисления относительного пути между двумя URL
func calculateRelativePath(sourceURL, targetURL string) (string, error) {
    s, err := url.Parse(sourceURL)
    t, err := url.Parse(targetURL)
    if err != nil {
        return targetURL, err
    }

    // Если домены разные, оставляем абсолютную ссылку
    if s.Host != t.Host {
        return targetURL, nil
    }

    // Определяем "пути" на диске для обоих файлов
    sourcePath := getDiskPath(s)
    targetPath := getDiskPath(t)

    // Вычисляем относительный путь из папки источника к файлу цели
    rel, err := filepath.Rel(filepath.Dir(sourcePath), targetPath)
    if err != nil {
        return targetURL, err
    }

    // Превращаем системные разделители (\ в Windows) в URL-разделители (/)
    return filepath.ToSlash(rel), nil
}

// Вспомогательная функция, которая повторяет логику SaveFileV2
func getDiskPath(u *url.URL) string {
    p := u.Path
    if p == "" || p == "/" {
        return "index.html"
    }

    // Очищаем путь от двойных слэшей и лишних элементов
    p = path.Clean(p)
    if p == "." {
        return "index.html"
    }

    // Убираем начальный слэш, чтобы filepath.Join не считал путь абсолютным
    p = strings.TrimPrefix(p, "/")

    // Если это папка (URL заканчивается на /) или страница без расширения
    // проверяем наличие точки в последнем сегменте пути
    lastSegment := path.Base(p)
    if strings.HasSuffix(u.Path, "/") || !strings.Contains(lastSegment, ".") {
        // Если это php, превращаем в html, иначе делаем index.html внутри папки
        if strings.HasSuffix(strings.ToLower(p), ".php") {
            return strings.TrimSuffix(p, ".php") + ".html"
        }
        return path.Join(p, "index.html")
    }

    return p
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

    // 1. Проверка домена (не скачиваем внешние сайты)
    if parsed.Host != f.domain {
        return false
    }

    pathLower := strings.ToLower(parsed.Path)

    // 2. Список расширений статических ресурсов (ассетов)
    assetExts := []string{
        ".css", ".js", ".mjs", ".json", ".map",
        ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".webp", ".avif",
        ".woff", ".woff2", ".ttf", ".otf", ".eot",
        ".mp4", ".webm", ".mp3", ".wav", ".pdf",
    }

    // Если это статический ассет — разрешаем скачивание из любого места на этом домене
    for _, ext := range assetExts {
        if strings.HasSuffix(pathLower, ext) {
            return true
        }
    }

    // 3. Проверка для страниц (HTML, PHP или URL без расширения)
    // Разрешаем, только если они находятся внутри базовой папки (basePath)
    isPage := strings.HasSuffix(pathLower, ".html") ||
              strings.HasSuffix(pathLower, ".php") ||
              !strings.Contains(filepath.Base(pathLower), ".")

    if isPage {
        return strings.HasPrefix(parsed.Path, f.basePath)
    }

    // По умолчанию разрешаем всё остальное, что не попало в фильтр страниц,
    // но находится на нашем домене (на всякий случай)
    return true
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

func SaveFileV2(outputDir string, urlStr string, data []byte, contentType string) (string, error) {
    parsed, err := url.Parse(urlStr)
    if err != nil || parsed.Host == "" {
        return "", fmt.Errorf("invalid URL or empty host")
    }

    // Получаем путь внутри домена
    relDiskPath := getDiskPath(parsed)

    // Собираем: output/wails.io/ru/index.html
    fullPath := filepath.Join(outputDir, parsed.Host, relDiskPath)

    if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
        return "", err
    }

    err = os.WriteFile(fullPath, data, 0644)
    if err != nil {
        return "", err
    }

    return relDiskPath, nil
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

		// Используем домен целевого URL в качестве Referer (более надежно)
		parsed, _ := url.Parse(u)
		req.Header.Set("Referer", parsed.Scheme+"://"+parsed.Host+"/")
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

			j.sendLog(msg, false)
		}
	}
}

func (j *Job) sendLog(msg string, terminalOnly bool) {
	if !terminalOnly && j.Events != nil {
		select {
		case j.Events <- msg:
		default:
		}
	}
	log.Println(msg)
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
	if j.Events != nil {
		defer close(j.Events)
	}
	signal.Notify(j.shutdownChan, os.Interrupt, syscall.SIGTERM)

	// Обработчик завершения
	defer func() {
		j.wg.Wait()
		j.sendLog("📭 All tasks completed, closing pending channel", false)
		j.cancel()

		if j.Events != nil {
			j.Events <- "✅ Download completed successfully!"
		}

		if err := j.saveState(); err != nil {
			log.Printf("Error saving state: %v", err)
		}
		log.Println("✅ Download completed. All links rewritten for local viewing.")
	}()

	// ПЕРВЫМ запускаем репортер прогресса (для GUI)
	go j.progressReporter()

	// Первичная очередь
	normalized, _ := NormalizeURL(j.RootURL)
	j.mu.Lock()
	j.depths[normalized] = 0
	j.visited[normalized] = true
	j.mu.Unlock()

	// Discover common files (404, robots, etc.)
	j.discoverCommonFiles()

	j.activeWG.Add(1)
	j.pending <- normalized

	// Запуск воркеров
	for i := 0; i < j.Config.Workers; i++ {
		j.wg.Add(1)
		go j.worker()
	}

	j.activeWG.Wait()
	close(j.pending)
	j.wg.Wait()
}

func (j *Job) discoverCommonFiles() {
	commonPaths := []string{
		"/404", "/404.html", "/robots.txt", "/sitemap.xml", "/favicon.ico",
		"/apple-touch-icon.png", "/manifest.json",
	}

	parsed, err := url.Parse(j.RootURL)
	if err != nil {
		return
	}
	baseURL := parsed.Scheme + "://" + parsed.Host

	for _, p := range commonPaths {
		targetURL := baseURL + p
		j.mu.Lock()
		if _, exists := j.depths[targetURL]; !exists {
			j.depths[targetURL] = 0 // Treat as root level
			j.mu.Unlock()
			j.activeWG.Add(1)
			select {
			case j.pending <- targetURL:
				j.sendLog(fmt.Sprintf("[Discovery] Queued common file: %s", p), false)
			default:
				j.activeWG.Done()
			}
		} else {
			j.mu.Unlock()
		}
	}
}

func (j *Job) worker() {
	defer j.wg.Done()

	for urlStr := range j.pending {
		j.processURL(urlStr)
		j.activeWG.Done()
	}
}

func (j *Job) processURL(urlStr string) {
    j.mu.Lock()
    depth := j.depths[urlStr]
    j.mu.Unlock()

    // Проверяем, что URL валидный перед скачиванием
    if !strings.HasPrefix(urlStr, "http") {
        j.sendLog(fmt.Sprintf("[Error] Invalid URL format: %s", urlStr), false)
        return
    }

    j.sendLog(fmt.Sprintf("[Info] Processing: %s (depth %d)", urlStr, depth), false)

    if depth > j.Config.MaxDepth {
        atomic.AddInt64(&j.stats.Skipped, 1)
        return
    }

    content, contentType, err := j.Downloader.Download(j.ctx, urlStr)
    if err != nil {
        j.sendLog(fmt.Sprintf("[Error] Failed to download %s: %v", urlStr, err), false)
        atomic.AddInt64(&j.stats.Failed, 1)
        return
    }

    // Хеши отключены, как мы и договаривались, чтобы сохранить структуру /ru/assets/
    hash := ContentHash(content)

    meta := FileMetadata{
        URL:         urlStr,
        ContentType: contentType,
        Hash:        hash,
        Depth:       depth,
    }

    modifiedContent := content
    for _, handler := range j.sortedHandlers() {
        modified, err := handler.Handle(modifiedContent, meta)
        if err != nil {
            log.Printf("Handler error for %s: %v", urlStr, err)
        } else {
            modifiedContent = modified
        }
    }

    // Сохраняем файл
    _, err = SaveFileV2(j.Config.OutputDir, urlStr, modifiedContent, contentType)
    if err != nil {
        j.sendLog(fmt.Sprintf("[Error] Save failed for %s: %v", urlStr, err), false)
        atomic.AddInt64(&j.stats.Failed, 1)
        return
    }

    atomic.AddInt64(&j.stats.TotalFiles, 1)
    atomic.AddInt64(&j.stats.DownloadedBytes, int64(len(content)))
    j.sendLog(fmt.Sprintf("[Done] Saved: %s", urlStr), false)

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
                normalized, err := NormalizeURL(rawLink)
                if err != nil {
                    continue
                }

                // Проверяем фильтры
                if !j.Filter.ShouldDownload(normalized) {
                    // Можно раскомментировать для отладки фильтрации:
                    // reason := j.Filter.FilterReason(normalized)
                    // log.Printf("Filtered out: %s (%s)", normalized, reason)
                    continue
                }

                j.mu.Lock()
                if !j.visited[normalized] {
                    j.visited[normalized] = true
                    j.depths[normalized] = depth + 1

                    // Увеличиваем счетчик ДО разблокировки и отправки
                    j.activeWG.Add(1)
                    j.mu.Unlock()

                    // Отправляем в очередь. Если канал полон — ждем.
                    select {
                    case j.pending <- normalized:
                        // Успешно добавлено
                    case <-j.ctx.Done():
                        // Если программа завершается, откатываем счетчик
                        j.activeWG.Done()
                        return
                    }
                } else {
                    j.mu.Unlock()
                }
            }
            break // Используем только первый подходящий парсер
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

			// Используем Marshal вместо MarshalIndent для экономии памяти и места
			data, err := json.Marshal(state)
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
