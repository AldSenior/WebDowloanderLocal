package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

// ==================== КОНФИГУРАЦИЯ ====================

// PostProcessorConfig - конфигурация постпроцессора
type PostProcessorConfig struct {
	InputDir      string // Папка со скачанным сайтом
	OutputDir     string // Папка для результатов
	OriginalHost  string // Оригинальный хост (example.com)
	SiteRootPath  string // Корневой путь сайта (/blog/)
	Workers       int    // Количество воркеров
	KeepExternal  bool   // Сохранять ссылки на внешние ресурсы
	RemoveMissing bool   // Удалять не найденные ресурсы
	ConvertPhp    bool   // Конвертировать .php в .html
	Verbose       bool   // Подробный вывод
	Debug         bool   // Отладочный вывод
}

// PostProcessor - многопоточный постпроцессор с HTML парсером
type PostProcessor struct {
	config         PostProcessorConfig
	fileQueue      chan string
	wg             sync.WaitGroup
	stats          PostProcessorStats
	siteStructure  *SiteStructure
	linkProcessor  *LinkProcessor
	cssProcessor   *CSSProcessor
	processedFiles sync.Map // Для отслеживания обработанных файлов
}

// PostProcessorStats - статистика обработки
type PostProcessorStats struct {
	TotalFiles      int64
	Processed       int64
	Modified        int64
	Failed          int64
	LinksRewritten  int64
	ExternalLinks   int64
	LocalCopiesMade int64
	StartTime       time.Time
	Duration        time.Duration
}

// SiteStructure - структура сайта для быстрого поиска файлов
type SiteStructure struct {
	mu            sync.RWMutex
	urlToFilePath map[string]string // URL путь → локальный файл
	filePathToURL map[string]string // Локальный файл → URL путь
	allFiles      map[string]string // Все файлы для поиска (базовое имя → полный путь)
}

// LinkProcessor - обработчик ссылок
type LinkProcessor struct {
	siteStructure *SiteStructure
	config        *PostProcessorConfig
	stats         *PostProcessorStats
}

// CSSProcessor - обработчик CSS
type CSSProcessor struct {
	linkProcessor *LinkProcessor
}

// NewPostProcessor создает новый постпроцессор
func NewPostProcessor(config PostProcessorConfig) *PostProcessor {
	if config.Workers <= 0 {
		config.Workers = runtime.NumCPU() * 2
	}

	if config.OutputDir == "" {
		config.OutputDir = config.InputDir
	}

	if config.SiteRootPath == "" {
		config.SiteRootPath = "/"
	}

	return &PostProcessor{
		config:    config,
		fileQueue: make(chan string, 10000),
		stats:     PostProcessorStats{},
	}
}

// ==================== ОСНОВНОЙ ЦИКЛ ====================

// Run запускает многопоточную обработку
func (p *PostProcessor) Run() error {
	p.stats.StartTime = time.Now()
	defer func() { p.stats.Duration = time.Since(p.stats.StartTime) }()

	log.Printf("🚀 Запуск постпроцессора с HTML парсером")
	log.Printf("📁 Входная папка: %s", p.config.InputDir)
	log.Printf("📁 Выходная папка: %s", p.config.OutputDir)
	log.Printf("🌐 Исходный хост: %s", p.config.OriginalHost)
	log.Printf("📍 Корень сайта: %s", p.config.SiteRootPath)
	log.Printf("👷 Воркеров: %d", p.config.Workers)

	// Проверяем существование входной директории
	if _, err := os.Stat(p.config.InputDir); os.IsNotExist(err) {
		return fmt.Errorf("входная директория не существует: %s", p.config.InputDir)
	}

	// Создаем выходную директорию если нужно
	if p.config.OutputDir != p.config.InputDir {
		if err := os.MkdirAll(p.config.OutputDir, 0755); err != nil {
			return fmt.Errorf("не удалось создать выходную директорию: %v", err)
		}
	}

	// Инициализируем структуру сайта
	if err := p.initSiteStructure(); err != nil {
		return fmt.Errorf("ошибка инициализации структуры сайта: %v", err)
	}

	// Инициализируем процессоры
	p.linkProcessor = &LinkProcessor{
		siteStructure: p.siteStructure,
		config:        &p.config,
		stats:         &p.stats,
	}

	p.cssProcessor = &CSSProcessor{
		linkProcessor: p.linkProcessor,
	}

	// Запускаем сбор файлов
	go p.collectFiles()

	// Запускаем воркеров
	for i := 0; i < p.config.Workers; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	// Ждем завершения
	p.wg.Wait()

	// Выводим статистику
	p.printStats()

	return nil
}

// initSiteStructure сканирует структуру сайта
func (p *PostProcessor) initSiteStructure() error {
	p.siteStructure = &SiteStructure{
		urlToFilePath: make(map[string]string),
		filePathToURL: make(map[string]string),
		allFiles:      make(map[string]string),
	}

	log.Printf("🔍 Сканирование структуры сайта...")

	err := filepath.Walk(p.config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("⚠️  Ошибка доступа к %s: %v", path, err)
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Добавляем в список всех файлов для поиска
		baseName := filepath.Base(path)
		p.siteStructure.allFiles[baseName] = path

		// Добавляем варианты с разными путями
		relPath, _ := filepath.Rel(p.config.InputDir, path)
		p.siteStructure.allFiles[relPath] = path
		p.siteStructure.allFiles[filepath.ToSlash(relPath)] = path

		// Определяем URL путь для файла
		urlPath := p.filePathToURLPath(path)

		p.siteStructure.mu.Lock()
		p.siteStructure.urlToFilePath[urlPath] = path
		p.siteStructure.filePathToURL[path] = urlPath
		p.siteStructure.mu.Unlock()

		return nil
	})

	log.Printf("📊 Структура сайта: %d файлов проиндексировано", len(p.siteStructure.urlToFilePath))

	return err
}

