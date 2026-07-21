package config

import (
	"os"
	"testing"
)

func writeBenchConfig(b *testing.B, content string) string {
	b.Helper()
	f, err := os.CreateTemp(b.TempDir(), "aegis-*.yaml")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		b.Fatal(err)
	}
	f.Close()
	return f.Name()
}

func BenchmarkLoad(b *testing.B) {
	path := writeBenchConfig(b, minimalConfig)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Load(path); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidate(b *testing.B) {
	cfg, err := Load(writeBenchConfig(b, minimalConfig))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := cfg.Validate(); err != nil {
			b.Fatal(err)
		}
	}
}
