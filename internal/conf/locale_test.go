package conf

import (
    "fmt"
    "os"
    "strings"
    "sync"
    "testing"
    "time"
)

func setupTestEnvironment(t *testing.T) {
    if t != nil {
        t.Helper()
    }
    os.Unsetenv("LANG")
    os.Unsetenv("LC_ALL")
    os.Unsetenv("LC_MESSAGES")
}

func TestMain(m *testing.M) {
    setupTestEnvironment(nil)
    code := m.Run()
    os.Exit(code)
}

func TestParseLocale_ValidFormats(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"simple language", "en", "en"},
        {"language with region", "en-US", "en-US"},
        {"language with country", "fr-FR", "fr-FR"},
        {"complex locale", "zh-Hans-CN", "zh-Hans-CN"},
        {"case normalization", "EN-us", "en-US"},
        {"lowercase input", "de-de", "de-DE"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := ParseLocale(tt.input)
            if err != nil {
                t.Errorf("ParseLocale(%q) returned unexpected error: %v", tt.input, err)
            }
            if result != tt.expected {
                t.Errorf("ParseLocale(%q) = %q, want %q", tt.input, result, tt.expected)
            }
        })
    }
}

func TestParseLocale_InvalidFormats(t *testing.T) {
    invalidLocales := []struct {
        name  string
        input string
    }{
        {"empty string", ""},
        {"whitespace only", "   "},
        {"invalid format", "invalid"},
        {"trailing dash", "en-"},
        {"leading dash", "-US"},
        {"multiple dashes", "en--US"},
        {"numeric only", "123"},
        {"too many parts", "en-US-GB-DE"},
        {"special characters", "en@US"},
        {"underscore format", "en_US"},
    }

    for _, tt := range invalidLocales {
        t.Run(tt.name, func(t *testing.T) {
            result, err := ParseLocale(tt.input)
            if err == nil {
                t.Errorf("ParseLocale(%q) = %q, expected error", tt.input, result)
            }
            if result != "" {
                t.Errorf("ParseLocale(%q) returned %q, expected empty string on error", tt.input, result)
            }
        })
    }
}

func TestLoadLocaleConfig_Defaults(t *testing.T) {
    config := LoadLocaleConfig()

    if config == nil {
        t.Fatal("LoadLocaleConfig() returned nil")
    }

    if config.DefaultLocale == "" {
        t.Error("Default locale should not be empty")
    }

    if config.FallbackLocale == "" {
        t.Error("Fallback locale should not be empty")
    }

    found := false
    for _, locale := range config.SupportedLocales {
        if locale == config.DefaultLocale {
            found = true
            break
        }
    }
    if !found {
        t.Error("Default locale should be in supported locales list")
    }
}

func TestLoadLocaleConfig_WithEnvironment(t *testing.T) {
    tests := []struct {
        name    string
        envVar  string
        envVal  string
        checker func(*testing.T, *LocaleConfig)
    }{
        {
            "LANG environment variable",
            "LANG",
            "fr-FR",
            func(t *testing.T, config *LocaleConfig) {
                if config.DefaultLocale != "fr-FR" {
                    t.Errorf("Expected default locale 'fr-FR', got %q", config.DefaultLocale)
                }
            },
        },
        {
            "LC_ALL overrides LANG",
            "LC_ALL",
            "de-DE",
            func(t *testing.T, config *LocaleConfig) {
                if config.DefaultLocale != "de-DE" {
                    t.Errorf("Expected default locale 'de-DE', got %q", config.DefaultLocale)
                }
            },
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            old := os.Getenv(tt.envVar)
            os.Setenv(tt.envVar, tt.envVal)
            t.Cleanup(func() {
                if old == "" {
                    os.Unsetenv(tt.envVar)
                } else {
                    os.Setenv(tt.envVar, old)
                }
            })

            config := LoadLocaleConfig()
            tt.checker(t, config)
        })
    }
}

func TestValidateLocale(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        valid   bool
        message string
    }{
        {"valid simple", "en", true, ""},
        {"valid with region", "en-US", true, ""},
        {"valid complex", "zh-Hans-CN", true, ""},
        {"invalid empty", "", false, "locale cannot be empty"},
        {"invalid format", "invalid-format-here", false, "invalid locale format"},
        {"invalid characters", "en@US", false, "locale contains invalid characters"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := ValidateLocale(tt.input)
            if tt.valid && err != nil {
                t.Errorf("ValidateLocale(%q) returned unexpected error: %v", tt.input, err)
            }
            if !tt.valid && err == nil {
                t.Errorf("ValidateLocale(%q) expected error but got none", tt.input)
            }
        })
    }
}

func TestNormalizeLocale(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"already normalized", "en-US", "en-US"},
        {"lowercase", "en-us", "en-US"},
        {"uppercase", "EN-US", "en-US"},
        {"mixed case", "En-Us", "en-US"},
        {"simple language", "en", "en"},
        {"complex locale", "zh-hans-cn", "zh-Hans-CN"},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := NormalizeLocale(tt.input)
            if result != tt.expected {
                t.Errorf("NormalizeLocale(%q) = %q, want %q", tt.input, result, tt.expected)
            }
        })
    }
}