// filePathToURLPath преобразует путь файла в URL путь
func (p *PostProcessor) filePathToURLPath(filePath string) string {
	// Относительный путь от входной директории
	relPath, err := filepath.Rel(p.config.InputDir, filePath)
	if err != nil {
		relPath = filepath.Base(filePath)
	}

	// Нормализуем разделители
	relPath = filepath.ToSlash(relPath)

	// Убираем расширение .html/.htm для красивых URL
	if strings.HasSuffix(strings.ToLower(relPath), "index.html") {
		relPath = strings.TrimSuffix(relPath, "/index.html") + "/"
		relPath = strings.TrimSuffix(relPath, "index.html") + "/"
	} else if strings.HasSuffix(strings.ToLower(relPath), "index.htm") {
		relPath = strings.TrimSuffix(relPath, "/index.htm") + "/"
		relPath = strings.TrimSuffix(relPath, "index.htm") + "/"
	} else if strings.HasSuffix(strings.ToLower(relPath), ".html") {
		relPath = strings.TrimSuffix(relPath, ".html")
	} else if strings.HasSuffix(strings.ToLower(relPath), ".htm") {
		relPath = strings.TrimSuffix(relPath, ".htm")
	} else if strings.HasSuffix(strings.ToLower(relPath), ".php") && p.config.ConvertPhp {
		relPath = strings.TrimSuffix(relPath, ".php")
	}

	// Добавляем корневой путь
	urlPath := p.config.SiteRootPath + strings.TrimPrefix(relPath, "/")
	if !strings.HasSuffix(urlPath, "/") && !strings.Contains(filepath.Base(urlPath), ".") {
		urlPath += "/"
	}

	return strings.TrimSuffix(urlPath, "/")
}

// ==================== СБОР ФАЙЛОВ ====================

// collectFiles собирает все файлы для обработки
func (p *PostProcessor) collectFiles() {
	defer close(p.fileQueue)

	filepath.Walk(p.config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("⚠️  Ошибка доступа к %s: %v", path, err)
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Проверяем расширение файла
		ext := strings.ToLower(filepath.Ext(path))
		shouldProcess := false

		switch ext {
		case ".html", ".htm", ".xhtml", ".php":
			shouldProcess = true
		case ".css", ".scss", ".less":
			shouldProcess = true
		case ".js":
			shouldProcess = true
		}

		if shouldProcess {
			atomic.AddInt64(&p.stats.TotalFiles, 1)
			p.fileQueue <- path
		}

		return nil
	})

	log.Printf("📂 Найдено файлов для обработки: %d", atomic.LoadInt64(&p.stats.TotalFiles))
}

// ==================== ВОРКЕРЫ ====================

// worker обрабатывает файлы
func (p *PostProcessor) worker(id int) {
	defer p.wg.Done()

	for filePath := range p.fileQueue {
		p.processFile(filePath, id)
	}
}

func (p *PostProcessor) processFile(filePath string, workerID int) {
	atomic.AddInt64(&p.stats.Processed, 1)

	// Помечаем файл как обрабатываемый
	if _, loaded := p.processedFiles.LoadOrStore(filePath, true); loaded {
		return // Файл уже обрабатывается
	}
	defer p.processedFiles.Delete(filePath)

	// Определяем выходной путь
	outputPath := filePath
	if p.config.OutputDir != p.config.InputDir {
		relPath, err := filepath.Rel(p.config.InputDir, filePath)
		if err != nil {
			relPath = filepath.Base(filePath)
		}
		outputPath = filepath.Join(p.config.OutputDir, relPath)
	}

	// Определяем тип файла
	ext := strings.ToLower(filepath.Ext(filePath))

	// Проверяем, нужно ли конвертировать PHP в HTML
	shouldConvert := (ext == ".php" && p.config.ConvertPhp)

	if shouldConvert {
		outputPath = strings.TrimSuffix(outputPath, ".php") + ".html"
	}

	// Создаем директорию если нужно
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		log.Printf("[Worker %d] Ошибка создания директории %s: %v",
			workerID, filepath.Dir(outputPath), err)
		atomic.AddInt64(&p.stats.Failed, 1)
		return
	}

	// Обрабатываем файл в зависимости от типа
	var modified bool
	var err error

	switch {
	case ext == ".html" || ext == ".htm" || ext == ".xhtml" || (ext == ".php" && !shouldConvert):
		modified, err = p.processHTMLFile(filePath, outputPath)
	case shouldConvert:
		// Конвертируем PHP в HTML
		modified, err = p.convertPHPToHTML(filePath, outputPath)
	case ext == ".css" || ext == ".scss" || ext == ".less":
		modified, err = p.processCSSFile(filePath, outputPath)
	case ext == ".js":
		modified, err = p.processJSFile(filePath, outputPath)
	default:
		// Просто копируем файл
		if filePath != outputPath {
			err = p.copyFile(filePath, outputPath)
		}
	}

	if err != nil {
		log.Printf("[Worker %d] Ошибка обработки %s: %v", workerID, filePath, err)
		atomic.AddInt64(&p.stats.Failed, 1)
		return
	}

	if modified {
		atomic.AddInt64(&p.stats.Modified, 1)
	}

	// Если создали новый файл .html, удаляем старый .php файл
	if shouldConvert && p.config.OutputDir == p.config.InputDir {
		if err := os.Remove(filePath); err != nil {
			log.Printf("[Worker %d] Ошибка удаления старого файла %s: %v",
				workerID, filePath, err)
		}
	}
}

// ==================== ОБРАБОТКА HTML С ПАРСЕРОМ ====================

// processHTMLFile обрабатывает HTML файлы с использованием парсера
func (p *PostProcessor) processHTMLFile(inputPath, outputPath string) (bool, error) {
	// Читаем файл
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return false, err
	}

	originalContent := string(content)

	if p.config.Debug {
		log.Printf("🔧 Обработка HTML файла: %s", inputPath)
	}

	// Парсим HTML
	doc, err := html.Parse(bytes.NewReader(content))
	if err != nil {
		log.Printf("⚠️  Ошибка парсинга HTML файла %s, используем fallback: %v", inputPath, err)
		// Fallback к regex обработке если парсер не справился
		return p.fallbackProcessHTML(originalContent, outputPath, inputPath)
	}

	// Обрабатываем все ссылки в DOM дереве
	modified := p.traverseAndRewriteHTML(doc, inputPath)

	// Очищаем ненужные мета-теги
	p.cleanMetaTags(doc)

	// Если были изменения, сохраняем файл
	if modified {
		var buf bytes.Buffer
		html.Render(&buf, doc)

		result := buf.String()

		// Дополнительная обработка PHP ссылок
		if p.config.ConvertPhp {
			result = p.updatePHPLinks(result)
		}

		err = os.WriteFile(outputPath, []byte(result), 0644)
		if err != nil {
			return false, err
		}

		if p.config.Verbose {
			log.Printf("✅ Изменен: %s -> %s", inputPath, outputPath)
		}
	} else if inputPath != outputPath {
		// Просто копируем файл если не было изменений
		err = os.WriteFile(outputPath, content, 0644)
		if err != nil {
			return false, err
		}
	}

	return modified, nil
}

