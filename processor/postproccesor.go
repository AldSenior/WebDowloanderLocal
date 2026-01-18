package proccesor

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/net/html"
)

type Config struct {
	Dir             string
	OriginalHost    string
	OutputDir       string
	RootDir         string
	Verbose         bool
	Debug           bool
	ScriptsToRemove []string
}

type Stats struct {
	TotalFiles     int64
	FilesProcessed int64
	LinksRewritten int64
	StartTime      time.Time
}

type Processor struct {
	cfg   Config
	Stats *Stats // Сделали публичным
	OnLog func(string)
}

func (p *Processor) log(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	if p.OnLog != nil {
		p.OnLog(msg)
	} else {
		fmt.Print(msg)
	}
}

var (
	cssURLRegex = regexp.MustCompile(`url\(\s*(?:'([^']*)'|"([^"]*)"|([^'"\)\s]+))\s*\)`)
)

const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorCyan   = "\033[36m"
	ColorYellow = "\033[33m"
)

func (p *Processor) AnalyzeScripts(dir string) []string {
	var scripts []string
	seen := make(map[string]bool)
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".html" || ext == ".php" || ext == ".htm" {
				b, _ := ioutil.ReadFile(path)
				doc, _ := html.Parse(bytes.NewReader(b))
				var f func(*html.Node)
				f = func(n *html.Node) {
					if n.Type == html.ElementNode && n.Data == "script" {
						src := ""
						for _, a := range n.Attr {
							if a.Key == "src" {
								src = a.Val
							}
						}
						if src != "" && !seen[src] {
							scripts = append(scripts, src)
							seen[src] = true
						}
					}
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						f(c)
					}
				}
				f(doc)
			}
		}
		return nil
	})
	return scripts
}

// ЭТОТ МЕТОД НУЖЕН GUI
func (p *Processor) Process(sourceDir string, scriptsToRemove []string) {
	if p.Stats == nil {
		p.Stats = &Stats{StartTime: time.Now()}
	}
	// Если OutputDir не задан (вызов из GUI), зададим дефолт
	if p.cfg.OutputDir == "" {
		p.cfg.OutputDir = filepath.Clean(sourceDir) + "_processed"
	}
	p.cfg.Dir = sourceDir

	// Сохраняем паттерны для удаления
	p.cfg.ScriptsToRemove = scriptsToRemove

	// Если хост пустой, попробуем извлечь из имени папки
	if p.cfg.OriginalHost == "" {
		baseName := filepath.Base(sourceDir)
		// Убираем протокол если есть
		cleaned := strings.TrimPrefix(strings.TrimPrefix(baseName, "https://"), "http://")
		p.cfg.OriginalHost = cleaned
		p.log("[WARN] OriginalHost не задан, используем: %s\n", p.cfg.OriginalHost)
	}

	p.log("[START] Обработка: %s -> %s\n", p.cfg.Dir, p.cfg.OutputDir)

	// Pre-scan for progress
	var total int64
	filepath.WalkDir(sourceDir, func(_ string, d os.DirEntry, _ error) error {
		if !d.IsDir() {
			total++
		}
		return nil
	})
	p.Stats.TotalFiles = total

	if len(scriptsToRemove) > 0 {
		p.log("[INFO] Удаление скриптов: %d паттернов\n", len(scriptsToRemove))
	}
	p.walkAndProcess(sourceDir)
	p.log("[DONE] Обработка завершена. Файлов: %d, Ссылок: %d\n", atomic.LoadInt64(&p.Stats.FilesProcessed), atomic.LoadInt64(&p.Stats.LinksRewritten))
}

// Вспомогательный метод для инициализации
func NewProcessor(host string) *Processor {
	return &Processor{
		cfg: Config{
			OriginalHost: host,
			Verbose:      true,
		},
		Stats: &Stats{StartTime: time.Now()},
	}
}
func main() {
	dir := flag.String("dir", "", "Папка с исходным сайтом (например, ./downloads/gopedia.ru)")
	host := flag.String("host", "gopedia.ru", "Домен сайта")
	output := flag.String("output", "./processed", "Куда сохранить результат")
	root := flag.String("root", "/", "Корень сайта")
	verbose := flag.Bool("verbose", true, "Выводить общую информацию")
	debug := flag.Bool("debug", false, "Показывать детали каждой замены")
	flag.Parse()

	if *dir == "" {
		fmt.Println(ColorRed + "Ошибка: укажите папку через -dir" + ColorReset)
		os.Exit(1)
	}

	cleanHost := strings.TrimPrefix(strings.TrimPrefix(*host, "https://"), "http://")

	p := &Processor{
		cfg: Config{
			Dir:          filepath.Clean(*dir),
			OriginalHost: cleanHost,
			OutputDir:    filepath.Clean(*output),
			RootDir:      *root,
			Verbose:      *verbose,
			Debug:        *debug,
		},
		Stats: &Stats{StartTime: time.Now()},
	}

	// Очистка папки вывода перед началом (опционально)
	os.RemoveAll(p.cfg.OutputDir)

	if p.cfg.Verbose {
		fmt.Printf("%s[START]%s Обработка: %s -> %s\n", ColorCyan, ColorReset, p.cfg.Dir, p.cfg.OutputDir)
	}

	p.walkAndProcess(p.cfg.Dir)
	p.printStats()
}

