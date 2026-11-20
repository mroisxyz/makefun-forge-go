package main

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// handleREST ports the REST v3 surface of the TypeScript forge to Go.
// It always returns a response (dispatch routes every /api/v3 path here).
func handleREST(s *Store, q *Req) *Resp {
	m, ok := parseRESTBody(q.Body)
	if !ok {
		return sendJSON(400, map[string]any{"message": "Problems parsing JSON"})
	}

	path := q.Path
	if path == "" {
		path = q.RawURL
		if i := strings.IndexByte(path, '?'); i >= 0 {
			path = path[:i]
		}
	}
	seg := restSegments(path)

	if len(seg) == 0 {
		r := sendJSON(200, map[string]any{
			"current_user_url": "/api/v3/user",
			"rate_limit_url":   "/api/v3/rate_limit",
			"repository_url":   "/api/v3/repos",
		})
		r.Headers["x-oauth-scopes"] = "repo, read:org"
		return r
	}

	if seg[0] == "meta" && len(seg) == 1 {
		return sendJSON(200, map[string]any{
			"installed_version":                 "1.0.0",
			"verifiable_password_authentication": false,
		})
	}
	if seg[0] == "rate_limit" && len(seg) == 1 {
		return sendJSON(200, map[string]any{
			"resources": map[string]any{
				"core": map[string]any{"limit": 5000, "remaining": 5000, "reset": 0},
			},
		})
	}
	if seg[0] == "user" && len(seg) == 1 && q.Method == "GET" {
		return sendJSON(200, map[string]any{
			"login": s.Viewer, "id": 1, "type": "User", "name": s.Viewer,
		})
	}

	if seg[0] == "user" && len(seg) >= 2 && seg[1] == "repos" {
		if q.Method == "GET" && len(seg) == 2 {
			page := queryInt(q, "page", 1)
			perPage := queryInt(q, "per_page", 30)
			owned := make([]*Repo, 0, len(s.Repos))
			for _, r := range s.Repos {
				if r.Owner == s.Viewer {
					owned = append(owned, r)
				}
			}
			sort.Slice(owned, func(i, j int) bool { return owned[i].DatabaseID < owned[j].DatabaseID })
			all := make([]any, 0, len(owned))
			for _, r := range owned {
				all = append(all, repoJSON(r))
			}
			return paginated(q, all, "/api/v3/user/repos", page, perPage)
		}
		if q.Method == "POST" && len(seg) == 2 {
			name := bodyStr(m, "name")
			if name == "" {
				return sendJSON(422, map[string]any{
					"message": "Validation Failed",
					"errors":  []any{map[string]any{"field": "name", "code": "missing"}},
				})
			}
			if existing := s.Get(s.Viewer, name); existing != nil {
				return sendJSON(201, repoJSON(existing))
			}
			r := s.CreateRepo(name, bodyStr(m, "description"), bodyBool(m, "private"))
			return sendJSON(201, repoJSON(r))
		}
	}

	if seg[0] == "repos" && len(seg) >= 3 {
		owner, name := seg[1], seg[2]
		r := s.Owned(owner, name)
		rest := seg[3:]

		if len(rest) == 0 {
			if r == nil {
				return notFound()
			}
			switch q.Method {
			case "GET":
				return sendJSON(200, repoJSON(r))
			case "PATCH":
				if v, present := m["description"]; present {
					r.Description = toStr(v)
				}
				if v, present := m["default_branch"]; present {
					r.DefaultBranch = toStr(v)
				}
				return sendJSON(200, repoJSON(r))
			case "DELETE":
				s.DeleteRepo(owner, name)
				return sendJSON(204, map[string]any{})
			}
		}

		if r == nil {
			return notFound()
		}

		if len(rest) >= 1 && rest[0] == "contents" && len(rest) >= 2 {
			branch := qget(q, "ref")
			if branch == "" {
				branch = bodyStr(m, "branch")
			}
			cm := contentMap(r, branch)
			target := strings.Join(rest[1:], "/")

			switch q.Method {
			case "PUT":
				content := bodyStr(m, "content")
				old := cm[target]
				given, present := m["sha"]
				givenSha := toStr(given)
				if old != nil {
					if !present || given == nil || givenSha == "" {
						return sendJSON(409, map[string]string{"message": "sha missing"})
					}
					if givenSha != old.SHA {
						return sendJSON(409, map[string]string{"message": "sha mismatch"})
					}
				}
				blob := b64decode(content)
				blobOID := PutObject(r, "blob", blob)
				cm[target] = &ContentEntry{Path: target, Content: content, SHA: blobOID, BlobOID: blobOID}
				msg := bodyStrDef(m, "message", "update "+target)
				commit := makeContentCommit(r, target, msg, blobOID, branch)
				status := 201
				if old != nil {
					status = 200
				}
				return sendJSON(status, map[string]any{
					"content": map[string]any{
						"name": lastSegment(target), "path": target, "sha": blobOID,
						"type": "file", "encoding": "base64", "content": content, "size": len(blob),
					},
					"commit": map[string]any{"sha": commit, "message": bodyStr(m, "message")},
				})
			case "GET":
				prefix := strings.TrimSuffix(target, "/") + "/"
				dir := []any{}
				for _, p := range sortedKeys(cm) {
					if strings.HasPrefix(p, prefix) {
						item := cm[p]
						name := p[len(prefix):]
						if i := strings.IndexByte(name, '/'); i >= 0 {
							name = name[:i]
						}
						dir = append(dir, map[string]any{
							"type": "file", "name": name, "path": p,
							"sha": item.SHA, "size": len(b64decode(item.Content)),
						})
					}
				}
				if len(dir) > 0 {
					return sendJSON(200, dir)
				}
				item := cm[target]
				if item == nil {
					return notFound()
				}
				return sendJSON(200, map[string]any{
					"name": lastSegment(target), "path": target, "sha": item.SHA,
					"type": "file", "encoding": "base64", "content": item.Content,
					"size": len(b64decode(item.Content)),
				})
			case "DELETE":
				item := cm[target]
				if item == nil {
					return notFound()
				}
				delete(cm, target)
				msg := bodyStrDef(m, "message", "delete "+target)
				commit := makeContentCommit(r, target, msg, item.BlobOID, branch)
				return sendJSON(200, map[string]any{
					"content": nil,
					"commit":  map[string]any{"sha": commit, "message": bodyStr(m, "message")},
				})
			}
		}

		if len(rest) == 1 && rest[0] == "commits" && q.Method == "GET" {
			branch := qget(q, "sha")
			if branch == "" {
				branch = r.DefaultBranch
			}
			order := []string{}
			seen := map[string]bool{}
			cur := r.Refs["refs/heads/"+branch]
			for cur != "" && !seen[cur] {
				seen[cur] = true
				order = append(order, cur)
				c := r.Commits[cur]
				if c == nil {
					break
				}
				cur = c.Parent
			}
			out := make([]any, 0, len(order))
			for _, sha := range order {
				message := ""
				if c := r.Commits[sha]; c != nil {
					message = c.Message
				}
				out = append(out, map[string]any{
					"sha": sha, "commit": map[string]any{"message": message},
				})
			}
			return sendJSON(200, out)
		}

		if len(rest) >= 1 && rest[0] == "labels" {
			if q.Method == "POST" {
				name := bodyStr(m, "name")
				r.Labels[name] = true
				n := len(r.Labels)
				return sendJSON(201, map[string]any{
					"id": n + 9000, "node_id": s.GlobalID("L", n), "name": name,
					"color": bodyStrDef(m, "color", "ededed"),
					"description": bodyStrDef(m, "description", ""),
				})
			}
			if q.Method == "GET" {
				out := []any{}
				for _, name := range sortedKeys(r.Labels) {
					out = append(out, map[string]any{"name": name})
				}
				return sendJSON(200, out)
			}
		}

		if len(rest) >= 1 && rest[0] == "issues" {
			if len(rest) == 1 && q.Method == "GET" {
				state := qget(q, "state")
				if state == "" {
					state = "open"
				}
				list := []*Issue{}
				for _, i := range r.Issues {
					if state == "all" || i.State == state {
						list = append(list, i)
					}
				}
				if direction := qget(q, "direction"); direction != "" {
					sort.Slice(list, func(a, b int) bool {
						if direction == "desc" {
							return list[a].Number > list[b].Number
						}
						return list[a].Number < list[b].Number
					})
				}
				out := make([]any, 0, len(list))
				for _, i := range list {
					out = append(out, issueJSON(s, r, i))
				}
				return sendJSON(200, out)
			}
			if len(rest) == 1 && q.Method == "POST" {
				labels := []string{}
				if arr, ok := m["labels"].([]any); ok {
					for _, lv := range arr {
						switch x := lv.(type) {
						case string:
							labels = append(labels, x)
						case map[string]any:
							labels = append(labels, toStr(x["name"]))
						default:
							labels = append(labels, "")
						}
					}
				}
				var unknown string
				foundUnknown := false
				for _, l := range labels {
					if !r.Labels[l] {
						unknown = l
						foundUnknown = true
						break
					}
				}
				if foundUnknown && unknown != "" {
					return sendJSON(422, map[string]any{
						"message": "Validation Failed",
						"errors":  []any{map[string]any{"field": "labels", "code": "invalid", "value": unknown}},
					})
				}
				i := s.AddIssue(r, bodyStr(m, "title"), bodyStr(m, "body"), labels)
				return sendJSON(201, issueJSON(s, r, i))
			}
			if len(rest) >= 2 {
				number, _ := strconv.Atoi(rest[1])
				if len(rest) >= 3 && rest[2] == "comments" && q.Method == "GET" {
					return sendJSON(200, commentsJSON(r, number))
				}
				issue := s.FindIssue(r, number)
				if issue == nil {
					return notFound()
				}
				if q.Method == "GET" {
					return sendJSONRaw(200, []byte(issueText(s, r, issue)))
				}
				if q.Method == "PATCH" {
					if v, present := m["state"]; present && toStr(v) != "" {
						if toStr(v) == "closed" {
							issue.State = "closed"
						} else {
							issue.State = "open"
						}
					}
					if v, present := m["title"]; present {
						issue.Title = toStr(v)
					}
					if v, present := m["body"]; present {
						issue.Body = toStr(v)
					}
					issue.Serialized = ""
					return sendJSON(200, issueJSON(s, r, issue))
				}
				if len(rest) >= 3 && rest[2] == "comments" && q.Method == "POST" {
					c := s.Comment(r, number, bodyStr(m, "body"))
					return sendJSON(201, map[string]any{
						"id": c.ID, "body": c.Body, "created_at": c.CreatedAt,
					})
				}
			}
		}

		if len(rest) >= 1 && rest[0] == "pulls" {
			if len(rest) == 1 && q.Method == "GET" {
				state := qget(q, "state")
				if state == "" {
					state = "open"
				}
				out := []any{}
				for _, p := range r.Pulls {
					if state == "all" || p.State == state {
						out = append(out, pullJSON(s, r, p))
					}
				}
				return sendJSON(200, out)
			}
			if len(rest) == 1 && q.Method == "POST" {
				p := s.AddPull(r, bodyStr(m, "title"), bodyStr(m, "body"), bodyStr(m, "head"), bodyStr(m, "base"))
				headMap := r.BranchContents[p.Head]
				if headMap != nil {
					p.Files = []string{}
					for _, path := range sortedKeys(headMap) {
						cur := r.Contents[path]
						if cur == nil || headMap[path] == nil || cur.SHA != headMap[path].SHA {
							p.Files = append(p.Files, path)
						}
					}
				}
				if len(p.Files) == 0 {
					keys := []string{}
					if headMap != nil {
						keys = sortedKeys(headMap)
					}
					if len(keys) > 5 {
						keys = keys[:5]
					}
					p.Files = keys
				}
				return sendJSON(201, pullJSON(s, r, p))
			}
			if len(rest) >= 2 {
				number, _ := strconv.Atoi(rest[1])
				p := s.FindPull(r, number)
				if p == nil {
					return notFound()
				}
				if len(rest) == 2 && q.Method == "GET" {
					return sendJSON(200, pullJSON(s, r, p))
				}
				if len(rest) == 2 && q.Method == "PATCH" {
					if v, present := m["state"]; present && toStr(v) != "" {
						p.State = toStr(v)
					}
					return sendJSON(200, pullJSON(s, r, p))
				}
				if len(rest) >= 3 && rest[2] == "merge" && q.Method == "PUT" {
					if p.State == "closed" || p.Merged {
						return sendJSON(405, map[string]string{"message": "Pull Request is not mergeable"})
					}
					methodName := bodyStrDef(m, "merge_method", "squash")
					headMap := r.BranchContents[p.Head]
					conflict := false
					for _, f := range p.Files {
						if !r.MergedPaths[f] {
							continue
						}
						var hSHA, cSHA string
						if headMap != nil {
							if item := headMap[f]; item != nil {
								hSHA = item.SHA
							}
						}
						if item := r.Contents[f]; item != nil {
							cSHA = item.SHA
						}
						if hSHA != cSHA {
							conflict = true
							break
						}
					}
					if conflict {
						return sendJSON(405, map[string]string{"message": "Pull Request has conflicts"})
					}
					if headMap != nil {
						for _, path := range sortedKeys(headMap) {
							item := headMap[path]
							r.Contents[path] = item
							r.MergedPaths[path] = true
							if item != nil {
								PutObject(r, "blob", b64decode(item.Content))
							}
						}
					}
					p.Merged = true
					p.State = "closed"
					p.MergedAt = s.Now
					sha := squashCommit(r, p, methodName)
					p.MergeCommitSHA = sha
					return sendJSON(200, map[string]any{
						"sha": sha, "merged": true, "message": "Pull Request successfully merged",
					})
				}
				if len(rest) >= 3 && rest[2] == "files" && q.Method == "GET" {
					out := []any{}
					for _, f := range p.Files {
						out = append(out, map[string]any{
							"sha": "", "filename": f, "status": "modified",
							"additions": 1, "deletions": 0, "changes": 1,
						})
					}
					return sendJSON(200, out)
				}
			}
		}

		if len(rest) >= 2 && rest[0] == "git" && rest[1] == "refs" {
			refName := strings.Join(rest[2:], "/")
			fullRef := "refs/" + refName
			switch q.Method {
			case "GET":
				sha := r.Refs[fullRef]
				if sha == "" {
					sha = strings.Repeat("0", 40)
				}
				return sendJSON(200, []any{
					map[string]any{"ref": fullRef, "object": map[string]any{"sha": sha, "type": "commit"}},
				})
			case "POST":
				ref := bodyStrDef(m, "ref", refName)
				sha := bodyStr(m, "sha")
				r.Refs[ref] = sha
				return sendJSON(201, map[string]any{
					"ref": ref, "object": map[string]any{"sha": sha},
				})
			}
		}

		if len(rest) >= 1 && rest[0] == "branches" {
			if q.Method == "GET" {
				names := []string{}
				seen := map[string]bool{}
				if !seen[r.DefaultBranch] {
					seen[r.DefaultBranch] = true
					names = append(names, r.DefaultBranch)
				}
				for ref := range r.Refs {
					if strings.HasPrefix(ref, "refs/heads/") {
						n := ref[len("refs/heads/"):]
						if !seen[n] {
							seen[n] = true
							names = append(names, n)
						}
					}
				}
				out := make([]any, 0, len(names))
				for _, n := range names {
					sha := r.Refs["refs/heads/"+n]
					if sha == "" {
						sha = strings.Repeat("0", 40)
					}
					out = append(out, map[string]any{
						"name": n, "commit": map[string]any{"sha": sha},
					})
				}
				return sendJSON(200, out)
			}
		}

		if len(rest) >= 2 && rest[0] == "commits" && q.Method == "GET" {
			c := r.Commits[rest[1]]
			if c == nil {
				return sendJSON(404, map[string]string{"message": "Not Found"})
			}
			parents := []any{}
			if c.Parent != "" {
				parents = append(parents, map[string]any{"sha": c.Parent})
			}
			return sendJSON(200, map[string]any{
				"sha": c.OID, "commit": map[string]any{"message": c.Message}, "parents": parents,
			})
		}

		return notFound()
	}

	return notFound()
}