// traverseAndRewriteHTML рекурсивно обходит DOM и заменяет ссылки
func (p *PostProcessor) traverseAndRewriteHTML(node *html.Node, filePath string) bool {
	modified := false

	// Обрабатываем текущий узел
	if node.Type == html.ElementNode {
		if p.processHTMLNode(node, filePath) {
			modified = true
		}
	}

	// Рекурсивно обрабатываем детей
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if p.traverseAndRewriteHTML(child, filePath) {
			modified = true
		}
	}

	return modified
}

// processHTMLNode обрабатывает атрибуты HTML элемента
func (p *PostProcessor) processHTMLNode(node *html.Node, filePath string) bool {
	modified := false

	// Список атрибутов, содержащих ссылки
	linkAttributes := map[string]bool{
		"href":       true,
		"src":        true,
		"action":     true,
		"data-src":   true,
		"data-href":  true,
		"poster":     true,
		"srcset":     true,
		"cite":       true,
		"formaction": true,
		"icon":       true,
		"manifest":   true,
		"archive":    true,
		"codebase":   true,
		"data":       true,
		"usemap":     true,
		"background": true,
		"content":    true, // для meta тегов
	}

	// Обрабатываем каждый атрибут
	for i, attr := range node.Attr {
		if linkAttributes[attr.Key] {
			newURL := p.linkProcessor.ProcessURL(attr.Val, filePath)
			if newURL != attr.Val {
				if p.config.Debug {
					log.Printf("  🔄 Замена ссылки: %s -> %s", attr.Val, newURL)
				}
				node.Attr[i].Val = newURL
				atomic.AddInt64(&p.stats.LinksRewritten, 1)
				modified = true
			}
		}

		// Особый случай: srcset может содержать несколько URL
		if attr.Key == "srcset" {
			newSrcset := p.processSrcset(attr.Val, filePath)
			if newSrcset != attr.Val {
				node.Attr[i].Val = newSrcset
				atomic.AddInt64(&p.stats.LinksRewritten, 1)
				modified = true
			}
		}
	}

	return modified
}

// processSrcset обрабатывает атрибут srcset
func (p *PostProcessor) processSrcset(srcset, filePath string) string {
	parts := strings.Split(srcset, ",")
	processedParts := make([]string, 0, len(parts))

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		// Разделяем URL и дескриптор (например, "1x", "2x", "100w")
		subparts := strings.Fields(part)
		if len(subparts) > 0 {
			url := subparts[0]
			newURL := p.linkProcessor.ProcessURL(url, filePath)

			if len(subparts) > 1 {
				processedParts = append(processedParts, newURL+" "+subparts[1])
			} else {
				processedParts = append(processedParts, newURL)
			}
		}
	}

	return strings.Join(processedParts, ", ")
}

// cleanMetaTags очищает ненужные мета-теги
func (p *PostProcessor) cleanMetaTags(doc *html.Node) {
	p.traverseAndCleanMeta(doc)
}

// traverseAndCleanMeta рекурсивно очищает мета-теги
func (p *PostProcessor) traverseAndCleanMeta(node *html.Node) {
	if node.Type == html.ElementNode {
		// Удаляем определенные мета-теги
		if node.Data == "meta" {
			var remove bool
			for _, attr := range node.Attr {
				if attr.Key == "http-equiv" && attr.Val == "refresh" {
					remove = true
					break
				}
				if attr.Key == "property" && strings.HasPrefix(attr.Val, "og:") {
					// Проверяем content на внешние ссылки
					for _, attr2 := range node.Attr {
						if attr2.Key == "content" && strings.Contains(attr2.Val, p.config.OriginalHost) {
							remove = true
							break
						}
					}
				}
			}

			if remove {
				// Удаляем узел
				if node.Parent != nil {
					node.Parent.RemoveChild(node)
				}
				return
			}
		}

		// Удаляем link теги с внешними ссылками
		if node.Data == "link" {
			var remove bool
			for _, attr := range node.Attr {
				if (attr.Key == "rel" && (attr.Val == "canonical" || attr.Val == "shortcut icon")) ||
					(attr.Key == "href" && strings.Contains(attr.Val, p.config.OriginalHost)) {
					remove = true
					break
				}
			}

			if remove {
				if node.Parent != nil {
					node.Parent.RemoveChild(node)
				}
				return
			}
		}
	}

	// Рекурсивно обрабатываем детей
	for child := node.FirstChild; child != nil; {
		next := child.NextSibling
		p.traverseAndCleanMeta(child)
		child = next
	}
}

// fallbackProcessHTML fallback обработка HTML при ошибке парсера
func (p *PostProcessor) fallbackProcessHTML(content, outputPath, filePath string) (bool, error) {
	// Используем regex только как fallback
	modifiedContent := content

	// Убираем протокол и хост из всех ссылок
	if p.config.OriginalHost != "" {
		hostPatterns := []string{
			"https?://" + regexp.QuoteMeta(p.config.OriginalHost),
			"//" + regexp.QuoteMeta(p.config.OriginalHost),
		}

		for _, pattern := range hostPatterns {
			re := regexp.MustCompile(pattern + `([^'"\s>]*)`)
			modifiedContent = re.ReplaceAllStringFunc(modifiedContent, func(match string) string {
				// Извлекаем путь после хоста
				path := strings.TrimPrefix(match, "http://"+p.config.OriginalHost)
				path = strings.TrimPrefix(path, "https://"+p.config.OriginalHost)
				path = strings.TrimPrefix(path, "//"+p.config.OriginalHost)

				// Обрабатываем путь через linkProcessor
				return p.linkProcessor.ProcessURL(path, filePath)
			})
		}
	}

	// Заменяем ссылки на .php файлы
	if p.config.ConvertPhp {
		modifiedContent = p.updatePHPLinks(modifiedContent)
	}

	if modifiedContent != content {
		err := os.WriteFile(outputPath, []byte(modifiedContent), 0644)
		if p.config.Verbose {
			log.Printf("✅ Изменен (fallback): %s -> %s", filePath, outputPath)
		}
		return true, err
	}

	// Копируем файл если не было изменений
	if outputPath != filePath {
		return false, p.copyFile(filePath, outputPath)
	}

	return false, nil
}

