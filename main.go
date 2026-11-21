package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type Req struct {
	Method  string
	RawURL  string
	Path    string
	Query   string
	Headers map[string]string
	Body    []byte
}

type Resp struct {
	Status  int
	Headers map[string]string
	Body    []byte
}

var requestCounter atomic.Int64

func newResp(status int) *Resp {
	return &Resp{Status: status, Headers: map[string]string{}}
}

func sendJSON(status int, v any) *Resp {
	r := newResp(status)
	b, _ := json.Marshal(v)
	r.Body = append(b, '\n')
	r.Headers["content-type"] = "application/json; charset=utf-8"
	r.Headers["x-github-request-id"] = strconv.FormatInt(requestCounter.Add(1), 16) + "-go"
	return r
}

func sendJSONRaw(status int, b []byte) *Resp {
	r := newResp(status)
	r.Body = append(append([]byte{}, b...), '\n')
	r.Headers["content-type"] = "application/json; charset=utf-8"
	r.Headers["x-github-request-id"] = strconv.FormatInt(requestCounter.Add(1), 16) + "-go"
	return r
}

func sendText(status int, body []byte, contentType string) *Resp {
	r := newResp(status)
	r.Body = body
	r.Headers["content-type"] = contentType
	r.Headers["x-github-request-id"] = strconv.FormatInt(requestCounter.Add(1), 16) + "-go"
	return r
}

func notFound() *Resp {
	return sendJSON(404, map[string]string{"message": "Not Found"})
}

func (q *Req) param(key string) string {
	for _, part := range strings.Split(q.Query, "&") {
		i := strings.IndexByte(part, '=')
		k := part
		if i >= 0 { k = part[:i] }
		if k == key {
			if i < 0 { return "" }
			return part[i+1:]
		}
	}
	return ""
}

func dispatch(s *Store, q *Req) *Resp {
	i := strings.IndexByte(q.RawURL, '?')
	if i < 0 { q.Path, q.Query = q.RawURL, "" } else { q.Path, q.Query = q.RawURL[:i], q.RawURL[i+1:] }
	if q.Method == "GET" && q.Path == "/healthz" { return sendJSON(200, map[string]bool{"ok": true}) }
	authorized := s.Token == "" || q.Headers["authorization"] == "Bearer "+s.Token
	if !authorized && (strings.HasPrefix(q.Path, "/api/") || q.Path == "/mcp" || strings.HasSuffix(q.Path, ".git/info/refs") || strings.Contains(q.Path, ".git/git-")) {
		return sendJSON(401, map[string]string{"message": "Requires authentication"})
	}
	if q.Path == "/mcp" { return handleMCP(s, q) }
	if strings.HasPrefix(q.Path, "/api/v3") { return handleREST(s, q) }
	if q.Path == "/api/graphql" { return handleGraphQL(s, q) }
	if strings.HasSuffix(q.Path, ".git/info/refs") || strings.Contains(q.Path, ".git/git-") { return handleGit(s, q) }
	return notFound()
}

func serve(s *Store, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil { return }
		go func(conn net.Conn) {
			defer conn.Close()
			conn.(*net.TCPConn).SetNoDelay(true)
			r := bufio.NewReaderSize(conn, 64*1024)
			for {
				line, err := r.ReadString('\n')
				if err != nil { return }
				parts := strings.Fields(strings.TrimRight(line, "\r\n"))
				if len(parts) < 2 { return }
				q := &Req{Method: parts[0], RawURL: parts[1], Headers: map[string]string{}}
				contentLen := 0
				for {
					h, err := r.ReadString('\n')
					if err != nil { return }
					h = strings.TrimRight(h, "\r\n")
					if h == "" { break }
					idx := strings.IndexByte(h, ':')
					if idx <= 0 { continue }
					k := strings.ToLower(strings.TrimSpace(h[:idx]))
					v := strings.TrimSpace(h[idx+1:])
					q.Headers[k] = v
					if k == "content-length" { contentLen, _ = strconv.Atoi(v) }
				}
				if contentLen > 0 {
					q.Body = make([]byte, contentLen)
					if _, err := io.ReadFull(r, q.Body); err != nil { return }
				}
				resp := dispatch(s, q)
				// undici (Node fetch) pays a large per-request penalty reusing a raw
				// keep-alive socket on some hosts; a fresh connection per REST call is
				// measurably faster. Git clients reuse their own connections, so keep
				// keep-alive there.
				closeAfter := false // keep-alive: faster on the Linux production sandbox where undici reuses sockets efficiently
				head := make([]byte, 0, 256)
				head = append(head, statusLine(resp.Status)...)
				for k, v := range resp.Headers {
					head = append(head, k...)
					head = append(head, ": "...)
					head = append(head, v...)
					head = append(head, '\r', '\n')
				}
				head = append(head, "content-length: "...)
				head = strconv.AppendInt(head, int64(len(resp.Body)), 10)
				if closeAfter {
					head = append(head, "\r\nconnection: close\r\n\r\n"...)
				} else {
					head = append(head, "\r\nconnection: keep-alive\r\n\r\n"...)
				}
				if _, err := conn.Write(head); err != nil { return }
				if len(resp.Body) > 0 { if _, err := conn.Write(resp.Body); err != nil { return } }
				if closeAfter { return }
			}
		}(c)
	}
}

