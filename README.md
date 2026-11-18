# LND4SU

Latihan Ngoding Darurat 4 Soal Ujian — small self-hosted online judge for C and
C++, built to fit a 1 GB ARM64 box. One Go binary, one SQLite file, `isolate`
sandbox. Login uses the DoItTill DokuWiki accounts (read-only mount); no
registration, no email.

## What it does

- Problems with a PDF statement, visible samples, 10 hidden test cases,
  per-problem time/memory limits, optional subtask groups.
- C and C++ only. Verdicts: AC / WA / TLE / MLE / RE / CE. All 10 cases always
  run; the verdict detail reveals every case (input, expected, actual output,
  time, memory).
- Run mode against your own stdin, without submitting.
- Wiki accounts: `users.auth.php` is parsed on each login, admin = member of the
  wiki `admin` group. Browsing is public; submitting and Run mode require a
  login.
- Admin: problem CRUD, import/export (ZIP bundle or bare JSON), rejudge one
  submission or a whole problem, blocklist.

## Env vars

| var | default | meaning |
| --- | --- | --- |
| `LND4SU_PORT` | `8080` | HTTP listen port (Caddy proxies to it) |
| `LND4SU_DATA` | `/data` | data dir: `lnd4su.db` + `statements/` |
| `LND4SU_WORKERS` | `1` | judging workers, 1 or 2 (compile is always serialized) |
| `LND4SU_ISOLATE` | `/usr/local/bin/isolate` | isolate binary |
| `LND4SU_BOXROOT` | `/var/local/isolate` | isolate box root |
| `LND4SU_USERS` | `/wiki-conf/users.auth.php` | DokuWiki users file (read-only) |
| `LND4SU_WIKI_URL` | DoItTill URL | footer link + "back to material" base |

Session cookies are random 32-byte tokens stored in SQLite; there is no cookie
signing secret to configure.

## Local development

```
go build ./...
go test ./...
go run ./cmd/server        # needs isolate; without it judging logs an error
```

## Deploy (music-vm)

1. `deploy/compose-snippet.yml` — paste the service under the `services:` header
   of `/opt/navidrome/docker-compose.override.yml` (no `networks:` line, no host
   port).
2. `deploy/caddy-snippet.txt` — add the door block to the Caddyfile and the port
   to Caddy's `ports:` list, then `docker compose restart caddy`.
3. `deploy/nsg-command.txt` — open the port in Azure (Cloud Shell, not the VM).
4. `cd /opt/navidrome && docker compose up -d lnd4su`
5. Wiki config is mounted read-only at `/wiki-conf`; the judge never writes it.

## Backup

```
cp /opt/navidrome/judge-data/lnd4su.db /opt/navidrome/judge-data/lnd4su.db.bak-$(date +%F)
```

WAL files (`lnd4su.db-wal`, `-shm`) are normally checkpointed on shutdown; for a
hot copy either stop the container first or copy all three files together.

## Admin notes

- Accounts live in the wiki. Add a wiki user → they can log in here immediately.
  Put them in the wiki `admin` group to give them problem-editing rights.
- Problems are created at `/admin/problems/new`. A problem without a PDF cannot
  be submitted to; it shows a placeholder.
- Import accepts a ZIP bundle (`problem.json` + `statement.pdf`) or a bare
  `.json` (problem lands in the no-PDF placeholder state). Exactly 10 test cases
  are required; import rejects anything else.
- Rejudge re-runs the stored source against the current test cases.
- Blocking a user only affects the judge; the wiki account is untouched.
- Export a problem from the admin list to get a round-trip bundle.

## Layout

```
cmd/server        entrypoint and env wiring
internal/store    SQLite (problems, cases, submissions, sessions, blocklist)
internal/auth     users.auth.php parsing, bcrypt, session tokens, lockout
internal/judge    isolate wrapper, compile/run, comparison, workers
internal/handlers HTTP routes, templates (embedded), static CSS (embedded)
step0             sandbox probe: privileged-container verdict matrix
deploy            compose / Caddy / NSG snippets
```