// ==================== ОБРАБОТКА CSS ====================

// processCSSFile обрабатывает CSS файлы
func (p *PostProcessor) processCSSFile(inputPath, outputPath string) (bool, error) {
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return false, err
	}

	originalContent := string(content)
	processedContent, err := p.cssProcessor.RewriteCSS(content, inputPath)
	if err != nil {
		return false, err
	}

	if string(processedContent) != originalContent {
		err = os.WriteFile(outputPath, processedContent, 0644)
		if err != nil {
			return false, err
		}
		if p.config.Verbose {
			log.Printf("✅ Изменен CSS: %s -> %s", inputPath, outputPath)
		}
		return true, nil
	}

	// Копируем файл если не было изменений
	if inputPath != outputPath {
		return false, p.copyFile(inputPath, outputPath)
	}

	return false, nil
}

// RewriteCSS переписывает CSS с заменой URL
func (c *CSSProcessor) RewriteCSS(content []byte, filePath string) ([]byte, error) {
	// Регулярные выражения для поиска URL в CSS
	// ВНИМАНИЕ: Этот метод использует regex только для CSS, что безопаснее чем для HTML
	patterns := []struct {
		pattern *regexp.Regexp
		replace func(string, string) string
	}{
		// url()
		{
			pattern: regexp.MustCompile(`url\s*\(\s*['"]?\s*([^)'"]+?)\s*['"]?\s*\)`),
			replace: func(match, url string) string {
				newURL := c.linkProcessor.ProcessURL(url, filePath)
				return strings.Replace(match, url, newURL, 1)
			},
		},
		// @import
		{
			pattern: regexp.MustCompile(`@import\s*(?:url\()?\s*['"]\s*([^'"]+?)\s*['"]\s*\)?\s*;`),
			replace: func(match, url string) string {
				newURL := c.linkProcessor.ProcessURL(url, filePath)
				return strings.Replace(match, url, newURL, 1)
			},
		},
		// Встроенные ссылки в CSS (редкий случай)
		{
			pattern: regexp.MustCompile(`(?:src|href)\s*:\s*['"]?\s*([^;'"]+?)\s*['"]?\s*;`),
			replace: func(match, url string) string {
				newURL := c.linkProcessor.ProcessURL(url, filePath)
				return strings.Replace(match, url, newURL, 1)
			},
		},
	}

	processed := string(content)

	for _, p := range patterns {
		processed = p.pattern.ReplaceAllStringFunc(processed, func(match string) string {
			// Извлекаем URL
			submatches := p.pattern.FindStringSubmatch(match)
			if len(submatches) < 2 {
				return match
			}

			url := strings.TrimSpace(submatches[1])
			return p.replace(match, url)
		})
	}

	return []byte(processed), nil
}

// ==================== ОБРАБОТКА JAVASCRIPT ====================

// processJSFile обрабатывает JavaScript файлы
func (p *PostProcessor) processJSFile(inputPath, outputPath string) (bool, error) {
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return false, err
	}

	originalContent := string(content)
	processedContent := p.processJavaScript(content, inputPath)

	if processedContent != originalContent {
		err = os.WriteFile(outputPath, []byte(processedContent), 0644)
		if err != nil {
			return false, err
		}
		if p.config.Verbose {
			log.Printf("✅ Изменен JS: %s -> %s", inputPath, outputPath)
		}
		return true, nil
	}

	// Копируем файл если не было изменений
	if inputPath != outputPath {
		return false, p.copyFile(inputPath, outputPath)
	}

	return false, nil
}

// processJavaScript обрабатывает JavaScript файлы
func (p *PostProcessor) processJavaScript(content []byte, filePath string) string {
	processed := string(content)

	// Ищем строковые литералы с URL
	urlPattern := regexp.MustCompile(`['"](https?://[^'"]*?)['"]`)
	processed = urlPattern.ReplaceAllStringFunc(processed, func(match string) string {
		parts := urlPattern.FindStringSubmatch(match)
		if len(parts) < 2 {
			return match
		}

		url := parts[1]
		// Если URL содержит оригинальный хост, обрабатываем его
		if strings.Contains(url, p.config.OriginalHost) {
			newURL := p.linkProcessor.ProcessURL(url, filePath)
			return strings.Replace(match, url, newURL, 1)
		}

		return match
	})

	return processed
}

// ==================== LINK PROCESSOR ====================

// ProcessURL обрабатывает URL и преобразует его в относительный путь
func (l *LinkProcessor) ProcessURL(originalURL, currentFilePath string) string {
	// Если URL пустой или только якорь
	if originalURL == "" || originalURL == "#" {
		return originalURL
	}

	// Проверяем специальные протоколы
	if l.isSpecialProtocol(originalURL) {
		return originalURL
	}

	// Извлекаем путь из URL
	path := l.extractPathFromURL(originalURL)
	if path == "" {
		return originalURL
	}

	// Обрабатываем путь
	return l.findRelativePath(path, currentFilePath)
}

