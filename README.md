# LND4SU

Latihan Ngoding Darurat 4 Soal Ujian — a small self-hosted online judge for C and C++, built to fit a 1 GB ARM64 box. One Go binary, one SQLite file, an `isolate` sandbox. Login uses the DoItTill DokuWiki accounts (read-only mount); no registration, no email.

Live: <https://crymises-music.centralindia.cloudapp.azure.com:8454> — linked from the [DoItTill wiki](https://crymises-music.centralindia.cloudapp.azure.com:8453) (Algorithm and Programming first).

## What it does

- Problems with a PDF statement, visible samples, and exactly 10 hidden test cases. All 10 cases always run; the verdict page reveals every case (input, expected output, actual output, time, memory). The score is cases passed out of 10, so partial credit works.
- C and C++ only. Verdicts: AC / WA / TLE / MLE / RE / CE. Limits are 1 s / 64 MB per problem (configurable per problem).
- Run mode: run code against your own stdin without submitting.
- Wiki accounts: `users.auth.php` is read fresh on every login, so a wiki password change applies immediately. Admin = member of the wiki `admin` group. Browsing is public; submitting and Run mode require a login.
- Admin: problem CRUD, import/export, rejudge one submission or a whole problem, blocklist.
- Import accepts a ZIP bundle (`problem.json` + `statement.pdf`), a bare `.json` (the problem lands in the no-PDF placeholder state), or a collection ZIP of several bundles at once.
- Phone-first scrapbook UI in Indonesian, installable as a PWA (manifest + service worker + icons).

## Problem bundle format

One ZIP per problem: `problem.json` + `statement.pdf`, nothing else.

- `problem.json`: slug, title, difficulty, tags, limits (ms / KB), compare mode (`tolerant` normalizes trailing whitespace and CRLF, `exact` is byte-for-byte), optional `wiki_page_id`, and exactly 10 test cases. Cases 1–5 are subtask A, 6–10 subtask B. Case 1 is the sample and must match the statement's example.
- `statement.pdf`: the problem statement, shown embedded on the problem page. A problem without a PDF shows a placeholder and cannot be submitted to.
- A collection ZIP is a ZIP of problem-bundle ZIPs, for importing several problems at once. See `deploy/ab-test/ab.zip` for a minimal A+B example bundle.

## Configuration

| var | default | meaning |
| --- | --- | --- |
| `LND4SU_PORT` | `8080` | HTTP listen port (Caddy proxies to it) |
| `LND4SU_DATA` | `/data` | data dir: `lnd4su.db` + `statements/` |
| `LND4SU_WORKERS` | `1` | judging workers — keep at 1 on a 1 GB box (compile is always serialized) |
| `LND4SU_ISOLATE` | `/usr/local/bin/isolate` | isolate binary |
| `LND4SU_BOXROOT` | `/var/local/isolate` | isolate box root |
| `LND4SU_USERS` | `/wiki-conf/users.auth.php` | DokuWiki users file (read-only) |
| `LND4SU_WIKI_URL` | DoItTill URL | footer link + "back to material" base |

Session cookies are random 32-byte tokens stored in SQLite; there is no cookie signing secret to configure.

## Local development

```sh
go build ./...
go test ./...
go run ./cmd/server        # needs isolate; without it judging logs an error
```

## Deploy (music-vm)

1. `deploy/compose-snippet.yml` — paste the service under the `services:` header of `/opt/navidrome/docker-compose.override.yml` (no `networks:` line, no host port).
2. `deploy/caddy-snippet.txt` — add the door block to the Caddyfile and the port to Caddy's `ports:` list, then recreate Caddy (`docker compose up -d caddy`).
3. `deploy/nsg-command.txt` — open the port in Azure (Cloud Shell, not the VM).
4. `cd /opt/navidrome && docker compose up -d lnd4su`
5. Wiki config is mounted read-only at `/wiki-conf`; the judge never writes it.

The full install story, including the sandbox gating (Step 0 / 0b) and the measured numbers, is in `HANDOFF.md`.

## Backup

```sh
cp /opt/navidrome/judge-data/lnd4su.db /opt/navidrome/judge-data/lnd4su.db.bak-$(date +%F)
```

WAL files (`lnd4su.db-wal`, `-shm`) are normally checkpointed on shutdown; for a hot copy either stop the container first or copy all three files together. Statement PDFs live beside the DB in `judge-data/statements/`.

## Admin notes

- Accounts live in the wiki. Add a wiki user → they can log in here immediately. Put them in the wiki `admin` group to give them problem-editing rights.
- Problems are created at `/admin/problems/new`. A problem without a PDF cannot be submitted to; it shows a placeholder.
- Import accepts a ZIP bundle, a bare `.json`, or a collection ZIP. Exactly 10 test cases are required; import rejects anything else.
- Rejudge re-runs the stored source against the current test cases.
- Blocking a user only affects the judge; the wiki account is untouched.
- Export a problem from the admin list to get a round-trip bundle.
- After 5 failed logins an account is locked out for 5 minutes (in-memory; a container restart clears it). During the lockout even the correct password is rejected.

## Layout

```text
cmd/server        entrypoint and env wiring
internal/store    SQLite (problems, cases, submissions, sessions, blocklist)
internal/auth     users.auth.php parsing, bcrypt, session tokens, lockout
internal/judge    isolate wrapper, compile/run, comparison, workers
internal/handlers HTTP routes, templates (embedded), static CSS/JS, PWA manifest + service worker + icons (embedded)
step0, step0b     sandbox probes: privileged-container verdict matrix and the 1-worker gate
deploy            compose / Caddy / NSG snippets, A+B acceptance bundle
HANDOFF.md        install guide and gating record for the deploying agent
```

## Why it exists

Ready-made judges don't fit the box this runs on: DOMjudge needs amd64 images and a dedicated judgehost, HUSTOJ needs an empty 1 GB box of its own, and this VM is a 1 GB ARM64 machine already running nine other services. LND4SU is the minimal judge that fits — one worker, ~4 MiB idle — written for the DoItTill wiki's Algorithm and Programming course.

## License

MIT — see [LICENSE](LICENSE).