// ---------- body / query helpers ----------

func parseRESTBody(body []byte) (map[string]any, bool) {
	if len(body) == 0 {
		return map[string]any{}, true
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, false
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, true
}

func restSegments(path string) []string {
	p := path
	if strings.HasPrefix(p, "/api/v3") {
		p = p[len("/api/v3"):]
		if strings.HasPrefix(p, "/") {
			p = p[1:]
		}
	}
	parts := strings.Split(p, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		if dec, err := url.PathUnescape(part); err == nil {
			part = dec
		}
		out = append(out, part)
	}
	return out
}

// qget uses q.param() and URL-decodes the value.
func qget(q *Req, key string) string {
	v, _ := url.QueryUnescape(q.param(key))
	return v
}

func queryInt(q *Req, key string, def int) int {
	v := q.param(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return string(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

func bodyStr(m map[string]any, key string) string { return toStr(m[key]) }

func bodyStrDef(m map[string]any, key, def string) string {
	v, present := m[key]
	if !present || v == nil {
		return def
	}
	return toStr(v)
}

func bodyBool(m map[string]any, key string) bool {
	switch x := m[key].(type) {
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	default:
		return false
	}
}

func b64decode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		if b2, e2 := base64.RawStdEncoding.DecodeString(s); e2 == nil {
			return b2
		}
	}
	return b
}

func lastSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ---------- JSON shapes ----------

func repoJSON(r *Repo) map[string]any {
	return map[string]any{
		"id": r.ID, "node_id": r.ID, "name": r.Name, "full_name": r.FullName,
		"owner":       map[string]any{"login": r.Owner, "type": "User"},
		"private":     r.Private,
		"description": r.Description,
		"html_url":    "/" + r.Owner + "/" + r.Name,
		"clone_url":   "/" + r.Owner + "/" + r.Name + ".git",
		"default_branch": r.DefaultBranch,
		"created_at":     r.CreatedAt,
		"updated_at":     r.UpdatedAt,
		"has_issues":     true,
		"size":           len(r.Objects),
	}
}

func issueJSON(s *Store, r *Repo, i *Issue) map[string]any {
	labels := make([]any, 0, len(i.Labels))
	for _, name := range i.Labels {
		labels = append(labels, map[string]any{"name": name})
	}
	return map[string]any{
		"id": i.DatabaseID, "node_id": i.ID, "number": i.Number,
		"title": i.Title, "body": i.Body, "state": i.State,
		"labels": labels,
		"user":   map[string]any{"login": s.Viewer, "type": "User"},
		"comments": len(r.Comments[i.Number]),
		"created_at": i.CreatedAt, "updated_at": i.CreatedAt,
		"html_url": "/" + r.FullName + "/issues/" + strconv.Itoa(i.Number),
	}
}

func pullJSON(s *Store, r *Repo, p *Pull) map[string]any {
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
		"id": p.Number + 900000, "node_id": p.ID, "number": p.Number,
		"title": p.Title, "body": p.Body, "state": p.State,
		"merged": p.Merged, "merged_at": p.MergedAt, "merge_commit_sha": p.MergeCommitSHA,
		"head": map[string]any{"ref": p.Head, "sha": headSHA},
		"base": map[string]any{"ref": p.Base, "sha": baseSHA},
		"user": map[string]any{"login": s.Viewer, "type": "User"},
		"created_at": p.CreatedAt, "updated_at": p.CreatedAt,
		"html_url": "/" + r.FullName + "/pull/" + strconv.Itoa(p.Number),
	}
}

func commentsJSON(r *Repo, n int) []any {
	cs := r.Comments[n]
	out := make([]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{"id": c.ID, "body": c.Body, "created_at": c.CreatedAt})
	}
	return out
}