// extractPathFromURL извлекает путь из URL
func (l *LinkProcessor) extractPathFromURL(urlStr string) string {
	// Проверяем, является ли это абсолютным путем к ресурсу
	// Например: /assets/css/stylesheet.css или /favicon.ico
	if strings.HasPrefix(urlStr, "/") {
		// Убираем начальный слеш
		path := strings.TrimPrefix(urlStr, "/")

		// Если это путь к ресурсу в корне, возвращаем как есть
		if l.isRootPath(urlStr) {
			return path
		}

		// Для других путей тоже возвращаем (они будут обработаны)
		return path
	}

	// Если это полный URL с протоколом
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		// Проверяем, наш ли это хост
		parsedURL, err := url.Parse(urlStr)
		if err != nil {
			return urlStr
		}

		if l.containsOriginalHost(parsedURL.Host) {
			// Наш хост - возвращаем только путь
			return parsedURL.Path
		} else {
			// Внешний хост
			atomic.AddInt64(&l.stats.ExternalLinks, 1)
			if l.config.KeepExternal {
				return urlStr
			}
			return "#"
		}
	}

	// Если это protocol-relative URL
	if strings.HasPrefix(urlStr, "//") {
		// Добавляем протокол для парсинга
		parsedURL, err := url.Parse("https:" + urlStr)
		if err != nil {
			return urlStr
		}

		if l.containsOriginalHost(parsedURL.Host) {
			return parsedURL.Path
		} else {
			atomic.AddInt64(&l.stats.ExternalLinks, 1)
			if l.config.KeepExternal {
				return urlStr
			}
			return "#"
		}
	}

	// Возвращаем как есть (относительный путь)
	return urlStr
}

// isSpecialProtocol проверяет специальные протоколы
func (l *LinkProcessor) isSpecialProtocol(url string) bool {
	specialPrefixes := []string{
		"mailto:", "tel:", "javascript:", "data:",
		"file:", "ftp:", "ssh:", "irc:", "magnet:",
		"blob:", "about:", "chrome:", "edge:",
	}

	for _, prefix := range specialPrefixes {
		if strings.HasPrefix(url, prefix) {
			return true
		}
	}

	return false
}

// containsOriginalHost проверяет, содержит ли URL оригинальный хост
func (l *LinkProcessor) containsOriginalHost(host string) bool {
	if l.config.OriginalHost == "" {
		return false
	}

	// Сравниваем хосты (можно учитывать www. префикс)
	cleanHost := strings.TrimPrefix(host, "www.")
	cleanOriginal := strings.TrimPrefix(l.config.OriginalHost, "www.")

	return cleanHost == cleanOriginal || host == l.config.OriginalHost
}

// processInternalURL обрабатывает внутренний URL
func (l *LinkProcessor) processInternalURL(parsedURL *url.URL, currentFilePath string) string {
	// Извлекаем путь
	path := parsedURL.Path
	if path == "" {
		path = "/"
	}

	// Убираем корневой путь сайта если есть
	if l.config.SiteRootPath != "/" && strings.HasPrefix(path, l.config.SiteRootPath) {
		path = strings.TrimPrefix(path, l.config.SiteRootPath)
	}

	// Если путь заканчивается на /, добавляем index.html
	if strings.HasSuffix(path, "/") {
		path += "index.html"
	}

	// Обрабатываем путь
	relativePath := l.findRelativePath(path, currentFilePath)

	// Сохраняем query и fragment если есть
	result := relativePath
	if parsedURL.RawQuery != "" {
		result += "?" + parsedURL.RawQuery
	}
	if parsedURL.Fragment != "" {
		result += "#" + parsedURL.Fragment
	}

	return result
}

// processAsPath обрабатывает строку как путь
func (l *LinkProcessor) processAsPath(path, currentFilePath string) string {
	// Если путь начинается с / - это абсолютный путь
	if strings.HasPrefix(path, "/") {
		// Убираем начальный слеш
		cleanPath := strings.TrimPrefix(path, "/")

		// Убираем корневой путь сайта если есть
		if l.config.SiteRootPath != "/" && strings.HasPrefix(cleanPath, strings.TrimPrefix(l.config.SiteRootPath, "/")) {
			cleanPath = strings.TrimPrefix(cleanPath, strings.TrimPrefix(l.config.SiteRootPath, "/"))
		}

		return l.findRelativePath(cleanPath, currentFilePath)
	}

	// Относительный путь
	return l.findRelativePath(path, currentFilePath)
}

// processRelativePath обрабатывает относительные пути (../ или ./)
func (l *LinkProcessor) processRelativePath(cleanPath, currentFilePath, originalPath string) string {
	// Нормализуем путь
	normalizedPath := l.normalizeRelativePath(cleanPath, currentFilePath)

	// Ищем файл
	foundFilePath := l.findFile(normalizedPath)
	if foundFilePath == "" {
		// Файл не найден
		return l.handleMissingFile(originalPath)
	}

	// Вычисляем относительный путь от текущего файла
	relativePath := l.calculateRelativePath(foundFilePath, currentFilePath)

	// Восстанавливаем query и fragment
	return l.restoreQueryFragment(relativePath, originalPath)
}

// normalizeRelativePath нормализует относительный путь
func (l *LinkProcessor) normalizeRelativePath(path, currentFilePath string) string {
	// Получаем абсолютный путь
	currentDir := filepath.Dir(currentFilePath)
	absPath, err := filepath.Abs(filepath.Join(currentDir, path))
	if err != nil {
		return path
	}

	// Делаем путь относительно корня сайта
	relToRoot, err := filepath.Rel(l.config.InputDir, absPath)
	if err != nil {
		return path
	}

	return filepath.ToSlash(relToRoot)
}

// findFile ищет файл в структуре сайта
func (l *LinkProcessor) findFile(path string) string {
	// Если путь пустой, это корень
	if path == "" || path == "/" {
		return l.findRootFile()
	}

	// Пробуем разные варианты
	variants := []string{
		path,
		path + "/index.html",
		path + "/index.htm",
	}

	// Если путь не имеет расширения, пробуем добавить .html/.htm
	if !strings.Contains(filepath.Base(path), ".") {
		variants = append(variants, path+".html", path+".htm")
	}

	// Если это PHP файл и включена конвертация
	if strings.HasSuffix(path, ".php") && l.config.ConvertPhp {
		htmlPath := strings.TrimSuffix(path, ".php") + ".html"
		variants = append(variants, htmlPath)
	}

	// Ищем в структуре сайта
	l.siteStructure.mu.RLock()
	defer l.siteStructure.mu.RUnlock()

	for _, variant := range variants {
		if filePath, found := l.siteStructure.urlToFilePath[variant]; found {
			return filePath
		}
	}

	// Ищем по имени файла
	baseName := filepath.Base(path)
	if filePath, found := l.siteStructure.allFiles[baseName]; found {
		return filePath
	}

	return ""
}

