package main

import (
	"regexp"
	"strconv"
	"strings"
)

// Git smart HTTP surface: info/refs advertisements, v2 ls-refs/fetch, v0 fetch
// and receive-pack. Ported 1:1 from forge-entry/gitserver.ts.

var gitRepoPathRe = regexp.MustCompile(`^/([^/]+)/([^/]+)\.git/(info/refs|git-upload-pack|git-receive-pack)$`)

func handleGit(s *Store, q *Req) *Resp {
	m := gitRepoPathRe.FindStringSubmatch(q.Path)
	if m == nil {
		return sendText(404, []byte("not found"), "text/plain")
	}
	owner := gitPercentDecode(m[1])
	name := gitPercentDecode(m[2])
	repo := s.Owned(owner, name)
	if repo == nil {
		return sendText(404, []byte("not found"), "text/plain")
	}
	if s.Token != "" && q.Headers["authorization"] != "Bearer "+s.Token {
		return sendText(401, []byte("unauthorized"), "text/plain")
	}

	isInfo := strings.HasSuffix(q.Path, "/info/refs")
	service := "upload-pack"
	if q.param("service") == "git-receive-pack" || strings.Contains(q.Path, "git-receive-pack") {
		service = "receive-pack"
	}

	if isInfo {
		v2 := service == "upload-pack" && q.Headers["git-protocol"] == "version=2"
		ct := gitUPAdvertisement
		if service == "receive-pack" {
			ct = gitRCAdvertisement
		}
		return sendText(200, gitAdvertisement(service, v2, repo), ct)
	}

	if service == "receive-pack" {
		commands := gitParseCommands(q.Body)
		pack := gitExtractPack(q.Body)
		okRefs, ngRefs := gitApplyReceive(repo, commands, pack)
		report := gitReportStatus(okRefs, ngRefs)
		return sendText(200, gitSideBandReport(report), gitRCResult)
	}

	// upload-pack POST: v2 command or v0 want/have.
	lines := gitParsePktLines(q.Body)
	command := gitFirstCommand(lines)
	if command == "ls-refs" {
		if len(lines) > 0 {
			return sendText(200, gitLsRefs(repo, lines[1:]), gitUPResult)
		}
		return sendText(200, gitLsRefs(repo, nil), gitUPResult)
	}
	if command == "fetch" {
		return gitHandleV2Fetch(repo, lines)
	}

	// v0 fetch: wants and haves are in two flush-terminated sections.
	wants, haves := gitCollectWantsHaves(lines)
	tip := gitZero
	if len(wants) > 0 {
		tip = wants[0]
	}
	held := map[string]bool{}
	for _, h := range haves {
		held[h] = true
	}
	objects, _ := gitReachable(repo, tip, gitMaxDepth(), held)
	pack := gitWritePack(objects)
	parts := [][]byte{gitPktString(gitNAK(tip, haves)), gitSideBandPack(pack)}
	return sendText(200, gitConcat(parts...), gitUPResult)
}

func gitCollectWantsHaves(lines [][]byte) ([]string, []string) {
	var wants, haves []string
	for _, line := range lines {
		text := strings.TrimSpace(string(line))
		if strings.HasPrefix(text, "want ") {
			wants = append(wants, strings.Split(text[5:], " ")[0])
		}
		if strings.HasPrefix(text, "have ") {
			haves = append(haves, text[5:])
		}
	}
	return wants, haves
}

func gitHandleV2Fetch(repo *Repo, lines [][]byte) *Resp {
	args := make([]string, 0, len(lines))
	for _, l := range lines[1:] {
		args = append(args, strings.TrimSpace(string(l)))
	}
	var wants, haves, shallows []string
	deepen, deepenSeen := 0, false
	for _, a := range args {
		if strings.HasPrefix(a, "want ") {
			wants = append(wants, strings.Split(a[5:], " ")[0])
		}
		if strings.HasPrefix(a, "have ") {
			haves = append(haves, a[5:])
		}
		if strings.HasPrefix(a, "shallow ") {
			shallows = append(shallows, a[8:])
		}
		if strings.HasPrefix(a, "deepen ") && !deepenSeen {
			deepenSeen = true
			v, err := strconv.Atoi(strings.TrimSpace(a[7:]))
			if err != nil {
				deepen = -1 // emulate Number('...') === NaN
			} else {
				deepen = v
			}
		}
	}

	depth := 0
	if deepen > 0 {
		depth = deepen
	} else if len(shallows) > 0 {
		depth = 2
	}
	held := map[string]bool{}
	if deepen <= 0 {
		for _, h := range haves {
			held[h] = true
		}
		for _, sh := range shallows {
			held[sh] = true
		}
	}
	if deepen == 0 {
		for _, have := range haves {
			if _, ok := repo.Objects[have]; !ok {
				continue
			}
			objs, _ := gitReachable(repo, have, gitMaxDepth(), map[string]bool{})
			for _, obj := range objs {
				held[obj.OID] = true
			}
		}
	}

	tip := gitZero
	if len(wants) > 0 {
		tip = wants[0]
	} else if keys := gitSortedKeys(repo.Refs); len(keys) > 0 {
		tip = repo.Refs[keys[0]]
	}
	for _, ref := range gitSortedKeys(repo.Refs) {
		if strings.HasPrefix(ref, "refs/tags/") && gitContains(wants, repo.Refs[ref]) {
			tip = repo.Refs[ref]
			break
		}
	}

	scanDepth := gitMaxDepth()
	if depth > 0 {
		scanDepth = depth
	}
	all, shallowOut := gitReachable(repo, tip, scanDepth, held)
	objects := make([]*GitObject, 0, len(all))
	for _, obj := range all {
		if !held[obj.OID] {
			objects = append(objects, obj)
		}
	}
	pack := gitWritePack(objects)

	var parts [][]byte
	parts = append(parts, gitPktString("acknowledgments\n"))
	if held[tip] {
		parts = append(parts, gitPktString("ACK "+tip+"\n"))
	} else {
		parts = append(parts, gitPktString("NAK\n"))
	}
	parts = append(parts, gitFlush)
	if len(shallowOut) > 0 {
		parts = append(parts, gitPktString("shallow-info\n"))
		for _, oid := range shallowOut {
			parts = append(parts, gitPktString("shallow "+oid+"\n"))
		}
		parts = append(parts, gitFlush)
	}
	parts = append(parts, gitPktString("packfile\n"))
	parts = append(parts, gitSideBandPack(pack))
	return sendText(200, gitConcat(parts...), gitUPResult)
}
