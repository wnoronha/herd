package docs

import (
	"embed"
	"fmt"
	"io/fs"
	"strings"
)

// FS contains all embedded markdown documentation files.
//
//go:embed *.md
var FS embed.FS

// DocInfo represents metadata for an embedded documentation file.
type DocInfo struct {
	Slug        string
	Filename    string
	Title       string
	Description string
}

// ListDocs returns all available embedded documentation topics.
func ListDocs() ([]DocInfo, error) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		return nil, err
	}

	var docList []DocInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		data, err := FS.ReadFile(entry.Name())
		if err != nil {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".md")
		title, desc := parseDocHeader(string(data))
		if title == "" {
			title = slug
		}
		docList = append(docList, DocInfo{
			Slug:        slug,
			Filename:    entry.Name(),
			Title:       title,
			Description: desc,
		})
	}
	return docList, nil
}

// GetDoc returns the content of the specified documentation topic or filename.
func GetDoc(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("document name cannot be empty")
	}

	filename := name
	if !strings.HasSuffix(filename, ".md") {
		filename += ".md"
	}

	data, err := FS.ReadFile(filename)
	if err == nil {
		return string(data), nil
	}

	// Try case-insensitive / prefix matching
	docList, lErr := ListDocs()
	if lErr == nil {
		lower := strings.ToLower(name)
		for _, d := range docList {
			if strings.EqualFold(d.Slug, lower) || strings.EqualFold(d.Filename, lower) || strings.Contains(strings.ToLower(d.Slug), lower) {
				content, err := FS.ReadFile(d.Filename)
				if err == nil {
					return string(content), nil
				}
			}
		}
	}

	return "", fmt.Errorf("document %q not found (run 'herd docs' to list topics)", name)
}

func parseDocHeader(content string) (string, string) {
	lines := strings.Split(content, "\n")
	var title, desc string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") && title == "" {
			title = strings.TrimPrefix(trimmed, "# ")
			continue
		}
		if title != "" && trimmed != "" && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "---") && !strings.HasPrefix(trimmed, "See also") && desc == "" {
			desc = trimmed
			break
		}
	}
	return title, desc
}
