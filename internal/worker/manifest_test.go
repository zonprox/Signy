package worker

import (
	"fmt"
	"strings"
	"testing"
)

func TestGenerateManifest(t *testing.T) {
	baseURL := "https://example.com"
	jobID := "test-job-123"

	manifest := GenerateManifest(baseURL, jobID)

	if manifest == "" {
		t.Fatal("manifest should not be empty")
	}

	// Verify it's valid XML-ish
	if !strings.Contains(manifest, "<?xml version=") {
		t.Fatal("should contain XML declaration")
	}
	if !strings.Contains(manifest, "<!DOCTYPE plist") {
		t.Fatal("should contain DOCTYPE")
	}

	// Verify URLs
	expectedIPAURL := fmt.Sprintf("%s/artifacts/%s/signed.ipa", baseURL, jobID)
	if !strings.Contains(manifest, expectedIPAURL) {
		t.Fatalf("should contain IPA URL: %s", expectedIPAURL)
	}

	expectedIconURL := fmt.Sprintf("%s/artifacts/%s/icon.png", baseURL, jobID)
	if !strings.Contains(manifest, expectedIconURL) {
		t.Fatalf("should contain display-image icon URL: %s", expectedIconURL)
	}

	// Verify plist keys
	requiredKeys := []string{
		"software-package",
		"display-image",
		"bundle-identifier",
		"bundle-version",
		"title",
	}
	for _, key := range requiredKeys {
		if !strings.Contains(manifest, key) {
			t.Fatalf("manifest should contain key: %s", key)
		}
	}
}

func TestGenerateManifestDifferentInputs(t *testing.T) {
	m1 := GenerateManifest("https://a.com", "job1")
	m2 := GenerateManifest("https://b.com", "job2")

	if m1 == m2 {
		t.Fatal("different inputs should produce different manifests")
	}

	if !strings.Contains(m1, "https://a.com/artifacts/job1/signed.ipa") {
		t.Fatal("m1 should reference correct URL")
	}
	if !strings.Contains(m2, "https://b.com/artifacts/job2/signed.ipa") {
		t.Fatal("m2 should reference correct URL")
	}
}
