package main

import (
	"fmt"
	"image/color"
	"io"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sitemvp/downloader"
	proccesor "sitemvp/processor"
	"strings"
	"time"

	"net/http"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Palette: Graphite & Neon
var (
	// Light Theme (Clean Silver)
	lightPrimary = color.NRGBA{R: 59, G: 130, B: 246, A: 255}  // Blue 500
	lightBg      = color.NRGBA{R: 243, G: 244, B: 246, A: 255} // Gray 100
	lightCardBg  = color.NRGBA{R: 255, G: 255, B: 255, A: 240} // White (Semi-transparent)
	lightText    = color.NRGBA{R: 17, G: 24, B: 39, A: 255}    // Gray 900

	// Dark Theme (Deep Graphite)
	darkPrimary = color.NRGBA{R: 14, G: 165, B: 233, A: 255}  // Sky 500 (Neon Cyan)
	darkBg      = color.NRGBA{R: 23, G: 23, B: 23, A: 255}    // Neutral 900 (Deep Graphite)
	darkCardBg  = color.NRGBA{R: 38, G: 38, B: 38, A: 200}    // Neutral 800 (Semi-transparent)
	darkText    = color.NRGBA{R: 245, G: 245, B: 245, A: 255} // Neutral 100
	darkOverlay = color.NRGBA{R: 0, G: 0, B: 0, A: 100}       // Shadow overlay

	successColor = color.NRGBA{R: 34, G: 197, B: 94, A: 255}
)

type customTheme struct {
	isDark bool
}

func newCustomTheme(isDark bool) fyne.Theme {
	return &customTheme{isDark: isDark}
}

func (t *customTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if t.isDark {
		switch name {
		case theme.ColorNamePrimary:
			return darkPrimary
		case theme.ColorNameBackground:
			return darkBg
		case theme.ColorNameForeground:
			return darkText
		case theme.ColorNameInputBackground:
			return color.NRGBA{R: 255, G: 255, B: 255, A: 15} // Glass effect input
		case theme.ColorNameButton:
			return color.NRGBA{R: 50, G: 50, B: 50, A: 255} // Dark button
		default:
			return theme.DarkTheme().Color(name, variant)
		}
	}
	switch name {
	case theme.ColorNamePrimary:
		return lightPrimary
	case theme.ColorNameBackground:
		return lightBg
	case theme.ColorNameForeground:
		return lightText
	case theme.ColorNameInputBackground:
		return lightCardBg
	default:
		return theme.LightTheme().Color(name, variant)
	}
}

func (t *customTheme) Font(style fyne.TextStyle) fyne.Resource {
	if t.isDark {
		return theme.DarkTheme().Font(style)
	}
	return theme.LightTheme().Font(style)
}

func (t *customTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	if t.isDark {
		return theme.DarkTheme().Icon(name)
	}
	return theme.LightTheme().Icon(name)
}

func (t *customTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 8 // Compact padding
	case theme.SizeNameInlineIcon:
		return 24
	default:
		if t.isDark {
			return theme.DarkTheme().Size(name)
		}
		return theme.LightTheme().Size(name)
	}
}

func createCard(content fyne.CanvasObject, title string, isDark bool) *fyne.Container {
	titleColor := lightText
	bgColor := lightCardBg
	separatorColor := lightPrimary
	if isDark {
		titleColor = darkText
		bgColor = darkCardBg
		separatorColor = darkPrimary
	}

	titleLabel := canvas.NewText(title, titleColor)
	titleLabel.TextSize = 20
	titleLabel.TextStyle = fyne.TextStyle{Bold: true}

	separator := canvas.NewRectangle(separatorColor)
	separator.SetMinSize(fyne.NewSize(0, 2))

	// Main layout: Header at top, Content in center (filling space)
	// We create a custom header container
	header := container.NewVBox(
		container.NewPadded(titleLabel),
		separator,
		widget.NewLabel(" "), // Spacer
	)

	// Use Border layout: Header at Top, Content in Center uses all space
	cardLayout := container.NewBorder(header, nil, nil, nil, container.NewPadded(content))

	// Эффект тени и прозрачности
	shadow := canvas.NewRectangle(color.NRGBA{R: 0, G: 0, B: 0, A: 60})
	shadow.CornerRadius = 16
	shadow.Move(fyne.NewPos(4, 4))

	bg := canvas.NewRectangle(bgColor)
	bg.CornerRadius = 16

	return container.NewStack(shadow, bg, cardLayout)
}

