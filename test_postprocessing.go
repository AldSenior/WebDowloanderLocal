package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"strings"
)

func main() {
	inputDir := flag.String("input", "", "Входная директория со скачанным сайтом")
	outputDir := flag.String("output", "", "Выходная директория (по умолчанию = входная)")
	host := flag.String("host", "", "Оригинальный хост сайта (например: example.com)")
	rootPath := flag.String("root", "/", "Корневой путь сайта (например: /blog/)")
	workers := flag.Int("workers", 0, "Количество воркеров (0 = auto)")
	keepExternal := flag.Bool("keep-external", true, "Сохранять внешние ссылки (true) или заменять на # (false)") // ← ДОБАВЬТЕ
	verbose := flag.Bool("verbose", false, "Подробный вывод")

	flag.Parse()

	// Проверяем обязательные параметры
	if *inputDir == "" {
		log.Fatal("❌ Ошибка: не указана входная директория (--input)")
	}
	if *host == "" {
		log.Fatal("❌ Ошибка: не указан хост сайта (--host)")
	}

	// Настраиваем логгирование
	if !*verbose {
		log.SetOutput(ioutil.Discard)
	}

	// Создаем конфигурацию
	config := PostProcessorConfig{
		InputDir:      *inputDir,
		OutputDir:     *outputDir,
		OriginalHost:  *host,
		SiteRootPath:  *rootPath,
		Workers:       *workers,
		KeepExternal:  *keepExternal,  // ← ДОБАВЬТЕ
		RemoveMissing: !*keepExternal, // ← ДОБАВЬТЕ
		// Verbose:       *verbose,       // ← ДОБАВЬТЕ ЕСЛИ НЕТ В СТРУКТУРЕ
	}

	if config.OutputDir == "" {
		config.OutputDir = config.InputDir
	}

	// Запускаем обработку
	processor := NewPostProcessor(config)

	fmt.Printf("🌐 Универсальный постпроцессор сайтов\n")
	fmt.Printf("📁 Вход: %s\n", config.InputDir)
	fmt.Printf("📁 Выход: %s\n", config.OutputDir)
	fmt.Printf("🌐 Хост: %s\n", config.OriginalHost)
	fmt.Printf("📍 Путь: %s\n", config.SiteRootPath)
	fmt.Printf("👷 Воркеров: %d\n", config.Workers)
	fmt.Printf("🔗 Внешние ссылки: %v\n", config.KeepExternal)
	fmt.Println(strings.Repeat("─", 50))

	if err := processor.Run(); err != nil {
		log.Fatalf("❌ Ошибка: %v", err)
	}

	fmt.Println("✅ Обработка завершена успешно!")
}
