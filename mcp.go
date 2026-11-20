package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// MCP 2026-07-28 (stateless) with a 2025-11-25 initialize fallback.
// Authentication is already enforced by dispatch() in main.go.

var mcpTools = []any{
	map[string]any{
		"name":        "create_repo",
		"description": "Create a repository for the authenticated user.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":        map[string]any{"type": "string"},
				"description": map[string]any{"type": "string"},
				"private":     map[string]any{"type": "boolean"},
			},
			"required": []string{"name"},
		},
	},
	map[string]any{
		"name":        "get_repo",
		"description": "Get a repository by owner and name.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner":     map[string]any{"type": "string"},
				"repo":      map[string]any{"type": "string"},
				"full_name": map[string]any{"type": "string"},
			},
		},
	},
	map[string]any{
		"name":        "create_issue",
		"description": "Create an issue in a repository.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string"},
				"repo":  map[string]any{"type": "string"},
				"title": map[string]any{"type": "string"},
				"body":  map[string]any{"type": "string"},
			},
			"required": []string{"owner", "repo", "title"},
		},
	},
	map[string]any{
		"name":        "list_issues",
		"description": "List issues in a repository.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string"},
				"repo":  map[string]any{"type": "string"},
				"state": map[string]any{"type": "string"},
			},
			"required": []string{"owner", "repo"},
		},
	},
	map[string]any{
		"name":        "create_pull_request",
		"description": "Open a pull request between two branches.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner": map[string]any{"type": "string"},
				"repo":  map[string]any{"type": "string"},
				"title": map[string]any{"type": "string"},
				"body":  map[string]any{"type": "string"},
				"head":  map[string]any{"type": "string"},
				"base":  map[string]any{"type": "string"},
			},
			"required": []string{"owner", "repo", "title", "head", "base"},
		},
	},
	map[string]any{
		"name":        "merge_pull_request",
		"description": "Merge a pull request.",
		"inputSchema": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"owner":       map[string]any{"type": "string"},
				"repo":        map[string]any{"type": "string"},
				"pull_number": map[string]any{"type": "integer"},
			},
			"required": []string{"owner", "repo", "pull_number"},
		},
	},
}

func mcpToolsListResult() map[string]any {
	return map[string]any{
		"resultType": "complete",
		"tools":      mcpTools,
		"ttlMs":      300000,
		"cacheScope": "public",
	}
}

// ---------- JSON-RPC helpers ----------

func mcpRPC(id any, result any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func mcpRPCError(id any, code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
}

func mcpToolResult(text string, isError bool) map[string]any {
	return map[string]any{
		"resultType": "complete",
		"content":    []any{map[string]any{"type": "text", "text": text}},
		"isError":    isError,
	}
}

func mcpText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---------- argument helpers ----------

func mcpString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return mcpText(v)
}

func mcpNumber(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case float32:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case bool:
		if n {
			return 1
		}
		return 0
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return int(f)
	}
	return 0
}

func mcpBool(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case float64:
		return b != 0
	case string:
		return b != "" && b != "false" && b != "0"
	}
	return false
}

func mcpArg(args map[string]any, names ...string) any {
	for _, name := range names {
		if v, ok := args[name]; ok && v != nil {
			return v
		}
	}
	return nil
}

// mcpOwnerRepo resolves owner/repo, accepting full_name as a synonym.
func mcpOwnerRepo(s *Store, args map[string]any) (string, string) {
	owner := mcpString(mcpArg(args, "owner"))
	name := mcpString(mcpArg(args, "repo"))
	if name == "" {
		if full := mcpString(mcpArg(args, "full_name")); full != "" {
			parts := strings.Split(full, "/")
			name = parts[len(parts)-1]
			if owner == "" && len(parts) >= 2 {
				owner = parts[len(parts)-2]
			}
		}
	}
	if owner == "" {
		owner = s.Viewer
	}
	return owner, name
}

// ---------- REST-shaped serialization for tool results ----------