type AnimatedProgress struct {
	widget.BaseWidget
	Progress   *widget.ProgressBar
	Label      *widget.Label
	DetailText *widget.Label
	Percentage *widget.Label

	progressBind binding.Float
	detailBind   binding.String
	percentBind  binding.String
}

func NewAnimatedProgress(title string) *AnimatedProgress {
	p := &AnimatedProgress{
		Label:        widget.NewLabel(title),
		progressBind: binding.NewFloat(),
		detailBind:   binding.NewString(),
		percentBind:  binding.NewString(),
	}

	p.Progress = widget.NewProgressBarWithData(p.progressBind)
	p.DetailText = widget.NewLabelWithData(p.detailBind)
	p.Percentage = widget.NewLabelWithData(p.percentBind)

	p.DetailText.SetText("Waiting...") // Initial value
	p.detailBind.Set("Waiting...")

	p.Percentage.SetText("0%") // Initial value
	p.percentBind.Set("0%")

	p.Label.TextStyle = fyne.TextStyle{Bold: true}
	p.ExtendBaseWidget(p)
	return p
}

func (p *AnimatedProgress) CreateRenderer() fyne.WidgetRenderer {
	headerBox := container.NewBorder(nil, nil, p.Label, p.Percentage)
	c := container.NewVBox(headerBox, p.Progress, p.DetailText)
	return widget.NewSimpleRenderer(c)
}

func (p *AnimatedProgress) SetProgress(value float64, detail string) {
	p.progressBind.Set(value)
	p.detailBind.Set(detail)
	p.percentBind.Set(fmt.Sprintf("%.0f%%", value*100))
} // здесь проблема с потоком и все ломается даже в системе слетает все из

