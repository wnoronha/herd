package agent

// registerDefaultToolMap builds the default cluster tool registry for an engine.
func registerDefaultToolMap(cctx ClusterContext, eng *Engine) map[string]ToolDefinition {
	toolList := BuildDefaultTools(cctx, eng)
	toolsMap := make(map[string]ToolDefinition, len(toolList)+1)
	for _, t := range toolList {
		toolsMap[t.Name] = t
	}
	toolsMap["run_agent"] = buildRunAgentTool(eng)
	if _, ok := toolsMap["memory_recall"]; !ok {
		toolsMap["memory_recall"] = buildMemoryRecallTool(eng)
	}
	return toolsMap
}