func mcpRepoJSON(s *Store, r *Repo) map[string]any {
	return map[string]any{
		"id":           r.ID,
		"node_id":      r.ID,
		"name":         r.Name,
		"full_name":    r.FullName,
		"owner":        map[string]any{"login": r.Owner, "type": "User"},
		"private":      r.Private,
		"description":  r.Description,
		"html_url":     "/" + r.Owner + "/" + r.Name,
		"clone_url":    "/" + r.Owner + "/" + r.Name + ".git",
		"default_branch": r.DefaultBranch,
		"created_at":   r.CreatedAt,
		"updated_at":   r.UpdatedAt,
		"has_issues":   true,
		"size":         len(r.Objects),
	}
}

func mcpIssueJSON(s *Store, r *Repo, i *Issue) map[string]any {
	labels := make([]any, 0, len(i.Labels))
	for _, name := range i.Labels {
		labels = append(labels, map[string]any{"name": name})
	}
	return map[string]any{
		"id":         i.DatabaseID,
		"node_id":    i.ID,
		"number":     i.Number,
		"title":      i.Title,
		"body":       i.Body,
		"state":      i.State,
		"labels":     labels,
		"user":       map[string]any{"login": s.Viewer, "type": "User"},
		"comments":   len(r.Comments[i.Number]),
		"created_at": i.CreatedAt,
		"updated_at": i.CreatedAt,
		"html_url":   "/" + r.FullName + "/issues/" + strconv.Itoa(i.Number),
	}
}

func mcpPullJSON(s *Store, r *Repo, p *Pull) map[string]any {
	headSHA := r.Refs["refs/heads/"+p.Head]
	if headSHA == "" {
		headSHA = p.HeadOID
	}
	if headSHA == "" {
		headSHA = strings.Repeat("0", 40)
	}
	baseSHA := r.Refs["refs/heads/"+p.Base]
	if baseSHA == "" {
		baseSHA = strings.Repeat("0", 40)
	}
	return map[string]any{
		"id":              p.Number + 900000,
		"node_id":         p.ID,
		"number":          p.Number,
		"title":           p.Title,
		"body":            p.Body,
		"state":           p.State,
		"merged":          p.Merged,
		"merged_at":       p.MergedAt,
		"merge_commit_sha": p.MergeCommitSHA,
		"head":            map[string]any{"ref": p.Head, "sha": headSHA},
		"base":            map[string]any{"ref": p.Base, "sha": baseSHA},
		"user":            map[string]any{"login": s.Viewer, "type": "User"},
		"created_at":      p.CreatedAt,
		"updated_at":      p.CreatedAt,
		"html_url":        "/" + r.FullName + "/pull/" + strconv.Itoa(p.Number),
	}
}

// ---------- tools/call ----------

type mcpCallError struct {
	Code    int
	Message string
}

func mcpNormalizeName(name string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteRune(c)
		}
	}
	return b.String()
}

