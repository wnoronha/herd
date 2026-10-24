package agent

import (
	"context"

	"google.golang.org/adk/v2/artifact"
	"google.golang.org/genai"
)

// saveArtifactToADK stores one version in the ADK artifact service under the
// shared session scope. Best-effort: returns 0 when the service is missing.
func saveArtifactToADK(ctx context.Context, eng *Engine, name, content string, metadata map[string]any) int64 {
	if eng == nil {
		return 0
	}
	svc := eng.artifactService()
	if svc == nil {
		return 0
	}
	resp, err := svc.Save(ctx, &artifact.SaveRequest{
		AppName:        herdADKAppName,
		UserID:         herdADKUserID,
		SessionID:      artifactSharedSession,
		FileName:       name,
		Part:           &genai.Part{Text: content},
		CustomMetadata: metadata,
	})
	if err != nil || resp == nil {
		return 0
	}
	return resp.Version
}

// loadArtifactFromADK loads the latest (or a pinned version) from the ADK
// service. It reports found=false when missing so callers fall back to disk.
func loadArtifactFromADK(ctx context.Context, eng *Engine, name string, version int64) (string, int64, bool) {
	if eng == nil {
		return "", 0, false
	}
	svc := eng.artifactService()
	if svc == nil {
		return "", 0, false
	}
	loaded, err := svc.Load(ctx, &artifact.LoadRequest{
		AppName:   herdADKAppName,
		UserID:    herdADKUserID,
		SessionID: artifactSharedSession,
		FileName:  name,
		Version:   version,
	})
	if err != nil || loaded == nil || loaded.Part == nil {
		return "", 0, false
	}
	text := loaded.Part.Text
	if text == "" && loaded.Part.InlineData != nil {
		text = string(loaded.Part.InlineData.Data)
	}
	if version > 0 {
		return text, version, true
	}
	// Resolve latest version number for the response.
	vers, err := svc.Versions(ctx, &artifact.VersionsRequest{
		AppName:   herdADKAppName,
		UserID:    herdADKUserID,
		SessionID: artifactSharedSession,
		FileName:  name,
	})
	if err != nil || vers == nil || len(vers.Versions) == 0 {
		return text, 0, true
	}
	var latest int64
	for _, v := range vers.Versions {
		if v > latest {
			latest = v
		}
	}
	return text, latest, true
}

type adkVersionedEntry struct {
	ArtifactMeta
	Version int64 `json:"version,omitempty"`
}

// listArtifactsFromADK returns ADK versioned entries not already seen on disk.
func listArtifactsFromADK(ctx context.Context, eng *Engine, seen map[string]bool) []adkVersionedEntry {
	if eng == nil {
		return nil
	}
	svc := eng.artifactService()
	if svc == nil {
		return nil
	}
	listed, err := svc.List(ctx, &artifact.ListRequest{
		AppName:   herdADKAppName,
		UserID:    herdADKUserID,
		SessionID: artifactSharedSession,
	})
	if err != nil || listed == nil {
		return nil
	}
	var out []adkVersionedEntry
	for _, name := range listed.FileNames {
		if seen[name] {
			continue
		}
		vers, err := svc.Versions(ctx, &artifact.VersionsRequest{
			AppName:   herdADKAppName,
			UserID:    herdADKUserID,
			SessionID: artifactSharedSession,
			FileName:  name,
		})
		var v int64
		if err == nil && vers != nil && len(vers.Versions) > 0 {
			for _, e := range vers.Versions {
				if e > v {
					v = e
				}
			}
		}
		out = append(out, adkVersionedEntry{
			ArtifactMeta: ArtifactMeta{Name: name, Size: 0},
			Version:      v,
		})
	}
	return out
}

// deleteArtifactFromADK removes all versions. Best-effort.
func deleteArtifactFromADK(ctx context.Context, eng *Engine, name string) {
	if eng == nil {
		return
	}
	svc := eng.artifactService()
	if svc == nil {
		return
	}
	_ = svc.Delete(ctx, &artifact.DeleteRequest{
		AppName:   herdADKAppName,
		UserID:    herdADKUserID,
		SessionID: artifactSharedSession,
		FileName:  name,
	})
}

// versionsArtifactFromADK lists all versions of one artifact.
func versionsArtifactFromADK(ctx context.Context, eng *Engine, name string) (any, error) {
	svc := eng.artifactService()
	if svc == nil {
		return map[string]any{"name": name, "versions": []any{}, "count": 0}, nil
	}
	vers, err := svc.Versions(ctx, &artifact.VersionsRequest{
		AppName:   herdADKAppName,
		UserID:    herdADKUserID,
		SessionID: artifactSharedSession,
		FileName:  name,
	})
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	if vers != nil {
		for _, v := range vers.Versions {
			out = append(out, map[string]any{"version": v})
		}
	}
	return map[string]any{"name": name, "versions": out, "count": len(out)}, nil
}
