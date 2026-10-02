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

	expectedTopics := []string{"architecture", "commands", "cross-platform", "protocol"}
	found := make(map[string]bool)
	for _, d := range docList {
		found[d.Slug] = true
		if d.Title == "" {
			t.Errorf("document %s has empty title", d.Slug)
		}
	}

	for _, exp := range expectedTopics {
		if !found[exp] {
			t.Errorf("expected topic %s to be embedded", exp)
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