func mcpCallTool(s *Store, name string, args map[string]any) (map[string]any, *mcpCallError) {
	n := mcpNormalizeName(name)

	if strings.HasSuffix(n, "createrepo") {
		repoName := mcpString(mcpArg(args, "name"))
		if repoName == "" {
			return nil, &mcpCallError{Code: -32602, Message: "missing name"}
		}
		desc := mcpString(mcpArg(args, "description"))
		private := mcpBool(mcpArg(args, "private"))
		repo := s.CreateRepo(repoName, desc, private)
		return mcpToolResult(mcpText(mcpRepoJSON(s, repo)), false), nil
	}

	if strings.HasSuffix(n, "getrepo") {
		owner, repoName := mcpOwnerRepo(s, args)
		repo := s.Owned(owner, repoName)
		if repo == nil {
			return mcpToolResult(mcpText(map[string]any{"message": "Not Found"}), false), nil
		}
		return mcpToolResult(mcpText(mcpRepoJSON(s, repo)), false), nil
	}

	if strings.HasSuffix(n, "createissue") {
		owner, repoName := mcpOwnerRepo(s, args)
		title := mcpArg(args, "title")
		if repoName == "" || title == nil {
			return nil, &mcpCallError{Code: -32602, Message: "missing owner, repo or title"}
		}
		repo := s.Owned(owner, repoName)
		if repo == nil {
			return mcpToolResult(mcpText(map[string]any{"message": "Not Found"}), true), nil
		}
		issue := s.AddIssue(repo, mcpString(title), mcpString(mcpArg(args, "body")), nil)
		return mcpToolResult(mcpText(mcpIssueJSON(s, repo, issue)), false), nil
	}

	if strings.HasSuffix(n, "listissues") {
		owner, repoName := mcpOwnerRepo(s, args)
		repo := s.Owned(owner, repoName)
		state := mcpString(mcpArg(args, "state"))
		if state == "" {
			state = "open"
		}
		items := make([]any, 0)
		if repo != nil {
			for _, i := range repo.Issues {
				if state == "all" || i.State == state {
					items = append(items, mcpIssueJSON(s, repo, i))
				}
			}
		}
		return mcpToolResult(mcpText(items), false), nil
	}

	if strings.HasSuffix(n, "createpullrequest") {
		owner, repoName := mcpOwnerRepo(s, args)
		title := mcpArg(args, "title")
		head := mcpArg(args, "head")
		base := mcpArg(args, "base")
		if repoName == "" || title == nil || head == nil || base == nil {
			return nil, &mcpCallError{Code: -32602, Message: "missing required argument"}
		}
		repo := s.Owned(owner, repoName)
		if repo == nil {
			return mcpToolResult(mcpText(map[string]any{"message": "Not Found"}), true), nil
		}
		pull := s.AddPull(repo, mcpString(title), mcpString(mcpArg(args, "body")), mcpString(head), mcpString(base))
		return mcpToolResult(mcpText(mcpPullJSON(s, repo, pull)), false), nil
	}

	if strings.HasSuffix(n, "mergepullrequest") {
		owner, repoName := mcpOwnerRepo(s, args)
		number := mcpNumber(mcpArg(args, "pull_number", "number"))
		repo := s.Owned(owner, repoName)
		var pull *Pull
		if repo != nil {
			pull = s.FindPull(repo, number)
		}
		if pull == nil {
			return mcpToolResult(mcpText(map[string]any{"message": "Not Found"}), true), nil
		}
		pull.Merged = true
		pull.State = "closed"
		return mcpToolResult(mcpText(mcpPullJSON(s, repo, pull)), false), nil
	}

	return nil, &mcpCallError{Code: -32601, Message: "Unknown tool: " + name}
}

// handleMCP answers a JSON-RPC 2.0 POST (or a 204 for DELETE).
func handleMCP(s *Store, q *Req) *Resp {
	if q.Method == "DELETE" {
		return newResp(204)
	}
	if q.Method != "POST" {
		return sendJSON(405, map[string]string{"message": "Method Not Allowed"})
	}

	var msg any
	if err := json.Unmarshal(q.Body, &msg); err != nil {
		return sendJSON(400, mcpRPCError(nil, -32700, "Parse error"))
	}

	m, _ := msg.(map[string]any)
	id := m["id"]
	method := mcpString(m["method"])
	params := mcpMap(m["params"])

	var response map[string]any

	switch {
	case method == "server/discover":
		response = mcpRPC(id, map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{"2026-07-28", "2025-11-25"},
			"capabilities":      map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":        map[string]any{"name": "makefun-forge", "version": "1.0.0"},
			"_meta": map[string]any{
				"io.modelcontextprotocol/serverInfo": map[string]any{"name": "makefun-forge", "version": "1.0.0"},
			},
		})
	case method == "initialize":
		response = mcpRPC(id, map[string]any{
			"resultType":       "complete",
			"protocolVersion":  "2025-11-25",
			"capabilities":     map[string]any{"tools": map[string]any{}},
			"serverInfo":       map[string]any{"name": "makefun-forge", "version": "1.0.0"},
		})
	case method == "notifications/initialized" || strings.HasPrefix(method, "notifications/"):
		return newResp(202)
	case method == "tools/list":
		response = mcpRPC(id, mcpToolsListResult())
	case method == "tools/call":
		name := mcpString(params["name"])
		args := mcpMap(params["arguments"])
		result, callErr := mcpCallTool(s, name, args)
		if callErr != nil {
			response = mcpRPCError(id, callErr.Code, callErr.Message)
		} else {
			response = mcpRPC(id, result)
		}
	default:
		response = mcpRPCError(id, -32601, "Method not found: "+method)
	}

	return sendJSON(200, response)
}

func mcpMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
