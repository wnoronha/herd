package docs

import (
	"strings"
	"testing"
)

func TestEmbeddedDocs(t *testing.T) {
	docList, err := ListDocs()
	if err != nil {
		t.Fatalf("ListDocs failed: %v", err)
	}

	if len(docList) == 0 {
		t.Fatalf("expected embedded documentation topics, found none")
	}

	// Structural checks (no hardcoded topic allowlist): every embedded
	// guide must be retrievable with a title and non-empty content so new
	// guides don't require a test bump.
	seen := make(map[string]bool)
	for _, d := range docList {
		if d.Slug == "" {
			t.Errorf("document %q has empty slug", d.Title)
		}
		if seen[d.Slug] {
			t.Errorf("duplicate embedded slug %q", d.Slug)
		}
		seen[d.Slug] = true
		if d.Title == "" {
			t.Errorf("document %s has empty title", d.Slug)
		}
		content, err := GetDoc(d.Slug)
		if err != nil {
			t.Errorf("GetDoc(%q) failed: %v", d.Slug, err)
			continue
		}
		if len(strings.TrimSpace(content)) == 0 {
			t.Errorf("document %s has empty content", d.Slug)
		}
	}

	// Test GetDoc
	archDoc, err := GetDoc("architecture")
	if err != nil {
		t.Fatalf("GetDoc('architecture') failed: %v", err)
	}
	if !strings.Contains(archDoc, "Herd Architecture") {
		t.Errorf("expected content to mention 'Herd Architecture'")
	}

	// Test case-insensitive / partial match
	commandsDoc, err := GetDoc("COMMANDS")
	if err != nil {
		t.Fatalf("GetDoc('COMMANDS') case-insensitive lookup failed: %v", err)
	}
	if !strings.Contains(commandsDoc, "Commands Reference") {
		t.Errorf("expected content to mention 'Commands Reference'")
	}

	// Test non-existent doc
	_, err = GetDoc("non-existent-guide")
	if err == nil {
		t.Errorf("expected error for non-existent document")
	}
}