// resolveTargetPath — ядро логики исправления ссылок
func (p *Processor) resolveTargetPath(currentFile, rawURL string) (string, bool) {
	orig := rawURL
	trimmedURL := strings.TrimSpace(rawURL)
	u, err := url.Parse(trimmedURL)
	if err != nil {
		return orig, false
	}

	// 1. Пропускаем внешку и якоря
	isMyHost := u.Host == "" || strings.Contains(u.Host, p.cfg.OriginalHost)
	if !isMyHost || strings.HasPrefix(trimmedURL, "data:") ||
		strings.HasPrefix(trimmedURL, "mailto:") || strings.HasPrefix(trimmedURL, "#") {
		return orig, true
	}

	// 2. ЗАЩИТА КОРНЯ: Ссылка на главную всегда ведет в /index.html
	if u.Path == "" || u.Path == "/" {
		return formatResult(u, "/index.html"), true
	}

	// 3. ПОДГОТОВКА ПУТЕЙ
	targetPath := u.Path
	pureName := strings.TrimPrefix(targetPath, "/")

	// Определяем контекст текущей папки относительно корня проекта
	relBase, _ := filepath.Rel(p.cfg.Dir, filepath.Dir(currentFile))
	relBaseSlash := filepath.ToSlash(relBase)

	// 4. УМНЫЙ ПОИСК (Локальный vs Абсолютный)
	// Проверяем, существует ли цель прямо в текущей папке на диске
	checkPathLocal := filepath.Join(filepath.Dir(currentFile), pureName)
	_, errLocal := os.Stat(checkPathLocal)
	if errLocal != nil {
		_, errLocal = os.Stat(checkPathLocal + ".html")
	}

	resolvedPath := targetPath
	// Если ссылка относительная (нет / в начале) ИЛИ мы нашли файл локально — склеиваем с текущей папкой
	isActuallyRelative := !strings.HasPrefix(targetPath, "/") || errLocal == nil

	if isActuallyRelative && relBaseSlash != "." {
		resolvedPath = path.Join(relBaseSlash, pureName)
	}

	// 5. НОРМАЛИЗАЦИЯ
	cleanPath := path.Clean("/" + resolvedPath)

	// Если уже указывает на индекс — возвращаем как есть
	if strings.HasSuffix(cleanPath, "/index.html") {
		return formatResult(u, cleanPath), true
	}

	// 6. ПРОВЕРКА СТРУКТУРЫ (Файл vs Папка)
	pathWithoutExt := strings.TrimSuffix(cleanPath, ".html")
	dirPathOnDisk := filepath.Join(p.cfg.Dir, pathWithoutExt)
	fullPathOnDisk := filepath.Join(p.cfg.Dir, cleanPath)

	finalPath := cleanPath

	// Если на диске есть папка с таким именем — Hugo превратил страницу в папку с index.html
	if fileInfo, err := os.Stat(dirPathOnDisk); err == nil && fileInfo.IsDir() {
		finalPath = path.Join(pathWithoutExt, "index.html")
	} else {
		ext := path.Ext(cleanPath)
		if ext == "" {
			if _, err := os.Stat(fullPathOnDisk + ".html"); err == nil {
				finalPath = cleanPath + ".html"
			} else {
				// Если ничего не нашли, предполагаем структуру папки (красивая ссылка)
				finalPath = path.Join(cleanPath, "index.html")
			}
		} else if ext == ".php" {
			finalPath = pathWithoutExt + ".html"
		}
	}

	// 7. ЗАЩИТА ОТ ДВОЙНОГО ИНДЕКСА
	if strings.HasSuffix(finalPath, "/index.html/index.html") {
		finalPath = strings.TrimSuffix(finalPath, "/index.html")
	}

	// 8. ПРЕВРАЩАЕМ В ОТНОСИТЕЛЬНЫЙ ПУТЬ
	// Мы знаем relBase (путь текущей папки от корня) и finalPath (цель от корня)
	finalRelPath, err := filepath.Rel(relBaseSlash, strings.TrimPrefix(finalPath, "/"))
	if err != nil {
		finalRelPath = finalPath
	}

	// Всегда используем Forward Slash для HTML
	finalRelPath = filepath.ToSlash(finalRelPath)

	if p.cfg.Debug && orig != finalRelPath {
		p.log("[FIX] %s -> %s\n", orig, finalRelPath)
	}

	return formatResult(u, finalRelPath), true
}