func TestGetFallbackLocale(t *testing.T) {
    tests := []struct {
        name      string
        requested string
        supported []string
        expected  string
    }{
        {
            "exact match",
            "en-US",
            []string{"en-US", "fr-FR", "de-DE"},
            "en-US",
        },
        {
            "language fallback",
            "en-GB",
            []string{"en-US", "fr-FR", "de-DE"},
            "en-US",
        },
        {
            "no match use default",
            "ja-JP",
            []string{"en-US", "fr-FR", "de-DE"},
            "en-US",
        },
        {
            "empty supported list",
            "en-US",
            []string{},
            "en",
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result := GetFallbackLocale(tt.requested, tt.supported)
            if result != tt.expected {
                t.Errorf("GetFallbackLocale(%q, %v) = %q, want %q",
                    tt.requested, tt.supported, result, tt.expected)
            }
        })
    }
}

func TestLocaleConfig_ConcurrentAccess(t *testing.T) {
    const numGoroutines = 100
    const numIterations = 1000

    var wg sync.WaitGroup
    errors := make(chan error, numGoroutines)

    for i := 0; i < numGoroutines; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            for j := 0; j < numIterations; j++ {
                config := LoadLocaleConfig()
                if config == nil {
                    errors <- fmt.Errorf("LoadLocaleConfig returned nil")
                    return
                }
                if err := ValidateLocale("en-US"); err != nil {
                    errors <- fmt.Errorf("ValidateLocale failed: %v", err)
                    return
                }
                result := NormalizeLocale("en-us")
                if result != "en-US" {
                    errors <- fmt.Errorf("NormalizeLocale returned %q, expected 'en-US'", result)
                    return
                }
            }
        }()
    }

    wg.Wait()
    close(errors)

    for err := range errors {
        t.Error(err)
    }
}

func BenchmarkParseLocale(b *testing.B) {
    locales := []string{"en", "en-US", "fr-FR", "de-DE", "zh-Hans-CN"}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        locale := locales[i%len(locales)]
        _, err := ParseLocale(locale)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkValidateLocale(b *testing.B) {
    locales := []string{"en", "en-US", "fr-FR", "de-DE", "zh-Hans-CN"}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        locale := locales[i%len(locales)]
        err := ValidateLocale(locale)
        if err != nil {
            b.Fatal(err)
        }
    }
}

func BenchmarkNormalizeLocale(b *testing.B) {
    locales := []string{"en", "EN-us", "Fr-fr", "DE-de", "zh-hans-cn"}

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        locale := locales[i%len(locales)]
        _ = NormalizeLocale(locale)
    }
}

func TestLocale_EdgeCases(t *testing.T) {
    t.Run("very long locale string", func(t *testing.T) {
        longLocale := strings.Repeat("a", 1000)
        _, err := ParseLocale(longLocale)
        if err == nil {
            t.Error("Expected error for extremely long locale string")
        }
    })

    t.Run("unicode characters", func(t *testing.T) {
        unicodeLocale := "en-🇺🇸"
        _, err := ParseLocale(unicodeLocale)
        if err == nil {
            t.Error("Expected error for locale with unicode characters")
        }
    })

    t.Run("null bytes", func(t *testing.T) {
        nullLocale := "en\x00US"
        _, err := ParseLocale(nullLocale)
        if err == nil {
            t.Error("Expected error for locale with null bytes")
        }
    })

    t.Run("memory allocation", func(t *testing.T) {
        for i := 0; i < 10000; i++ {
            _, _ = ParseLocale("en-US")
            _ = ValidateLocale("en-US")
            _ = NormalizeLocale("en-us")
        }
    })
}

func TestLocale_Timeout(t *testing.T) {
    timeout := time.Second * 5

    done := make(chan bool)
    go func() {
        for i := 0; i < 1000; i++ {
            LoadLocaleConfig()
        }
        done <- true
    }()

    select {
    case <-done:
    case <-time.After(timeout):
        t.Error("LoadLocaleConfig operations took too long")
    }
}

func TestLoadLocaleFromFile(t *testing.T) {
    tmpfile, err := os.CreateTemp("", "locale_test_*.json")
    if err != nil {
        t.Fatal(err)
    }
    defer os.Remove(tmpfile.Name())

    configJSON := ` + "`" + `{
        "default_locale": "en-US",
        "fallback_locale": "en",
        "supported_locales": ["en-US", "fr-FR", "de-DE"]
    }` + "`" + `
    if _, err := tmpfile.Write([]byte(configJSON)); err != nil {
        t.Fatal(err)
    }
    tmpfile.Close()

    result, err := LoadLocaleFromFile(tmpfile.Name())
    if err != nil {
        t.Fatalf("LoadLocaleFromFile failed: %v", err)
    }
    if result.DefaultLocale != "en-US" {
        t.Errorf("Expected default locale 'en-US', got %q", result.DefaultLocale)
    }
}