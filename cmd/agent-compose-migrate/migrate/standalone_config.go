package migrate

import (
	"encoding/json"
	"fmt"
	"sort"
)

type legacyOctoBusServer struct {
	URL   string `json:"url"`
	Token string `json:"token,omitempty"`
}

func legacyProjectOctoBusServers(agents []convertedStandaloneAgent) ([]map[string]any, error) {
	servers := make(map[string]legacyOctoBusServer)
	owners := make(map[string]string)
	for _, agent := range agents {
		var config struct {
			OctoBusServers json.RawMessage `json:"octobus_servers"`
		}
		if err := json.Unmarshal([]byte(agent.definition.configJSON), &config); err != nil || len(config.OctoBusServers) == 0 || string(config.OctoBusServers) == "null" {
			continue
		}
		var agentServers map[string]legacyOctoBusServer
		if err := json.Unmarshal(config.OctoBusServers, &agentServers); err != nil {
			return nil, fmt.Errorf("decode standalone agent %s OctoBus servers: %w", agent.definition.id, err)
		}
		for name, server := range agentServers {
			if existing, exists := servers[name]; exists && existing != server {
				return nil, fmt.Errorf("standalone agents %s and %s define conflicting OctoBus server %s", owners[name], agent.definition.id, name)
			}
			servers[name] = server
			owners[name] = agent.definition.id
		}
	}
	if len(servers) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		server := servers[name]
		item := map[string]any{"name": name, "url": server.URL}
		if server.Token != "" {
			item["token"] = server.Token
		}
		result = append(result, item)
	}
	return result, nil
}

func legacyAgentJSON(item legacyAgentDefinition, name string, scheduler map[string]any) map[string]any {
	result := map[string]any{"name": name, "enabled": item.enabled != 0, "display_name": item.name, "description": item.description, "provider": item.provider, "model": item.model, "system_prompt": item.systemPrompt, "image": item.image, "env": legacyEnvList(item.envJSON), "capset_ids": legacyJSONValue(item.capsetIDs, []any{}), "skills": legacyJSONValue(item.skills, []any{}), "volumes": legacyJSONValue(item.volumesJSON, []any{})}
	if item.driver != "" {
		result["driver"] = map[string]any{"name": item.driver}
	}
	if item.workspaceID != "" {
		result["workspace"] = map[string]any{"name": item.workspaceID}
	}
	var config map[string]any
	if json.Unmarshal([]byte(item.configJSON), &config) == nil {
		if value, ok := config["jupyter"]; ok {
			result["jupyter"] = value
		}
		if value, ok := config["mcp_servers"]; ok {
			result["mcp_servers"] = legacyNamedServerList(value)
		}
	}
	if scheduler != nil {
		result["scheduler"] = scheduler
	}
	return result
}

func legacyNamedServerList(value any) any {
	servers, ok := value.(map[string]any)
	if !ok {
		return value
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]any, 0, len(names))
	for _, name := range names {
		server, ok := servers[name].(map[string]any)
		if !ok {
			result = append(result, servers[name])
			continue
		}
		item := make(map[string]any, len(server)+1)
		for key, field := range server {
			switch key {
			case "env", "headers":
				item[key] = legacyNamedValueList(field)
			default:
				item[key] = field
			}
		}
		item["name"] = name
		result = append(result, item)
	}
	return result
}

func legacyNamedValueList(value any) any {
	values, ok := value.(map[string]any)
	if !ok {
		return value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]any, 0, len(names))
	for _, name := range names {
		field, ok := values[name].(map[string]any)
		if !ok {
			result = append(result, map[string]any{"name": name, "value": values[name]})
			continue
		}
		item := make(map[string]any, len(field)+1)
		for key, nested := range field {
			item[key] = nested
		}
		item["name"] = name
		result = append(result, item)
	}
	return result
}

func legacyEnvList(raw string) []map[string]any {
	var items []map[string]any
	if json.Unmarshal([]byte(raw), &items) != nil {
		return nil
	}
	return items
}

func legacyJSONValue(raw string, fallback any) any {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return fallback
	}
	return value
}
