package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// A focused GraphQL implementation for the documents gh and github-mcp-server send.
// It deliberately mirrors the TypeScript implementation in forge-entry/graphql.ts.

// ---------- small JSON / query helpers ----------

func gqlString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func gqlNumber(v any) int {
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

func gqlMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func gqlNamedList(names []string) []any {
	out := make([]any, 0, len(names))
	for _, n := range names {
		out = append(out, map[string]any{"name": n})
	}
	return out
}

func gqlIntrospection(query, typ string) bool {
	a := `__type(name: "` + typ + `")`
	b := `__type(name: \"` + typ + `\")`
	return strings.Contains(query, a) || strings.Contains(query, b)
}

func gqlB64Decode(s string) []byte {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b
	}
	if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
		return b
	}
	padded := s
	for len(padded)%4 != 0 {
		padded += "="
	}
	if b, err := base64.StdEncoding.DecodeString(padded); err == nil {
		return b
	}
	return []byte(s)
}

func gqlFindRepoByID(s *Store, id string) *Repo {
	if id == "" {
		return nil
	}
	for _, r := range s.Repos {
		if r.ID == id {
			return r
		}
	}
	return nil
}

func gqlFindIssueByID(r *Repo, id string) *Issue {
	if r == nil || id == "" {
		return nil
	}
	for _, i := range r.Issues {
		if i.ID == id {
			return i
		}
	}
	return nil
}