// findRootFile ищет корневой файл (index.html)
func (l *LinkProcessor) findRootFile() string {
	rootFiles := []string{
		"/index.html",
		"/index.htm",
		"index.html",
		"index.htm",
		"",
	}

	l.siteStructure.mu.RLock()
	defer l.siteStructure.mu.RUnlock()

	for _, rootFile := range rootFiles {
		if filePath, found := l.siteStructure.urlToFilePath[rootFile]; found {
			return filePath
		}
	}

	return ""
}

// findFileInDirectory ищет файл в указанной директории
func (l *LinkProcessor) findFileInDirectory(dir, fileName string) string {
	// Пробуем разные варианты расширений
	variants := []string{
		fileName,
		fileName + ".html",
		fileName + ".htm",
	}

	for _, variant := range variants {
		fullPath := filepath.Join(dir, variant)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
	}

	// Проверяем директорию с index.html
	dirPath := filepath.Join(dir, fileName)
	indexFiles := []string{
		filepath.Join(dirPath, "index.html"),
		filepath.Join(dirPath, "index.htm"),
	}

	for _, indexFile := range indexFiles {
		if _, err := os.Stat(indexFile); err == nil {
			return indexFile
		}
	}

	return ""
}

// calculateRelativePath вычисляет относительный путь между двумя файлами
func (l *LinkProcessor) calculateRelativePath(targetFile, currentFile string) string {
	// Вычисляем путь относительно корня сайта
	targetRelToRoot, err1 := filepath.Rel(l.config.InputDir, targetFile)
	currentRelToRoot, err2 := filepath.Rel(l.config.InputDir, filepath.Dir(currentFile))

	if err1 != nil || err2 != nil {
		// Если не удалось вычислить, используем стандартный метод
		return l.calculateStandardRelativePath(targetFile, currentFile)
	}

	// Нормализуем пути
	targetRelToRoot = filepath.ToSlash(targetRelToRoot)
	currentRelToRoot = filepath.ToSlash(currentRelToRoot)

	// Если ресурс находится в корневых директориях (assets, css, js, images и т.д.)
	// а текущая страница находится глубоко в структуре, нужно подняться на несколько уровней
	rootDirs := []string{"assets", "css", "js", "images", "img", "fonts", "static", "media"}

	for _, rootDir := range rootDirs {
		if strings.HasPrefix(targetRelToRoot, rootDir+"/") {
			// Ресурс находится в корневой директории
			// Вычисляем, сколько уровней нужно подняться
			levelsUp := strings.Count(currentRelToRoot, "/")
			if currentRelToRoot != "." && currentRelToRoot != "" {
				levelsUp++ // Добавляем еще один уровень для самой директории
			}

			// Создаем путь с нужным количеством ../
			var relPath string
			if levelsUp > 0 {
				upPath := strings.Repeat("../", levelsUp)
				relPath = upPath + targetRelToRoot
			} else {
				relPath = targetRelToRoot
			}

			return relPath
		}
	}

	// Стандартное вычисление относительного пути
	return l.calculateStandardRelativePath(targetFile, currentFile)
}

// calculateStandardRelativePath стандартное вычисление относительного пути
func (l *LinkProcessor) calculateStandardRelativePath(targetFile, currentFile string) string {
	relPath, err := filepath.Rel(filepath.Dir(currentFile), targetFile)
	if err != nil {
		// Если не удалось вычислить, используем имя файла
		return "./" + filepath.Base(targetFile)
	}

	// Нормализуем разделители
	relPath = filepath.ToSlash(relPath)

	// Добавляем ./ если путь не начинается с ../ и не является абсолютным
	if !strings.HasPrefix(relPath, "../") && !strings.HasPrefix(relPath, "./") && relPath != "." {
		relPath = "./" + relPath
	}

	return relPath
}

// findTargetFile находит целевой файл по пути
func (l *LinkProcessor) findTargetFile(path, currentFilePath string) string {
	// Если путь начинается с / - убираем слеш
	cleanPath := strings.TrimPrefix(path, "/")

	// Если путь пустой, ищем индексный файл
	if cleanPath == "" {
		cleanPath = "index.html"
	}

	// Всегда сначала ищем от корня сайта для абсолютных путей
	fullPath := filepath.Join(l.config.InputDir, cleanPath)
	if _, err := os.Stat(fullPath); err == nil {
		return fullPath
	}

	// Если не нашли, проверяем, может быть это директория с index.html
	if info, err := os.Stat(fullPath); err == nil && info.IsDir() {
		indexFiles := []string{
			filepath.Join(fullPath, "index.html"),
			filepath.Join(fullPath, "index.htm"),
		}
		for _, indexFile := range indexFiles {
			if _, err := os.Stat(indexFile); err == nil {
				return indexFile
			}
		}
	}

	// Варианты для поиска файла (с разными расширениями)
	variants := l.getFileVariants(cleanPath)

	// Пробуем каждый вариант от корня сайта
	for _, variant := range variants {
		fullPath := filepath.Join(l.config.InputDir, variant)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
	}

	// Затем ищем относительно текущей директории
	currentDir := filepath.Dir(currentFilePath)
	for _, variant := range variants {
		fullPath := filepath.Join(currentDir, variant)
		if _, err := os.Stat(fullPath); err == nil {
			return fullPath
		}
	}

	// Ищем в структуре сайта
	l.siteStructure.mu.RLock()
	defer l.siteStructure.mu.RUnlock()

	for _, variant := range variants {
		if filePath, found := l.siteStructure.urlToFilePath[variant]; found {
			return filePath
		}
	}

	// Ищем по имени файла
	baseName := filepath.Base(cleanPath)
	if filePath, found := l.siteStructure.allFiles[baseName]; found {
		return filePath
	}

	// Последняя попытка: рекурсивный поиск по всему сайту
	return l.recursiveFindFile(cleanPath)
}

