package main

import (
	"fmt"
	"math/big"
)

type GitObject struct {
	OID  string
	Type string
	Body []byte
}

type GitCommit struct {
	OID, Tree, Parent, Message string
}

type ContentEntry struct {
	Path, Content, SHA, BlobOID string
}

type Issue struct {
	ID         string
	Number     int
	Title      string
	Body       string
	State      string
	Labels     []string
	CreatedAt  string
	DatabaseID int
	Serialized string
}

type Pull struct {
	ID             string
	Number         int
	Title          string
	Body           string
	State          string
	Merged         bool
	Head           string
	Base           string
	HeadOID        string
	Files          []string
	CreatedAt      string
	MergedAt       string
	MergeCommitSHA string
}

type Comment struct {
	ID        int
	Body      string
	CreatedAt string
}

type Repo struct {
	ID            string
	DatabaseID    int
	Name          string
	Owner         string
	FullName      string
	Description   string
	Private       bool
	DefaultBranch string
	CreatedAt     string
	UpdatedAt     string
	Refs          map[string]string
	Objects       map[string]*GitObject
	Commits       map[string]*GitCommit
	Contents      map[string]*ContentEntry
	Issues        []*Issue
	Pulls         []*Pull
	Comments      map[int][]Comment
	BranchContents map[string]map[string]*ContentEntry
	MergedPaths   map[string]bool
	Labels        map[string]bool
	Packs         map[string][]byte
	NextIssue     int
	NextPull      int
}

type Store struct {
	Viewer      string
	Token       string
	Seed        string
	Now         string
	Repos       map[string]*Repo
	NextRepoID  int
	NextGlobal  int
	RequestID   int
}

func NewStore(viewer, token, seed, now string) *Store {
	if now == "" { now = "2026-11-20T00:00:00Z" }
	if seed == "" { seed = "6a09e667f3bcc909" }
	return &Store{Viewer: viewer, Token: token, Seed: seed, Now: now, Repos: map[string]*Repo{}, NextRepoID: 1, NextGlobal: 1}
}

func (s *Store) Key(owner, name string) string { return lower(owner) + "/" + lower(name) }

func (s *Store) Get(owner, name string) *Repo { return s.Repos[s.Key(owner, name)] }

func (s *Store) Owned(owner, name string) *Repo {
	if lower(owner) != lower(s.Viewer) { return nil }
	return s.Get(owner, name)
}

func (s *Store) CreateRepo(name string, desc string, private bool) *Repo {
	id := s.NextRepoID; s.NextRepoID++
	r := &Repo{
		ID: s.GlobalID("R", id), DatabaseID: id, Name: name, Owner: s.Viewer,
		FullName: s.Viewer + "/" + name, Description: desc, Private: private,
		DefaultBranch: "main", CreatedAt: s.Now, UpdatedAt: s.Now,
		Refs: map[string]string{}, Objects: map[string]*GitObject{}, Commits: map[string]*GitCommit{},
		Contents: map[string]*ContentEntry{}, Issues: []*Issue{}, Pulls: []*Pull{},
		Comments: map[int][]Comment{}, BranchContents: map[string]map[string]*ContentEntry{},
		MergedPaths: map[string]bool{}, Labels: map[string]bool{}, Packs: map[string][]byte{},
		NextIssue: 1, NextPull: 1,
	}
	s.Repos[s.Key(s.Viewer, name)] = r
	return r
}

func (s *Store) DeleteRepo(owner, name string) bool {
	k := s.Key(owner, name)
	if _, ok := s.Repos[k]; !ok { return false }
	delete(s.Repos, k)
	return true
}

func (s *Store) GlobalID(prefix string, n int) string {
	seed := new(big.Int)
	seed.SetString("0x"+s.Seed[:8], 0)
	mul := new(big.Int)
	mul.SetString("9e3779b97f4a7c15", 16)
	v := new(big.Int).Mul(big.NewInt(int64(n)), mul)
	v.Add(v, seed)
	return prefix + "_" + v.Text(36)
}

func (s *Store) AddIssue(r *Repo, title, body string, labels []string) *Issue {
	n := r.NextIssue; r.NextIssue++
	i := &Issue{ID: s.GlobalID("I", n+r.DatabaseID*100000), Number: n, Title: title, Body: body,
		State: "open", Labels: append([]string{}, labels...), CreatedAt: s.Now, DatabaseID: n}
	r.Issues = append(r.Issues, i)
	return i
}

func (s *Store) AddPull(r *Repo, title, body, head, base string) *Pull {
	n := r.NextPull; r.NextPull++
	p := &Pull{ID: s.GlobalID("PR", n+r.DatabaseID*100000), Number: n, Title: title, Body: body,
		State: "open", Head: head, Base: base, HeadOID: r.Refs["refs/heads/"+head], CreatedAt: s.Now}
	r.Pulls = append(r.Pulls, p)
	return p
}

func (s *Store) FindIssue(r *Repo, n int) *Issue {
	for _, i := range r.Issues { if i.Number == n { return i } }
	return nil
}

func (s *Store) FindPull(r *Repo, n int) *Pull {
	for _, p := range r.Pulls { if p.Number == n { return p } }
	return nil
}

func (s *Store) Comment(r *Repo, n int, body string) Comment {
	c := Comment{ID: len(r.Comments[n]) + n*1000 + 1, Body: body, CreatedAt: s.Now}
	r.Comments[n] = append(r.Comments[n], c)
	return c
}

func lower(v string) string {
	b := []byte(v)
	for i, c := range b { if c >= 'A' && c <= 'Z' { b[i] = c + 32 } }
	return string(b)
}

func jsonString(v string) string {
	out := []byte{'"'}
	for _, c := range []byte(v) {
		switch c {
		case '"': out = append(out, '\\', '"')
		case '\\': out = append(out, '\\', '\\')
		case '\n': out = append(out, '\\', 'n')
		case '\r': out = append(out, '\\', 'r')
		case '\t': out = append(out, '\\', 't')
		default:
			if c < 32 { out = append(out, []byte(fmt.Sprintf("\\u%04x", c))...) } else { out = append(out, c) }
		}
	}
	return string(append(out, '"'))
}
