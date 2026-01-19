package proccesor

import (
	"os"
	"testing"
)

func TestResolveTargetPath(t *testing.T) {
	p := &Processor{
		cfg: Config{
			Dir:          "testdata",
			OriginalHost: "gopedia.ru",
		},
	}

	testCases := []struct {
		name        string
		currentFile string
		inputURL    string
		expected    string
	}{
		// --- Базовые переходы ---
		{
			name:        "Same directory reference",
			currentFile: "testdata/study/beginning/index.html",
			inputURL:    "/study/beginning/",
			expected:    "./index.html",
		},
		{
			name:        "Parent directory reference",
			currentFile: "testdata/study/beginning/index.html",
			inputURL:    "/study/",
			expected:    "../index.html",
		},
		{
			name:        "Sibling directory reference",
			currentFile: "testdata/study/beginning/index.html",
			inputURL:    "/study/advanced/",
			expected:    "../advanced/index.html",
		},
		{
			name:        "Root reference from nested directory",
			currentFile: "testdata/study/beginning/index.html",
			inputURL:    "/",
			expected:    "../../index.html",
		},

		// --- Специфические случаи ---
		{
			name:        "Windows-style path normalization",
			currentFile: "testdata\\study\\beginning\\index.html",
			inputURL:    "/study/beginning/page.html",
			expected:    "./page.html",
		},
		{
			name:        "External host untouched",
			currentFile: "testdata/index.html",
			inputURL:    "https://google.com/search?q=test",
			expected:    "https://google.com/search?q=test",
		},
		{
			name:        "Preserve Query and Fragment",
			currentFile: "testdata/study/index.html",
			inputURL:    "/study/beginning/index.html?section=1#anchor",
			expected:    "./beginning/index.html?section=1#anchor",
		},
		{
			name:        "PHP to HTML conversion",
			currentFile: "testdata/index.html",
			inputURL:    "/contacts.php",
			expected:    "./contacts.html",
		},
		{
			name:        "Implicit directory to index.html",
			currentFile: "testdata/index.html",
			inputURL:    "/study",
			expected:    "./study/index.html",
		},
		{
			name:        "Deep jump to sibling assets",
			currentFile: "testdata/study/beginning/index.html",
			inputURL:    "/assets/css/style.css",
			expected:    "../../assets/css/style.css",
		},
		{
			name:        "Original problem case: /study to /study/beginning",
			currentFile: "testdata/study/index.html",
			inputURL:    "/study/beginning/",
			expected:    "./beginning/index.html",
		},
		// {
		// 	name:        "Special protocols untouched",
		// 	currentFile: "testdata/index.html",
		// 	inputURL:    "mailto:admin@gopedia.ru",
		// 	expected:    "mailto:admin@gopedia.ru",
		// },
	}

	// Подготовка окружения
	setupTestData()
	defer os.RemoveAll("testdata")

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, ok := p.resolveTargetPath(tc.currentFile, tc.inputURL)
			if !ok {
				t.Errorf("Failed to resolve: %s", tc.name)
				return
			}
			if result != tc.expected {
				t.Errorf("\nInput URL: %s\nExpected:  %s\nGot:       %s", tc.inputURL, tc.expected, result)
			}
		})
	}
}

func setupTestData() {
	os.MkdirAll("testdata/study/beginning", 0755)
	os.MkdirAll("testdata/study/advanced", 0755)
	os.MkdirAll("testdata/assets/css", 0755)
	os.WriteFile("testdata/index.html", []byte(""), 0644)
	os.WriteFile("testdata/study/index.html", []byte(""), 0644)
	os.WriteFile("testdata/study/beginning/index.html", []byte(""), 0644)
	os.WriteFile("testdata/study/advanced/index.html", []byte(""), 0644)
}
