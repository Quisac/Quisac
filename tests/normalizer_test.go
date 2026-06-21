package tests

import (
	"testing"

	"quisac/src/normalizer"
)

func TestPathToModule(t *testing.T) {
	tests := []struct {
		filePath string
		repoRoot string
		expected string
	}{
		{"/repo/src/services/payment.go", "/repo", "src.services.payment"},
		{"/repo/src/utils/db.go", "/repo", "src.utils.db"},
		{"/repo/main.go", "/repo", "main"},
		{"/repo/src/services/auth.go", "/repo", "src.services.auth"},
	}

	for _, tt := range tests {
		result := normalizer.PathToModule(tt.filePath, tt.repoRoot)
		if result != tt.expected {
			t.Errorf("PathToModule(%q, %q) = %q, want %q", tt.filePath, tt.repoRoot, result, tt.expected)
		}
	}
}

func TestFilterInternal(t *testing.T) {
	graphNodes := map[string]bool{
		"src.services.payment": true,
		"src.utils.db":         true,
		"src.services.auth":    true,
	}

	tests := []struct {
		imports  []string
		expected []string
	}{
		{
			imports:  []string{"fmt", "os", "src.utils.db", "fastapi", "src.services.auth"},
			expected: []string{"src.utils.db", "src.services.auth"},
		},
		{
			imports:  []string{"fmt", "os", "net/http"},
			expected: []string{},
		},
		{
			imports:  []string{"src.services.payment", "src.utils.db"},
			expected: []string{"src.services.payment", "src.utils.db"},
		},
	}

	for _, tt := range tests {
		result := normalizer.FilterInternal(tt.imports, graphNodes)
		if len(result) != len(tt.expected) {
			t.Errorf("FilterInternal(%v) = %v, want %v", tt.imports, result, tt.expected)
			continue
		}
		for i := range result {
			if result[i] != tt.expected[i] {
				t.Errorf("FilterInternal(%v)[%d] = %q, want %q", tt.imports, i, result[i], tt.expected[i])
			}
		}
	}
}

func TestBuildNodeSet(t *testing.T) {
	filePaths := []string{
		"/repo/src/services/payment.go",
		"/repo/src/utils/db.go",
		"/repo/src/services/auth.go",
	}

	result := normalizer.BuildNodeSet(filePaths, "/repo")

	expected := map[string]bool{
		"src.services.payment": true,
		"src.utils.db":         true,
		"src.services.auth":    true,
	}

	for k := range expected {
		if !result[k] {
			t.Errorf("BuildNodeSet missing expected node %q", k)
		}
	}

	if len(result) != len(expected) {
		t.Errorf("BuildNodeSet returned %d nodes, want %d", len(result), len(expected))
	}
}