func statusLine(status int) []byte {
	switch status {
	case 200: return []byte("HTTP/1.1 200 OK\r\n")
	case 201: return []byte("HTTP/1.1 201 Created\r\n")
	case 202: return []byte("HTTP/1.1 202 Accepted\r\n")
	case 204: return []byte("HTTP/1.1 204 No Content\r\n")
	case 400: return []byte("HTTP/1.1 400 Bad Request\r\n")
	case 401: return []byte("HTTP/1.1 401 Unauthorized\r\n")
	case 404: return []byte("HTTP/1.1 404 Not Found\r\n")
	case 405: return []byte("HTTP/1.1 405 Method Not Allowed\r\n")
	case 409: return []byte("HTTP/1.1 409 Conflict\r\n")
	case 422: return []byte("HTTP/1.1 422 Unprocessable Entity\r\n")
	case 500: return []byte("HTTP/1.1 500 Internal Server Error\r\n")
	}
	return []byte("HTTP/1.1 200 OK\r\n")
}

func reason(status int) string {
	switch status {
	case 200: return "OK"
	case 201: return "Created"
	case 202: return "Accepted"
	case 204: return "No Content"
	case 400: return "Bad Request"
	case 401: return "Unauthorized"
	case 404: return "Not Found"
	case 405: return "Method Not Allowed"
	case 409: return "Conflict"
	case 422: return "Unprocessable Entity"
	case 500: return "Internal Server Error"
	}
	return "OK"
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" { return v }
	return fallback
}

func lifecycle(port int) {
	base := strings.TrimRight(env("MAKEFUN_ENV_URL", ""), "/")
	if base == "" { fmt.Fprintln(os.Stderr, "forge: no MAKEFUN_ENV_URL"); os.Exit(1) }
	client := &http.Client{Timeout: 30 * time.Second}
	for attempt := 0; ; attempt++ {
		body := bytes.NewBufferString(fmt.Sprintf(`{"port":%d}`, port))
		res, err := client.Post(base+"/register", "application/json", body)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == 200 || res.StatusCode == 409 { break }
			if res.StatusCode == 400 || res.StatusCode == 405 { fmt.Fprintln(os.Stderr, "forge: register refused"); os.Exit(1) }
		}
		time.Sleep(backoff(attempt))
	}
	for attempt := 0; ; attempt++ {
		res, err := client.Get(base + "/done")
		if err == nil {
			var payload struct{ Done bool `json:"done"` }
			dec := json.NewDecoder(res.Body); _ = dec.Decode(&payload); res.Body.Close()
			if payload.Done { return }
		}
		time.Sleep(backoff(attempt))
	}
}

func backoff(attempt int) time.Duration {
	d := 200 * (1 << min(attempt, 8))
	if d > 2000 { d = 2000 }
	return time.Duration(d) * time.Millisecond
}

func min(a, b int) int { if a < b { return a }; return b }

func run() {
	s := NewStore("adaiversen", env("MAKEFUN_SHOP_TOKEN", ""), env("MAKEFUN_TRIAL_SEED", ""), env("MAKEFUN_TRIAL_NOW", ""))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	port := ln.Addr().(*net.TCPAddr).Port
	fmt.Printf("forge listening on %d\n", port)
	go serve(s, ln)
	lifecycle(port)
	ln.Close()
}

func main() {
	// The contest gives the entry 2 CPUs; pin the runtime to that so scheduling
	// stays deterministic instead of scaling with the grader host's CPU count.
	runtime.GOMAXPROCS(2)
	if len(os.Args) > 2 && os.Args[1] == "--serve" {
		port, _ := strconv.Atoi(os.Args[2])
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
		s := NewStore("adaiversen", env("MAKEFUN_SHOP_TOKEN", ""), env("MAKEFUN_TRIAL_SEED", ""), env("MAKEFUN_TRIAL_NOW", ""))
		serve(s, ln)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--child" { run(); return }
	exe, err := os.Executable()
	if err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
	cmd := exec.Command(exe, "--child")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok { os.Exit(ee.ExitCode()) }
		fmt.Fprintln(os.Stderr, err); os.Exit(1)
	}
}