func main() {
	log.Println("🚀 Starting Site Cloner MVP...")

	myApp := app.NewWithID("com.arthur.sitemvp")
	isDarkMode := true
	myApp.Settings().SetTheme(newCustomTheme(isDarkMode))

	window := myApp.NewWindow("Site Cloner — Professional Web Downloader")
	window.Resize(fyne.NewSize(800, 600)) // Более компактный размер по умолчанию
	window.CenterOnScreen()

	outputDir := "./downloads"
	var currentHost string

	downloadLogBinding := binding.NewString()
	downloadLogBinding.Set("Ready to download...\n")

	procLogBinding := binding.NewString()
	procLogBinding.Set("Ready to process...\n")

	// GLOBAL BINDINGS (Declared early for visibility)
	showSuccessDialog := binding.NewString()
	isDownloadingBinding := binding.NewBool()
	isProcessingBinding := binding.NewBool()

	// Theme toggle

	// Theme toggle
	themeToggle := widget.NewButton("☀️ Light", func() {})
	var updateTheme func()
	updateTheme = func() {
		if isDarkMode {
			themeToggle.SetText("☀️ Light")
			themeToggle.OnTapped = func() {
				isDarkMode = false
				myApp.Settings().SetTheme(newCustomTheme(isDarkMode))
				updateTheme()
			}
		} else {
			themeToggle.SetText("🌙 Dark")
			themeToggle.OnTapped = func() {
				isDarkMode = true
				myApp.Settings().SetTheme(newCustomTheme(isDarkMode))
				updateTheme()
			}
		}
	}
	updateTheme()

	header := container.NewBorder(nil, nil,
		widget.NewLabelWithStyle("Site Cloner", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		themeToggle,
	)

	// DOWNLOADER

	urlEntry := widget.NewEntry()
	urlEntry.SetPlaceHolder("https://example.com")
	urlEntry.OnChanged = func(s string) {
		if parsed, err := url.Parse(s); err == nil {
			currentHost = parsed.Host
		}
	}

	dirEntry := widget.NewEntry()
	dirEntry.SetText(outputDir)
	dirEntry.OnChanged = func(s string) { outputDir = s }

	btnBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				outputDir = uri.Path()
				dirEntry.SetText(outputDir)
			}
		}, window)
	})

	downloadLogEntry := widget.NewMultiLineEntry()
	downloadLogEntry.Wrapping = fyne.TextWrapWord
	downloadLogEntry.Bind(downloadLogBinding)
	downloadLogEntry.Disable()
	downloadScroll := container.NewScroll(downloadLogEntry)
	downloadScroll.SetMinSize(fyne.NewSize(0, 250))

	progressCard := NewAnimatedProgress("Download Progress")

	var isDownloading bool
	var downloadBtn *widget.Button
	downloadBtn = widget.NewButtonWithIcon("🚀 Start Download", theme.DownloadIcon(), func() {
		if isDownloading {
			dialog.ShowInformation("Busy", "Download in progress", window)
			return
		}

		if urlEntry.Text == "" {
			dialog.ShowError(fmt.Errorf("URL cannot be empty"), window)
			return
		}

		// Используем binding для управления состоянием
		isDownloadingBinding.Set(true)

		cfg := downloader.Config{
			OutputDir:   outputDir,
			Workers:     20,
			Retries:     5,
			MaxDepth:    15,
			Delay:       200 * time.Millisecond,
			MaxFileSize: downloader.DefaultMaxFileSize,
			UserAgent:   downloader.DefaultUserAgent,
		}

		job, err := downloader.NewJob(urlEntry.Text, cfg)
		if err != nil {
			downloadLogBinding.Set(fmt.Sprintf("❌ Error: %v\n", err))
			downloadLogBinding.Set(fmt.Sprintf("❌ Error: %v\n", err))
			isDownloadingBinding.Set(false)
			return
		}

		downloadLogBinding.Set(fmt.Sprintf("📡 Starting: %s\n\n", urlEntry.Text))
		progressCard.SetProgress(0, "Init...")

		go func() {
			logCh := make(chan string, 100)

			go func() {
				for msg := range job.Events {
					logCh <- msg
				}
				close(logCh)
			}()

			go func() {
				for msg := range logCh {
					currentLog, _ := downloadLogBinding.Get()
					downloadLogBinding.Set(currentLog + msg + "\n")

					if strings.Contains(msg, "Файлов:") {
						progressCard.SetProgress(0.6, msg)
					} else if strings.Contains(msg, "completed") {
						progressCard.SetProgress(1.0, "Done!")
					}
				}
			}()

			job.Run()

			time.Sleep(200 * time.Millisecond) // Подождем чтобы все логи обновились

			currentLog, _ := downloadLogBinding.Get()
			downloadLogBinding.Set(currentLog + "\n✅ Finished!\n")
			progressCard.SetProgress(1.0, "Complete!")

			isDownloadingBinding.Set(false)
			showSuccessDialog.Set("Download complete!")
		}()
	})
	downloadBtn.Importance = widget.HighImportance

	// Layout: Inputs at Top, Log in Center
	// Layout: Inputs at Top, Log in Center
	downloadInputs := container.NewVBox(
		widget.NewLabelWithStyle("🌐 URL", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		urlEntry,
		widget.NewLabelWithStyle("📁 Output Output", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, btnBrowse, dirEntry),
		layout.NewSpacer(),
		downloadBtn,
		progressCard,
	)

	downloadForm := container.NewBorder(downloadInputs, nil, nil, nil, downloadScroll)

	downloadCard := createCard(downloadForm, "⬇️  Downloader", isDarkMode)

	// PROCESSOR

	procDirEntry := widget.NewEntry()
	procDirEntry.SetText(outputDir)

	btnProcBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				procDirEntry.SetText(uri.Path())
			}
		}, window)
	})

	hostEntry := widget.NewEntry()
	hostEntry.SetPlaceHolder("example.com")

	procLogEntry := widget.NewMultiLineEntry()
	procLogEntry.Wrapping = fyne.TextWrapWord
	procLogEntry.Bind(procLogBinding)
	procLogEntry.Disable()
	procScroll := container.NewScroll(procLogEntry)
	procScroll.SetMinSize(fyne.NewSize(0, 250))

	procProgress := NewAnimatedProgress("Process Progress")

	var isProcessing bool
	var processBtn *widget.Button
	processBtn = widget.NewButtonWithIcon("⚙️  Process", theme.DocumentIcon(), func() {
		if isProcessing {
			dialog.ShowInformation("Busy", "Processing in progress", window)
			return
		}

		sourceDir := procDirEntry.Text
		host := hostEntry.Text

		if host == "" && currentHost != "" {
			host = currentHost
			hostEntry.SetText(host)
		}

		if sourceDir == "" {
			dialog.ShowError(fmt.Errorf("Source directory empty"), window)
			return
		}

		isProcessingBinding.Set(true)

		procLogBinding.Set("🔄 Initializing...\n")
		procProgress.SetProgress(0, "Starting...")

		go func() {
			p := proccesor.NewProcessor(host)

			p.OnLog = func(msg string) {
				currentLog, _ := procLogBinding.Get()
				procLogBinding.Set(currentLog + msg)

				if strings.Contains(msg, "[START]") {
					procProgress.SetProgress(0.1, "Processing...")
				} else if strings.Contains(msg, "[DONE]") {
					procProgress.SetProgress(1.0, "Done!")
				}
			}

			scripts := p.AnalyzeScripts(sourceDir)
			currentLog, _ := procLogBinding.Get()
			procLogBinding.Set(currentLog + fmt.Sprintf("\n📊 Scripts: %d\n\n", len(scripts)))
			procProgress.SetProgress(0.3, fmt.Sprintf("%d scripts", len(scripts)))

			// 1. Prepare output path
			processedDirName := filepath.Base(sourceDir) + "_processed"
			outputPath := filepath.Join(filepath.Dir(sourceDir), processedDirName)

			// 2. Copy directory
			procLogBinding.Set(currentLog + fmt.Sprintf("\n📂 Copying to %s...\n", processedDirName))
			if err := copyDir(sourceDir, outputPath); err != nil {
				dialog.ShowError(fmt.Errorf("Failed to copy directory: %v", err), window)
				isProcessingBinding.Set(false)
				return
			}

			// 3. Process the COPIED directory
			p.Process(outputPath)

			time.Sleep(200 * time.Millisecond)

			currentLog, _ = procLogBinding.Get()
			procLogBinding.Set(currentLog + "\n✅ Complete!\n")

			isProcessingBinding.Set(false)

			msg := fmt.Sprintf("Done!\n\nProcessed Folder: %s\n\nFiles: %d\nLinks: %d",
				outputPath, p.Stats.FilesProcessed, p.Stats.LinksRewritten)

			showSuccessDialog.Set(msg)
		}()
	})
	processBtn.Importance = widget.HighImportance

	// Layout: Inputs at Top, Log in Center
	// Layout: Inputs at Top, Log in Center
	procInputs := container.NewVBox(
		widget.NewLabelWithStyle("📂 Source", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, btnProcBrowse, procDirEntry),
		widget.NewLabelWithStyle("🌐 Original Host", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		hostEntry,
		layout.NewSpacer(),
		processBtn,
		procProgress,
	)

	processForm := container.NewBorder(procInputs, nil, nil, nil, procScroll)

	processorCard := createCard(processForm, "⚙️  Processor", isDarkMode)

	// === SERVER TAB ===

	serverDirEntry := widget.NewEntry()
	serverDirEntry.SetText(outputDir)
	serverPortEntry := widget.NewEntry()
	serverPortEntry.SetText("8080")

	serverStatus := binding.NewString()
	serverStatus.Set("Stopped")
	serverStatusLabel := widget.NewLabelWithData(serverStatus)

	var server *http.Server
	var isServerRunning bool
	var serverBtn *widget.Button

	// Контроллер для запуска сервера (доступен из Library)
	var ctrlStartServer func(dir string)
	var ctrlStopServer func()

	serverLogBinding := binding.NewString()
	serverLogBinding.Set("Server Ready...\n")
	serverLogEntry := widget.NewMultiLineEntry()
	serverLogEntry.Bind(serverLogBinding)
	serverLogEntry.Disable()
	serverScroll := container.NewScroll(serverLogEntry)
	serverScroll.SetMinSize(fyne.NewSize(0, 150))

	openBrowserBtn := widget.NewButtonWithIcon("Open in Browser", theme.GridIcon(), func() {
		u, _ := url.Parse("http://localhost:" + serverPortEntry.Text)
		fyne.CurrentApp().OpenURL(u)
	})
	openBrowserBtn.Disable()

	// Logic Implementation
	ctrlStopServer = func() {
		if server != nil {
			server.Close()
			server = nil
		}
		isServerRunning = false
		serverBtn.SetText("Start Server")
		serverBtn.SetIcon(theme.MediaPlayIcon())
		serverBtn.Importance = widget.HighImportance
		serverStatus.Set("Stopped")
		openBrowserBtn.Disable()
		serverLogBinding.Set("Server stopped.\n")
	}

	ctrlStartServer = func(dir string) {
		if isServerRunning {
			ctrlStopServer()
		}

		port := serverPortEntry.Text
		if dir == "" {
			dir = serverDirEntry.Text
		} else {
			serverDirEntry.SetText(dir)
		}

		if dir == "" {
			dialog.ShowError(fmt.Errorf("Directory cannot be empty"), window)
			return
		}

		server = &http.Server{Addr: ":" + port, Handler: http.FileServer(http.Dir(dir))}
		isServerRunning = true
		serverBtn.SetText("Stop Server")
		serverBtn.SetIcon(theme.MediaStopIcon())
		serverBtn.Importance = widget.DangerImportance
		serverStatus.Set("Running on http://localhost:" + port)
		openBrowserBtn.Enable()

		currentLog, _ := serverLogBinding.Get()
		serverLogBinding.Set(currentLog + fmt.Sprintf("🚀 Server started on port %s\nServing: %s\n", port, dir))

		go func() {
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				isServerRunning = false
				serverStatus.Set("Error: " + err.Error())
				serverBtn.SetText("Start Server")
				serverBtn.Importance = widget.HighImportance
				openBrowserBtn.Disable()
			}
		}()

		// Auto-open browser
		u, _ := url.Parse("http://localhost:" + port)
		fyne.CurrentApp().OpenURL(u)
	}

	serverBtn = widget.NewButtonWithIcon("Start Server", theme.MediaPlayIcon(), func() {
		if isServerRunning {
			ctrlStopServer()
		} else {
			ctrlStartServer("") // Use default dir from entry
		}
	})
	serverBtn.Importance = widget.HighImportance

	btnServerBrowse := widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if uri != nil {
				serverDirEntry.SetText(uri.Path())
			}
		}, window)
	})

	// Layout: Inputs at Top, Log in Center
	// Layout: Inputs at Top, Log in Center
	serverInputs := container.NewVBox(
		widget.NewLabelWithStyle("📂 Site Directory", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		container.NewBorder(nil, nil, nil, btnServerBrowse, serverDirEntry),
		widget.NewLabelWithStyle("🔌 Port", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		serverPortEntry,
		layout.NewSpacer(),
		container.NewGridWithColumns(2, serverBtn, openBrowserBtn),
		layout.NewSpacer(),
		container.NewHBox(widget.NewLabel("Status:"), serverStatusLabel),
	)

	serverForm := container.NewBorder(serverInputs, nil, nil, nil, serverScroll)

	serverCard := createCard(serverForm, "🌐 Local Web Server", isDarkMode)

	// Bindings Listeners
	showSuccessDialog.AddListener(binding.NewDataListener(func() {
		msg, _ := showSuccessDialog.Get()
		if msg != "" {
			dialog.ShowInformation("Success", msg, window)
			showSuccessDialog.Set("") // Reset
		}
	}))

	isDownloadingBinding.AddListener(binding.NewDataListener(func() {
		if val, _ := isDownloadingBinding.Get(); val {
			downloadBtn.Disable()
		} else {
			downloadBtn.Enable()
		}
	}))

	isProcessingBinding.AddListener(binding.NewDataListener(func() {
		if val, _ := isProcessingBinding.Get(); val {
			processBtn.Disable()
		} else {
			processBtn.Enable()
		}
	}))

	var tabs *container.AppTabs // Forward declaration for closures

	// === LIBRARY TAB ===

	libraryContainer := container.NewGridWithColumns(3) // 3 columns for cards
	libraryScroll := container.NewScroll(libraryContainer)

	scanLibrary := func() {
		libraryContainer.Objects = nil // Clear

		files, err := os.ReadDir(outputDir)
		if err != nil {
			libraryContainer.Add(widget.NewLabel("Folder not found: " + outputDir))
			libraryContainer.Refresh()
			return
		}

		found := false
		for _, f := range files {
			if f.IsDir() {
				siteName := f.Name()
				fullPath := filepath.Join(outputDir, siteName)

				// Logic: Show only processed sites in Library or mark them
				isProcessed := strings.HasSuffix(siteName, "_processed")

				// Only show processed folders OR raw folders that don't have a processed version?
				// User request: "в библиотеке не должно быть скачанных сайтов без обработки"
				// So we filter ONLY for _processed folders.
				if !isProcessed {
					continue
				}

				found = true
				cleanName := strings.TrimSuffix(siteName, "_processed")

				// Card Content
				lbl := widget.NewLabelWithStyle(cleanName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
				statusLbl := widget.NewLabelWithStyle("✅ Processed", fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
				statusLbl.Alignment = fyne.TextAlignCenter

				btnLaunch := widget.NewButtonWithIcon("Launch", theme.MediaPlayIcon(), func() {
					if tabs != nil {
						tabs.SelectIndex(2)
					}
					ctrlStartServer(fullPath)
				})
				btnLaunch.Importance = widget.HighImportance

				cardContent := container.NewVBox(
					lbl,
					statusLbl,
					layout.NewSpacer(),
					btnLaunch,
				)

				bg := canvas.NewRectangle(color.NRGBA{R: 255, G: 255, B: 255, A: 20})
				bg.CornerRadius = 8
				card := container.NewStack(bg, container.NewPadded(cardContent))

				libraryContainer.Add(card)
			}
		}
		if !found {
			libraryContainer.Add(widget.NewLabelWithStyle("No processed sites found.", fyne.TextAlignCenter, fyne.TextStyle{Italic: true}))
		}
		libraryContainer.Refresh()
	}

	btnRefreshLib := widget.NewButtonWithIcon("Refresh Library", theme.ViewRefreshIcon(), scanLibrary)

	libraryLayout := container.NewBorder(
		container.NewPadded(btnRefreshLib),
		nil, nil, nil,
		container.NewPadded(libraryScroll),
	)

	libraryCard := createCard(libraryLayout, "📚 Site Library", isDarkMode)

	// Init Library
	scanLibrary()

	// ABOUT

	aboutContent := container.NewVBox(
		layout.NewSpacer(),
		widget.NewLabelWithStyle("Site Cloner MVP", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle("v1.0.0", fyne.TextAlignCenter, fyne.TextStyle{}),
		layout.NewSpacer(),
		widget.NewLabel("✨ Features:\n• Multi-threaded\n• Smart links\n• PHP→HTML\n• Real-time progress\n• Themes"),
		layout.NewSpacer(),
		widget.NewLabel("🛠 Stack:\nGo • Fyne • Concurrency"),
		layout.NewSpacer(),
	)

	aboutCard := createCard(aboutContent, "ℹ️  About", isDarkMode)

	// TABS

	// TABS

	tabs = container.NewAppTabs(
		container.NewTabItemWithIcon("Download", theme.DownloadIcon(), downloadCard),
		container.NewTabItemWithIcon("Process", theme.DocumentIcon(), processorCard),
		container.NewTabItemWithIcon("Server", theme.ComputerIcon(), serverCard),
		container.NewTabItemWithIcon("Library", theme.FolderOpenIcon(), libraryCard),
		container.NewTabItemWithIcon("About", theme.InfoIcon(), aboutCard),
	)

	mainContent := container.NewBorder(
		container.NewPadded(header),
		nil, nil, nil,
		container.NewPadded(tabs),
	)

	window.SetContent(mainContent)
	window.ShowAndRun()

	log.Println("👋 Goodbye!")
}

// Helper function to copy a directory
func copyDir(src string, dst string) error {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !si.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = copyDir(srcPath, dstPath)
			if err != nil {
				return err
			}
		} else {
			// Copy file
			if err = copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
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

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