func issueText(s *Store, r *Repo, i *Issue) string {
	if i.Serialized == "" {
		b, _ := json.Marshal(issueJSON(s, r, i))
		i.Serialized = string(b)
	}
	return i.Serialized
}

func paginated(q *Req, items []any, pathname string, page, perPage int) *Resp {
	start := (page - 1) * perPage
	if start < 0 {
		start = 0
	}
	if start > len(items) {
		start = len(items)
	}
	end := start + perPage
	if end > len(items) {
		end = len(items)
	}
	slice := items[start:end]
	r := sendJSON(200, slice)
	if start+perPage < len(items) {
		host := q.Headers["host"]
		if host == "" {
			host = "127.0.0.1"
		}
		next := "http://" + host + pathname + "?page=" + strconv.Itoa(page+1) + "&per_page=" + strconv.Itoa(perPage)
		r.Headers["link"] = "<" + next + ">; rel=\"next\", <" + next + ">; rel=\"last\""
	}
	return r
}

// ---------- git-object helpers ----------

func contentMap(r *Repo, branch string) map[string]*ContentEntry {
	if branch == "" || branch == r.DefaultBranch {
		return r.Contents
	}
	if m, ok := r.BranchContents[branch]; ok {
		return m
	}
	m := make(map[string]*ContentEntry, len(r.Contents))
	for k, v := range r.Contents {
		m[k] = v
	}
	r.BranchContents[branch] = m
	return m
}

func buildTree(path, blobOID string) []byte {
	name := lastSegment(path)
	if name == "" {
		name = "file"
	}
	head := append([]byte("100644 "+name), 0)
	raw, _ := hex.DecodeString(blobOID)
	return append(head, raw...)
}

func makeContentCommit(r *Repo, path, message, blobOID, branch string) string {
	if branch == "" {
		branch = r.DefaultBranch
	}
	parent := r.Refs["refs/heads/"+branch]
	treeOID := PutObject(r, "tree", buildTree(path, blobOID))
	oid := MakeCommit(r, treeOID, parent, message)
	r.Refs["refs/heads/"+branch] = oid
	return oid
}

func squashCommit(r *Repo, p *Pull, method string) string {
	parent := r.Refs["refs/heads/"+p.Base]
	if parent == "" {
		parent = p.HeadOID
	}
	treeOID := PutObject(r, "tree", []byte{})
	message := p.Title + "\n\nSquashed " + method + " commit"
	oid := MakeCommit(r, treeOID, parent, message)
	r.Refs["refs/heads/"+p.Base] = oid
	return oid
}