// getFileVariants возвращает все возможные варианты имени файла
func (l *LinkProcessor) getFileVariants(path string) []string {
	variants := []string{path}

	// Если путь не имеет расширения или заканчивается на /, пробуем добавить index.html
	if !strings.Contains(filepath.Base(path), ".") || strings.HasSuffix(path, "/") {
		if strings.HasSuffix(path, "/") {
			variants = append(variants, path+"index.html", path+"index.htm")
		} else {
			variants = append(variants, path+"/index.html", path+"/index.htm")
		}
	}

	// Если это PHP файл и включена конвертация
	if strings.HasSuffix(path, ".php") && l.config.ConvertPhp {
		htmlPath := strings.TrimSuffix(path, ".php") + ".html"
		variants = append([]string{htmlPath}, variants...)
	}

	// Для путей без расширения пробуем добавить распространенные расширения
	ext := filepath.Ext(path)
	if ext == "" {
		commonExtensions := []string{
			".html", ".htm", ".css", ".js",
			".png", ".jpg", ".jpeg", ".gif", ".svg", ".webp", ".ico",
			".woff", ".woff2", ".ttf", ".eot", ".otf",
			".mp4", ".webm", ".mp3", ".wav", ".ogg",
		}
		for _, commonExt := range commonExtensions {
			variants = append(variants, path+commonExt)
		}
	}

	return variants
}

// recursiveFindFile рекурсивно ищет файл по всему сайту
func (l *LinkProcessor) recursiveFindFile(filename string) string {
	baseName := filepath.Base(filename)

	// Ищем файл с таким именем в любом месте сайта
	var foundPath string
	filepath.Walk(l.config.InputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		if filepath.Base(path) == baseName {
			foundPath = path
			return filepath.SkipAll // Останавливаем поиск
		}

		return nil
	})

	return foundPath
}

// restoreQueryFragment восстанавливает query параметры и fragment
func (l *LinkProcessor) restoreQueryFragment(basePath, originalPath string) string {
	result := basePath

	// Добавляем query параметры если есть
	if idx := strings.Index(originalPath, "?"); idx != -1 {
		if strings.Contains(basePath, "?") {
			// Если basePath уже содержит query, заменяем его
			if baseIdx := strings.Index(basePath, "?"); baseIdx != -1 {
				result = basePath[:baseIdx] + originalPath[idx:]
			}
		} else {
			result += originalPath[idx:]
		}
	} else if idx := strings.Index(originalPath, "#"); idx != -1 {
		// Добавляем fragment если нет query
		if !strings.Contains(basePath, "?") {
			result += originalPath[idx:]
		}
	}

	return result
}

// handleMissingFile обрабатывает случай когда файл не найден
func (l *LinkProcessor) handleMissingFile(originalPath string) string {
	if l.config.RemoveMissing {
		// Возвращаем только fragment если есть
		if idx := strings.Index(originalPath, "#"); idx != -1 {
			return originalPath[idx:]
		}
		return "#"
	}

	// Возвращаем оригинальный путь
	return originalPath
}

// findRelativePath ищет файл и возвращает относительный путь
func (l *LinkProcessor) findRelativePath(path, currentFilePath string) string {
	// Если путь пустой, возвращаем как есть
	if path == "" {
		return path
	}

	// Убираем query и fragment из пути для поиска файла
	cleanPath := path
	queryFragment := ""
	if idx := strings.Index(path, "?"); idx != -1 {
		cleanPath = path[:idx]
		queryFragment = path[idx:]
	} else if idx := strings.Index(path, "#"); idx != -1 {
		cleanPath = path[:idx]
		queryFragment = path[idx:]
	}

	// ==================== ОСНОВНОЕ ИСПРАВЛЕНИЕ ====================
	// Всегда вычисляем относительный путь от текущего файла

	// 1. Найти целевой файл
	targetFile := l.findTargetFile(cleanPath, currentFilePath)
	if targetFile == "" {
		// Файл не найден
		if l.config.RemoveMissing {
			return "#" + queryFragment
		}
		return path
	}

	// 2. ВСЕГДА вычисляем относительный путь от текущего файла
	relativePath := l.calculateRelativePath(targetFile, currentFilePath)

	// 3. Добавляем query и fragment обратно
	return relativePath + queryFragment
}

// isRootPath проверяет, является ли путь абсолютным от корня сайта
func (l *LinkProcessor) isRootPath(path string) bool {
	// Пути, которые всегда считаются корневыми
	rootPaths := []string{
		"/assets/", "/css/", "/js/", "/images/", "/img/", "/fonts/",
		"/static/", "/media/", "/favicon.", "/robots.txt", "/sitemap.xml",
	}

	for _, rootPath := range rootPaths {
		if strings.HasPrefix(path, rootPath) {
			return true
		}
	}

	return false
}

// processAbsolutePath обрабатывает абсолютные пути (начинающиеся с /)
func (l *LinkProcessor) processAbsolutePath(cleanPath, currentFilePath, originalPath string) string {
	// Убираем начальный слеш
	pathWithoutSlash := strings.TrimPrefix(cleanPath, "/")

	// Убираем корневой путь сайта если есть
	if l.config.SiteRootPath != "/" {
		siteRootWithoutSlash := strings.TrimPrefix(strings.TrimSuffix(l.config.SiteRootPath, "/"), "/")
		if strings.HasPrefix(pathWithoutSlash, siteRootWithoutSlash+"/") {
			pathWithoutSlash = strings.TrimPrefix(pathWithoutSlash, siteRootWithoutSlash+"/")
		} else if pathWithoutSlash == siteRootWithoutSlash {
			pathWithoutSlash = ""
		}
	}

	// Теперь ищем файл
	foundFilePath := l.findFile(pathWithoutSlash)
	if foundFilePath == "" {
		// Файл не найден
		return l.handleMissingFile(originalPath)
	}

	// Вычисляем относительный путь
	relativePath := l.calculateRelativePath(foundFilePath, currentFilePath)

	// Восстанавливаем query и fragment
	return l.restoreQueryFragment(relativePath, originalPath)
}