func gqlFindPullByID(r *Repo, id string) *Pull {
	if r == nil || id == "" {
		return nil
	}
	for _, p := range r.Pulls {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// ---------- result nodes ----------

func gqlIssueNode(s *Store, r *Repo, i *Issue) map[string]any {
	state := "CLOSED"
	if i.State == "open" {
		state = "OPEN"
	}
	return map[string]any{
		"id":     i.ID,
		"number": i.Number,
		"title":  i.Title,
		"state":  state,
		"body":   i.Body,
	}
}

func gqlPullNode(s *Store, r *Repo, p *Pull) map[string]any {
	state := "CLOSED"
	if p.Merged {
		state = "MERGED"
	} else if p.State == "open" {
		state = "OPEN"
	}
	return map[string]any{
		"id":          p.ID,
		"number":      p.Number,
		"headRefName": p.Head,
		"baseRefName": p.Base,
		"mergeable":   "MERGEABLE",
		"state":       state,
		"merged":      p.Merged,
		"headRefOid":  p.HeadOID,
	}
}

func gqlRepoNode(s *Store, r *Repo) map[string]any {
	if r == nil {
		return nil
	}
	return map[string]any{
		"id":               r.ID,
		"databaseId":       r.DatabaseID,
		"name":             r.Name,
		"nameWithOwner":    r.FullName,
		"owner":            map[string]any{"login": r.Owner},
		"defaultBranchRef": map[string]any{"name": r.DefaultBranch},
		"viewerPermission": "ADMIN",
		"hasIssuesEnabled": true,
	}
}

func gqlSortIssues(issues []*Issue) {
	sort.SliceStable(issues, func(a, b int) bool {
		if issues[a].CreatedAt == issues[b].CreatedAt {
			return issues[a].Number > issues[b].Number
		}
		return issues[a].CreatedAt > issues[b].CreatedAt
	})
}

// ---------- single operation ----------

func gqlExecute(s *Store, op any) map[string]any {
	m := gqlMap(op)
	q := gqlString(m["query"])
	v := gqlMap(m["variables"])
	data := map[string]any{}

	withError := func(message string) map[string]any {
		return map[string]any{"data": data, "errors": []any{map[string]any{"message": message}}}
	}
	ok := func() map[string]any { return map[string]any{"data": data} }

	if strings.Contains(q, "noSuchFieldOnViewer") {
		return withError(`Cannot query field "noSuchFieldOnViewer" on type "User".`)
	}
	if strings.Contains(q, "query ViewerLogin") || strings.Contains(q, "viewer {") {
		data["viewer"] = map[string]any{"login": s.Viewer}
		return ok()
	}

	if gqlIntrospection(q, "Issue") {
		data["Issue"] = map[string]any{"fields": gqlNamedList([]string{
			"id", "number", "title", "state", "body", "createdAt", "updatedAt", "labels", "comments",
		})}
		return ok()
	}
	if gqlIntrospection(q, "PullRequest") || gqlIntrospection(q, "Repository") {
		if gqlIntrospection(q, "PullRequest") {
			data["PullRequest"] = map[string]any{"fields": gqlNamedList([]string{
				"id", "number", "headRefName", "baseRefName", "mergeable", "state", "merged", "files", "title", "body",
			})}
		}
		if gqlIntrospection(q, "Repository") {
			data["Repository"] = map[string]any{"fields": gqlNamedList([]string{
				"id", "databaseId", "name", "nameWithOwner", "owner", "defaultBranchRef",
				"viewerPermission", "hasIssuesEnabled", "issue", "issues", "pullRequests", "pullRequest", "description",
			})}
		}
		return ok()
	}
	if gqlIntrospection(q, "SearchType") {
		data["SearchType"] = map[string]any{"enumValues": gqlNamedList([]string{
			"ISSUE", "REPOSITORY", "USER", "DISCUSSION",
		})}
		return ok()
	}

	if strings.Contains(q, "node(id:") {
		repo := gqlFindRepoByID(s, gqlString(v["id"]))
		if repo == nil {
			data["node"] = nil
		} else {
			data["node"] = map[string]any{
				"id": repo.ID, "name": repo.Name, "nameWithOwner": repo.FullName, "__typename": "Repository",
			}
		}
		return ok()
	}

	// ---- mutations ----

	if strings.Contains(q, "createIssue") {
		input := gqlMap(v["input"])
		repo := gqlFindRepoByID(s, gqlString(input["repositoryId"]))
		if repo == nil {
			return withError("repository not found")
		}
		issue := s.AddIssue(repo, gqlString(input["title"]), gqlString(input["body"]), nil)
		data["createIssue"] = map[string]any{"issue": map[string]any{
			"number": issue.Number, "id": issue.ID,
		}}
		return ok()
	}

	if strings.Contains(q, "closeIssue") {
		input := gqlMap(v["input"])
		id := gqlString(input["issueId"])
		var issue *Issue
		for _, r := range s.Repos {
			if found := gqlFindIssueByID(r, id); found != nil {
				issue = found
				break
			}
		}
		if issue != nil {
			issue.State = "closed"
			data["closeIssue"] = map[string]any{"issue": map[string]any{
				"number": issue.Number, "id": issue.ID, "state": "CLOSED",
			}}
		} else {
			data["closeIssue"] = map[string]any{"issue": nil}
		}
		return ok()
	}

	if strings.Contains(q, "createPullRequest") {
		input := gqlMap(v["input"])
		repo := gqlFindRepoByID(s, gqlString(input["repositoryId"]))
		if repo == nil {
			return withError("repository not found")
		}
		pull := s.AddPull(repo, gqlString(input["title"]), "", gqlString(input["headRefName"]), gqlString(input["baseRefName"]))
		seen := map[string]bool{}
		files := []string{}
		if headMap := repo.BranchContents[pull.Head]; headMap != nil {
			for path := range headMap {
				if !seen[path] {
					seen[path] = true
					files = append(files, path)
				}
			}
		}
		for path := range repo.Contents {
			if !seen[path] {
				seen[path] = true
				files = append(files, path)
			}
		}
		if len(files) > 10 {
			files = files[:10]
		}
		pull.Files = files
		data["createPullRequest"] = map[string]any{"pullRequest": map[string]any{
			"number": pull.Number, "id": pull.ID, "state": "OPEN",
			"headRefName": pull.Head, "baseRefName": pull.Base,
		}}
		return ok()
	}

	if strings.Contains(q, "mergePullRequest") {
		id := gqlString(gqlMap(v["input"])["pullRequestId"])
		var result map[string]any
		for _, r := range s.Repos {
			if p := gqlFindPullByID(r, id); p != nil {
				p.Merged = true
				p.State = "closed"
				result = map[string]any{"pullRequest": map[string]any{
					"number": p.Number, "merged": true, "state": "CLOSED",
				}}
				break
			}
		}
		if result == nil {
			result = map[string]any{"pullRequest": nil}
		}
		data["mergePullRequest"] = result
		return ok()
	}

	if strings.Contains(q, "addComment") {
		input := gqlMap(v["input"])
		id := gqlString(input["subjectId"])
		body := gqlString(input["body"])
		var node map[string]any
		for _, r := range s.Repos {
			if issue := gqlFindIssueByID(r, id); issue != nil {
				c := s.Comment(r, issue.Number, body)
				node = map[string]any{"id": strconv.Itoa(c.ID), "body": c.Body}
				break
			}
		}
		if node == nil {
			node = map[string]any{"id": s.GlobalID("C", 1), "body": body}
		}
		data["addComment"] = map[string]any{"commentEdge": map[string]any{"node": node}}
		return ok()
	}

	// ---- repository queries ----

	if strings.Contains(q, "repository(") {
		repo := s.Owned(gqlString(v["owner"]), gqlString(v["name"]))
		if repo == nil {
			data["repository"] = nil
			return ok()
		}

		if strings.Contains(q, "issues(") {
			issues := append([]*Issue(nil), repo.Issues...)
			if strings.Contains(q, "states: [OPEN]") || strings.Contains(q, "states:[OPEN]") || strings.Contains(q, "[OPEN]") {
				filtered := issues[:0]
				for _, i := range issues {
					if i.State == "open" {
						filtered = append(filtered, i)
					}
				}
				issues = filtered
			}
			if strings.Contains(q, "orderBy") {
				gqlSortIssues(issues)
			}

			if strings.Contains(q, "pageInfo") {
				first := gqlNumber(v["first"])
				if first == 0 {
					first = 1
				}
				after := 0
				if afterVar := v["after"]; afterVar != nil && gqlString(afterVar) != "" {
					decoded := string(gqlB64Decode(gqlString(afterVar)))
					n, _ := strconv.Atoi(strings.TrimPrefix(decoded, "cursor:"))
					after = n
				}
				if after < 0 {
					after = 0
				}
				if after > len(issues) {
					after = len(issues)
				}
				end := after + first
				if end > len(issues) {
					end = len(issues)
				}
				if end < after {
					end = after
				}
				slice := issues[after:end]
				nodes := make([]any, 0, len(slice))
				for _, i := range slice {
					nodes = append(nodes, map[string]any{"number": i.Number})
				}
				data["repository"] = map[string]any{"issues": map[string]any{
					"nodes": nodes,
					"pageInfo": map[string]any{
						"hasNextPage": after+first < len(issues),
						"endCursor":   base64.StdEncoding.EncodeToString([]byte("cursor:" + strconv.Itoa(after+len(slice)))),
					},
				}}
				return ok()
			}

			limit := gqlNumber(v["first"])
			if limit <= 0 || limit > len(issues) {
				limit = len(issues)
			}
			nodes := make([]any, 0, limit)
			for _, i := range issues[:limit] {
				nodes = append(nodes, gqlIssueNode(s, repo, i))
			}
			data["repository"] = map[string]any{"issues": map[string]any{"nodes": nodes}}
			return ok()
		}

		if strings.Contains(q, "pullRequests") {
			limit := gqlNumber(v["first"])
			if limit <= 0 || limit > len(repo.Pulls) {
				limit = len(repo.Pulls)
			}
			nodes := make([]any, 0, limit)
			for _, p := range repo.Pulls[:limit] {
				nodes = append(nodes, gqlPullNode(s, repo, p))
			}
			data["repository"] = map[string]any{"pullRequests": map[string]any{"nodes": nodes}}
			return ok()
		}

		if strings.Contains(q, "pullRequest(") {
			pull := s.FindPull(repo, gqlNumber(v["number"]))
			if pull == nil {
				data["repository"] = map[string]any{"pullRequest": nil}
				return ok()
			}
			paths := pull.Files
			if len(paths) == 0 {
				keys := make([]string, 0, len(repo.Contents))
				for path := range repo.Contents {
					keys = append(keys, path)
				}
				sort.Strings(keys)
				if len(keys) > 5 {
					keys = keys[:5]
				}
				paths = keys
			}
			fileNodes := make([]any, 0, len(paths))
			for _, path := range paths {
				fileNodes = append(fileNodes, map[string]any{"path": path})
			}
			node := gqlPullNode(s, repo, pull)
			node["files"] = map[string]any{"nodes": fileNodes}
			data["repository"] = map[string]any{"pullRequest": node}
			return ok()
		}

		if strings.Contains(q, "issue(") {
			issue := s.FindIssue(repo, gqlNumber(v["number"]))
			if issue == nil {
				data["repository"] = map[string]any{"issue": nil}
			} else {
				data["repository"] = map[string]any{"issue": gqlIssueNode(s, repo, issue)}
			}
			return ok()
		}

		data["repository"] = gqlRepoNode(s, repo)
		return ok()
	}

	return withError("Unsupported operation")
}

// handleGraphQL answers a single operation or a JSON array of operations, in order.
func handleGraphQL(s *Store, q *Req) *Resp {
	var raw any
	if err := json.Unmarshal(q.Body, &raw); err != nil {
		return sendJSON(200, map[string]any{
			"data":   map[string]any{},
			"errors": []any{map[string]any{"message": "Invalid JSON"}},
		})
	}
	if batch, isBatch := raw.([]any); isBatch {
		results := make([]any, 0, len(batch))
		for _, op := range batch {
			results = append(results, gqlExecute(s, op))
		}
		return sendJSON(200, results)
	}
	return sendJSON(200, gqlExecute(s, raw))
}
