package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wnoronha/herd/docs"
)

func docsAction(topic string, exportDir string, all bool) error {
	if exportDir != "" {
		return exportDocs(exportDir)
	}

	if topic == "" && !all {
		return listDocsSummary()
	}

	if all {
		allDocs, err := docs.ListDocs()
		if err != nil {
			return err
		}
		for i, d := range allDocs {
			content, err := docs.GetDoc(d.Slug)
			if err != nil {
				continue
			}
			if i > 0 {
				fmt.Println("\n" + strings.Repeat("=", 80) + "\n")
			}
			fmt.Println(content)
		}
		return nil
	}

	content, err := docs.GetDoc(topic)
	if err != nil {
		return err
	}

	fmt.Println(content)
	return nil
}

func listDocsSummary() error {
	allDocs, err := docs.ListDocs()
	if err != nil {
		return fmt.Errorf("failed to list embedded docs: %w", err)
	}

	fmt.Println("Herd - Embedded Documentation Guides")
	fmt.Println("Usage: herd docs <topic> | herd docs --all | herd docs --export <dir>")
	fmt.Println()
	fmt.Println("Available Topics:")

	for _, d := range allDocs {
		fmt.Printf("  • %-20s %s\n", d.Slug, d.Title)
		if d.Description != "" {
			fmt.Printf("    %s\n", d.Description)
		}
		fmt.Println()
	}

	fmt.Println("Examples:")
	fmt.Println("  herd docs architecture       View system architecture overview")
	fmt.Println("  herd docs commands           View complete CLI reference")
	fmt.Println("  herd docs cross-platform     View compilation and Raspberry Pi guide")
	fmt.Println("  herd docs adk-integration    View Google ADK agent integration guide")
	fmt.Println("  herd docs --export ./my-docs Export all markdown files to a directory")
	return nil
}

func exportDocs(targetDir string) error {
	allDocs, err := docs.ListDocs()
	if err != nil {
		return fmt.Errorf("failed to list docs: %w", err)
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", targetDir, err)
	}

	for _, d := range allDocs {
		content, err := docs.GetDoc(d.Slug)
		if err != nil {
			continue
		}
		destPath := filepath.Join(targetDir, d.Filename)
		if err := os.WriteFile(destPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", destPath, err)
		}
		fmt.Printf("Exported %s\n", destPath)
	}

	fmt.Printf("\nSuccessfully exported %d documents to %s\n", len(allDocs), targetDir)
	return nil
}