// processDirectoryRelative обрабатывает относительные пути без префиксов
func (l *LinkProcessor) processDirectoryRelative(cleanPath, currentFilePath, originalPath string) string {
	// Это путь вида "subdir/file.html"
	// Рассчитываем полный путь относительно текущей директории
	currentDir := filepath.Dir(currentFilePath)
	fullPath := filepath.Join(currentDir, cleanPath)

	// Сначала проверяем существование файла по этому пути
	if _, err := os.Stat(fullPath); err == nil {
		// Файл найден, вычисляем относительный путь
		relativePath := l.calculateRelativePath(fullPath, currentFilePath)
		return l.restoreQueryFragment(relativePath, originalPath)
	}

	// Если файл не найден, ищем в структуре сайта
	foundFilePath := l.findFile(cleanPath)
	if foundFilePath == "" {
		// Файл не найден
		return l.handleMissingFile(originalPath)
	}

	// Вычисляем относительный путь
	relativePath := l.calculateRelativePath(foundFilePath, currentFilePath)

	// Восстанавливаем query и fragment
	return l.restoreQueryFragment(relativePath, originalPath)
}

// processSimpleName обрабатывает простое имя файла
func (l *LinkProcessor) processSimpleName(cleanPath, currentFilePath, originalPath string) string {
	// Сначала пробуем найти файл в той же директории
	possiblePaths := []string{
		cleanPath,
		cleanPath + ".html",
		cleanPath + ".htm",
		cleanPath + "/index.html",
		cleanPath + "/index.htm",
	}

	// Если это PHP файл и включена конвертация
	if l.config.ConvertPhp && strings.HasSuffix(cleanPath, ".php") {
		htmlName := strings.TrimSuffix(cleanPath, ".php") + ".html"
		possiblePaths = append([]string{htmlName}, possiblePaths...)
	}

	// Ищем файл
	var foundFilePath string
	for _, path := range possiblePaths {
		if filePath := l.findFileInDirectory(filepath.Dir(currentFilePath), path); filePath != "" {
			foundFilePath = filePath
			break
		}
	}

	if foundFilePath == "" {
		// Файл не найден
		return l.handleMissingFile(originalPath)
	}

	// Вычисляем относительный путь
	relativePath := l.calculateRelativePath(foundFilePath, currentFilePath)

	// Восстанавливаем query и fragment
	return l.restoreQueryFragment(relativePath, originalPath)
}

// ==================== ВСПОМОГАТЕЛЬНЫЕ МЕТОДЫ ====================

// convertPHPToHTML конвертирует PHP файл в HTML
func (p *PostProcessor) convertPHPToHTML(inputPath, outputPath string) (bool, error) {
	content, err := os.ReadFile(inputPath)
	if err != nil {
		return false, err
	}

	// Проверяем, содержит ли файл HTML
	if !p.containsHTMLContent(string(content)) {
		// Просто копируем как есть
		return false, p.copyFile(inputPath, outputPath)
	}

	// Обрабатываем как HTML
	return p.processHTMLFile(inputPath, outputPath)
}

// containsHTMLContent проверяет наличие HTML контента
func (p *PostProcessor) containsHTMLContent(content string) bool {
	htmlTags := []string{"<!DOCTYPE", "<html", "<head", "<body", "<div", "<p", "<h1", "<h2", "<h3", "<script", "<style"}

	contentLower := strings.ToLower(content)
	for _, tag := range htmlTags {
		if strings.Contains(contentLower, strings.ToLower(tag)) {
			return true
		}
	}

	return false
}

// copyFile копирует файл
func (p *PostProcessor) copyFile(source, destination string) error {
	// Создаем директорию если нужно
	if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
		return err
	}

	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destinationFile, err := os.Create(destination)
	if err != nil {
		return err
	}
	defer destinationFile.Close()

	_, err = io.Copy(destinationFile, sourceFile)
	return err
}

// updatePHPLinks обновляет ссылки на .php файлы
func (p *PostProcessor) updatePHPLinks(content string) string {
	if !p.config.ConvertPhp {
		return content
	}

	// Заменяем .php на .html во всех атрибутах
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(href|src|action)\s*=\s*['"]([^'"]*?)\.php(\?[^'"]*?)?['"]`),
		regexp.MustCompile(`url\s*\(\s*['"]?([^)'"]*?)\.php(\?[^'"]*?)?['"]?\s*\)`),
	}

	result := content
	for _, pattern := range patterns {
		result = pattern.ReplaceAllStringFunc(result, func(match string) string {
			submatches := pattern.FindStringSubmatch(match)
			if len(submatches) < 2 {
				return match
			}

			// Заменяем .php на .html
			return strings.Replace(match, ".php", ".html", 1)
		})
	}

	return result
}

// ==================== СТАТИСТИКА ====================

// printStats выводит статистику обработки
func (p *PostProcessor) printStats() {
	fmt.Printf("\n%s\n", strings.Repeat("═", 70))
	fmt.Printf("📊 СТАТИСТИКА ОБРАБОТКИ\n")
	fmt.Printf("├─ Всего файлов: %d\n", p.stats.TotalFiles)
	fmt.Printf("├─ Обработано: %d\n", p.stats.Processed)
	fmt.Printf("├─ Изменено: %d\n", p.stats.Modified)
	fmt.Printf("├─ Ошибок: %d\n", p.stats.Failed)
	fmt.Printf("├─ Ссылок переписано: %d\n", p.stats.LinksRewritten)
	fmt.Printf("├─ Внешних ссылок: %d\n", p.stats.ExternalLinks)
	fmt.Printf("├─ Локальных копий создано: %d\n", p.stats.LocalCopiesMade)
	fmt.Printf("└─ Время выполнения: %v\n", p.stats.Duration.Round(time.Millisecond))
	fmt.Printf("%s\n", strings.Repeat("═", 70))
}

// ==================== ТОЧКА ВХОДА ====================

// RunPostProcessing запускает постобработку скачанного сайта
func RunPostProcessing(inputDir, originalHost, siteRootPath string) error {
	config := PostProcessorConfig{
		InputDir:      inputDir,
		OriginalHost:  originalHost,
		SiteRootPath:  siteRootPath,
		OutputDir:     inputDir,
		Workers:       runtime.NumCPU() * 2,
		RemoveMissing: false, // Не удалять не найденные ресурсы
		ConvertPhp:    true,  // Конвертировать .php в .html
		KeepExternal:  false, // Не оставлять внешние ссылки
		Verbose:       true,
		Debug:         false,
	}

	processor := NewPostProcessor(config)
	return processor.Run()
}
