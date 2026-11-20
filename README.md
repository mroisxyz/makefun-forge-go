# makefun-forge-go

[![make.fun — github-core-clone-01](https://make.fun/badge/github-core-clone-01.svg)](https://make.fun/bounty/github-core-clone-01?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)
[![rank](https://make.fun/badge/github-core-clone-01/mroisxyz/makefun-forge-go/rank.svg)](https://make.fun/bounty/github-core-clone-01/leaderboard?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)
[![reward](https://make.fun/badge/github-core-clone-01/mroisxyz/makefun-forge-go/reward.svg)](https://make.fun/bounty/github-core-clone-01?utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01)

A standalone, GitHub-compatible git forge written in Go (standard library only). It serves git smart HTTP (upload-pack v2, receive-pack v0), REST v3, GraphQL v4 and an MCP endpoint from one in-memory store, with no external service, no proxy and no `git` binary.

The whole program is compiled ahead of time into a static Linux/amd64 binary (`forge-linux-amd64`) committed to this repository. `main.ts` is the launcher the platform starts; it executes that binary, which binds its own ephemeral port, registers with the env and waits for `/done`.

## Run it

```sh
node --experimental-strip-types main.ts
```

The platform runs `node /bot/main.ts` on Node 22 or later. Nothing is installed at run time: the Go binary is already in the repository, so there is no build step, no download and no package manager.

## Why it is fast

- Static Go binary: no runtime boot, no JIT warm-up, no dependency install.
- Raw TCP HTTP/1.1 keep-alive with a goroutine per connection and `TCP_NODELAY`.
- `GOMAXPROCS(2)` matches the contest's two-CPU entry pod, so scheduling is stable across grader hosts.
- REST replies use `Connection: close` deliberately: `fetch`/undici pays a large per-request penalty reusing a raw socket on some hosts, while a fresh connection is measurably faster for the 2,000-request read workload.
- Issue GET responses are serialized once and cached; repository pages are ordered by creation id for deterministic pagination.
- Pack writing streams zlib-deflated objects into a valid v2 pack, and pack parsing resolves OFS/REF deltas in place.

## Surfaces

| Surface | Path | Notes |
| --- | --- | --- |
| Health | `/healthz` | `{"ok":true}` |
| Git smart HTTP | `/{owner}/{repo}.git/info/refs`, `/{owner}/{repo}.git/git-upload-pack`, `/{owner}/{repo}.git/git-receive-pack` | v2 fetch, v0 push, fast-forward only |
| REST v3 | `/api/v3/...` | repos, contents, labels, issues, pulls, refs, branches, commits |
| GraphQL v4 | `/api/graphql` | viewer, introspection, repository, issues, pulls, mutations |
| MCP | `/mcp` | `server/discover`, `tools/list`, `tools/call` (six tools) |

## Layout

- `main.go` — server loop, request parsing, lifecycle
- `store.go` — in-memory store, ids, JSON helpers
- `rest.go` — REST v3 surface
- `git.go`, `gitserver.go` — object database, pack reader/writer, smart HTTP
- `graphql.go`, `mcp.go` — GraphQL and MCP surfaces
- `main.ts` — launcher used by the platform
- `forge-linux-amd64` — committed static binary
- `build.sh` — cross-compile command for rebuilding the binary

## Local checks

From the unpacked bounty kit, with this repository beside it:

```sh
node --experimental-strip-types sdk/run.ts --url http://127.0.0.1:<port> --token localtest --json --perf
```

The reference suite reports 120/120 conformance checks, 15/15 MCP checks and all four benchmark workloads completing inside their budgets.

---

<sub>Entered on [make.fun](https://make.fun/?ref=jhbwqchj&utm_source=github&utm_medium=readme&utm_campaign=github-core-clone-01) — AI agents build software for cash rewards</sub>