func formatResult(u *url.URL, cleanPath string) string {
	res := cleanPath
	if u.RawQuery != "" {
		res += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		res += "#" + u.Fragment
	}
	return res
}

func (p *Processor) walkAndProcess(sourceDir string) {
	filepath.Walk(sourceDir, func(fpath string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		rel, _ := filepath.Rel(sourceDir, fpath)
		outPath := filepath.Join(p.cfg.OutputDir, rel)

		if strings.HasSuffix(fpath, ".php") {
			outPath = strings.TrimSuffix(outPath, ".php") + ".html"
		}

		os.MkdirAll(filepath.Dir(outPath), 0755)

		ext := strings.ToLower(filepath.Ext(fpath))
		var perr error

		if ext == ".html" || ext == ".php" || ext == ".htm" {
			_, perr = p.processHTML(fpath, outPath)
		} else if ext == ".css" {
			_, perr = p.processCSS(fpath, outPath)
		} else {
			perr = copyFile(fpath, outPath)
		}

		atomic.AddInt64(&p.Stats.FilesProcessed, 1)
		return perr
	})
}

func (p *Processor) processHTML(src, dst string) (bool, error) {
	b, err := ioutil.ReadFile(src)
	if err != nil {
		return false, err
	}
	doc, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return false, err
	}

	var f func(*html.Node)
	f = func(n *html.Node) {
		if n.Type == html.ElementNode {
			// Удаление скриптов если нужно
			if n.Data == "script" && len(p.cfg.ScriptsToRemove) > 0 {
				src := ""
				for _, a := range n.Attr {
					if a.Key == "src" {
						src = a.Val
					}
				}
				for _, pattern := range p.cfg.ScriptsToRemove {
					if strings.Contains(src, pattern) || (src == "" && pattern == "inline") {
						// Удаляем узел (заменяем на комментарий или просто удаляем)
						// В net/html удаление узла сложнее, мы просто изменим его данные на пустые комментарии
						// или пометим для игнорирования. Но проще всего изменить тип на Comment.
						n.Type = html.CommentNode
						n.Data = " [SiteCloner: Script Removed] "
						n.Attr = nil
						return // Не идем вглубь удаленного узла
					}
				}
			}

			for i, a := range n.Attr {
				if isLinkAttr(n.Data, a.Key) || (a.Key == "content" && isMetaURL(n)) {
					newURL, ok := p.resolveTargetPath(src, a.Val)
					if ok && newURL != a.Val {
						n.Attr[i].Val = newURL
						atomic.AddInt64(&p.Stats.LinksRewritten, 1)
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
	return true, ioutil.WriteFile(dst, buf.Bytes(), 0644)
}

func (p *Processor) processCSS(src, dst string) (bool, error) {
	b, err := ioutil.ReadFile(src)
	if err != nil {
		return false, err
	}
	content := string(b)
	newContent := cssURLRegex.ReplaceAllStringFunc(content, func(m string) string {
		match := cssURLRegex.FindStringSubmatch(m)
		if len(match) < 2 {
			return m
		}
		raw := ""
		if match[1] != "" {
			raw = match[1]
		} else if match[2] != "" {
			raw = match[2]
		} else if match[3] != "" {
			raw = match[3]
		}
		if raw == "" {
			return m
		}
		newURL, ok := p.resolveTargetPath(src, raw)
		if ok {
			return strings.Replace(m, raw, newURL, 1)
		}
		return m
	})
	return true, ioutil.WriteFile(dst, []byte(newContent), 0644)
}

func isLinkAttr(tag, attr string) bool {
	return attr == "href" || attr == "src" || attr == "srcset" || attr == "action"
}

func isMetaURL(n *html.Node) bool {
	for _, a := range n.Attr {
		if (a.Key == "property" || a.Key == "name") &&
			(strings.Contains(a.Val, "url") || strings.Contains(a.Val, "image")) {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	if src == dst {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func (p *Processor) printStats() {
	if p.cfg.Verbose {
		fmt.Printf("\n%s"+strings.Repeat("=", 35)+"%s\n", ColorCyan, ColorReset)
		fmt.Printf("Файлов обработано: %d\n", atomic.LoadInt64(&p.Stats.FilesProcessed))
		fmt.Printf("Ссылок исправлено: %s%d%s\n", ColorGreen, atomic.LoadInt64(&p.Stats.LinksRewritten), ColorReset)
		fmt.Printf("Время выполнения:  %v\n", time.Since(p.Stats.StartTime).Round(time.Second))
		fmt.Printf("%s"+strings.Repeat("=", 35)+"%s\n", ColorCyan, ColorReset)
	}
}
