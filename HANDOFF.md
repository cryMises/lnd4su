# LND4SU — installation handoff

You are finishing a deployment that is **built and gated but not yet installed**.
Everything in this file was verified by measurement or by pulled CI logs; anything
unverified is labelled as such. Do not upgrade a label.

Read this whole file before running anything. Then work top to bottom.

---

## 0. FIRST: rotate the exposed yubal door password

A **complete bcrypt hash** (not a prefix) for basic-auth user `yubal` was pasted
into a chat transcript during this build. That door's password must be treated as
compromised. Do this before any other work.

Context: the hash sits on a `basic_auth { yubal $2a$14$… }` line in
`/opt/navidrome/Caddyfile`, in the block whose `reverse_proxy` points at
`yubal:8000` (believed `:8452` — confirm by reading the file, do not assume the
port). It is a Caddy credential, **not** a DokuWiki account hash, so the judge's
auth path is unaffected. Rotate it anyway.

1. Identify the block and the exact line number:

```
grep -n -B2 -A2 'reverse_proxy yubal' /opt/navidrome/Caddyfile
```

2. Back up first:

```
cp /opt/navidrome/Caddyfile /opt/navidrome/Caddyfile.bak-rotation
```

3. Generate a new hash **interactively** so the plaintext never enters shell
history, `ps`, or a log. `caddy hash-password` prompts on stdin when you omit
`--plaintext`:

```
docker exec -it caddy caddy hash-password
```

Type the new password twice. Copy the resulting `$2a$14$…` line.

4. Replace the hash on that one line. The username stays `yubal`. Keep the
   `$2a$14$` prefix exactly as generated — do not rewrite it to `$2y$`.

5. This is a **Caddyfile-only** change, so restart is correct here (not recreate):

```
cd /opt/navidrome && docker compose restart caddy
```

6. Verify, and paste the output:

```
curl -s -o /dev/null -w '%{http_code}\n' -u yubal:NEWPASSWORD https://crymises-music.centralindia.cloudapp.azure.com:PORT_OF_YUBAL/
curl -s -o /dev/null -w '%{http_code}\n' https://crymises-music.centralindia.cloudapp.azure.com:PORT_OF_YUBAL/
```

Expect `200` with credentials, `401` without. The door also needs to be
reachable through the NSG before `200` is possible from outside — if you get a
timeout instead of `401`, test from the box itself before concluding the
rotation failed.

7. Ask the operator whether that password was reused for the wiki login, SSH, or
   anywhere else. If it was, those need rotation too — an exposed bcrypt hash is
   offline-crackable, and a reused password is compromised regardless of the hash.

**Do not paste any hash, prefix, or password into chat, files, logs, or a ticket
from here on.** For wiki hashes, prefix only (`substr($2,1,4)`).

---

## 1. What LND4SU is

*Latihan Ngoding Darurat 4 Soal Ujian* — a small self-hosted online judge for a
DokuWiki ("DoItTill") study wiki, sized for a 1 GB ARM VM that already runs nine
other services.

- Judges **C and C++ only** (one gcc toolchain).
- Every submission runs inside `ioi/isolate` in a privileged container.
- Login uses the **wiki's own accounts**, read read-only from `users.auth.php`.
- 10 test cases per problem; all 10 always run, all 10 revealed after judging.
- Phone-first, Indonesian-language UI.
- No contests, no scoreboard, no practice mode, no other languages. **Do not add
  any of that.** It was deliberately cut.

Source of truth for intent is `lnd4su.md` (one directory above the repo). If this
file and that file disagree, `lnd4su.md` wins — stop and ask.

---

## 2. Hard rules (violating any of these fails the job)

1. **Sandbox always.** Untrusted code never runs outside `isolate` — not the
   program, not the compile. No network inside the sandbox. If the sandbox
   cannot be proven, stop.
2. **No anonymous execution.** Submit and Run require a logged-in wiki account.
   Browsing is public. Enforced in the app, not at the door.
3. **Secrets never in chat, files, git, or logs.** There is no session secret to
   generate — sessions are random 32-byte tokens in SQLite.
4. **Read-only wiki mount.** The judge never writes to `users.auth.php`, never
   serves it, never logs hashes. The mount carries `:ro`.
5. **Backup before every edit.** Compose file, Caddyfile, wiki pages dir.
6. **House compose rules** (this box, do not re-derive): `services:` is line 1 of
   the override file; insert the new service immediately under it; **no
   `networks:` line**; caddy's `ports:` list is **not** in numeric order (live
   order was `8452, 8453, 8451, 8444, 8448, 8449, 8450, 8443`) — anchor on
   content, never on a line number.
7. **Problems come from the operator, not from you.** The six real problems in
   `deploy/problems/` are authored and verified; import them, do not edit their
   test data, and do not invent extra problems. No placeholder statements
   presented as real content.
8. **Never claim live/passing without evidence** — pasted output from the
   operator, or a fetch you performed yourself. "Probably works" is not a report.

---

## 3. Verified state (do not re-litigate)

### Repo and images

- Repo: `github.com/cryMises/lnd4su`, **private**, branch `main`, HEAD `6048e78`.
- App image: `ghcr.io/crymises/lnd4su:latest` (also `:sha-6048e78`).
  Digest `sha256:3d6fe493c50fcf4b0aaf10af387347facf5caab1474c317aca352654ce98c2e3`.
  ARM64, 12.6 MB static Go binary; image 111.8 MB compressed.
- GHCR packages are **public** — the box needs no registry auth. Verified by
  fetching a pull token and a manifest (HTTP 200) anonymously.
- Probe images: `ghcr.io/crymises/lnd4su-step0:latest`
  (`sha256:003cd85c…`) and `ghcr.io/crymises/lnd4su-step0b:latest`
  (`sha256:d27e9fa8…`).

### Gate results (all user-pasted, all pass)

**Phase -1 — auth hash scheme.** PASS. `users.auth.php` lives at
`/opt/navidrome/dokuwiki-config/dokuwiki/conf/users.auth.php` (note the doubled
`dokuwiki` segment) and both existing accounts (`admin`, `lb95`) carry the `$2y$`
bcrypt prefix. The Go code swaps `$2y$`→`$2a$` in memory before verifying; the
stored hash is never modified.

**Phase 0 — sandbox proven.** PASS, `SUMMARY pass=6/6` in the exact
privileged-container shape that ships:

```
AC  PASS   WA  PASS   TLE PASS   MLE PASS (cg-mem=65536KB, cg-oom-killed=1)
RE  PASS   CE  PASS
```

Box facts: cgroup v2 (`cgroup2fs`), full CapEff under `--privileged`, isolate 2.7,
controllers include `memory`, sandbox runs as uid 60000 with `/box` as cwd.

**Phase 0b — worker count.** **SHIP 1 WORKER.** Two concurrent pipelines peaked at
**177 MB** of judging cgroup memory, dropped host `MemAvailable` by **113 MB**,
and grew swap **+32 MB** (2014 → 2046 MB of a 2046 MB swap) on a box already at
~63 % RAM. Spec keeps 2 workers only with no swap growth, so this is a successful
gate outcome, not a failure. `LND4SU_WORKERS: "1"` is already in the compose
snippet with these numbers recorded beside it. **Do not retry to force 2** — the
compile mutex is unconditional either way, and submissions simply queue.

### Defects already found and fixed during gating

Do not re-discover these; they are why the gates took several rounds.

| defect | effect if unfixed |
| --- | --- |
| cgroup controller enabled on the container's own scope | cgroup v2 forbids it while PID 1 lives there → no `memory.max` → every case `XX`. Fixed by hosting boxes in an empty pool `/sys/fs/cgroup/lnd4su`. |
| gcc started with an empty environment | no `PATH` → cannot find `cc1`/`as`/`ld` → **every submission CE**. Fixed with `--env=PATH=/usr/local/bin:/usr/bin:/bin`. |
| `<box_root>/<id>/box` is `0700 root`, sandbox runs as uid 60000 | compiler cannot write its output → every compile fails. Fixed by chmod `0777` after `--init`. |
| `--fsize=2048` applied to compiles | any C++ binary over 2 MB fails to link. Fixed with `--fsize=16384` for compiles. |
| isolate writes **no** `status` key on clean exit | treating empty status as failure fails every successful compile. Empty status means OK. |
| MLE unreachable when `--mem == --cg-mem` | `malloc` returns NULL → RE instead of an OOM kill. Fixed: `--mem` = 2× the limit as a backstop, `--cg-mem` = the real cap. |

### Known behaviour to watch (not a defect)

`cg-mem` counts cgroup **page cache**, not just RSS — a hello-world reported
`cg-mem:45728` against `max-rss:1176`. The app gives every test case a **fresh**
box, so a case does not inherit the compile's cache. But it means a 64 MB limit is
consumed partly by file cache. If acceptance shows MLE on a program that should
pass, that is the first thing to look at, and the honest fix is raising the
problem's memory limit — not changing the sandbox semantics.

---

## 4. What is already prepared in the repo

```
deploy/compose-snippet.yml     the service block, LND4SU_WORKERS=1, with measured notes
deploy/caddy-snippet.txt       the door block + the rule about the ports list
deploy/nsg-command.txt         the az command, priority 1011, with the "confirm first" note
deploy/problems/                 the SIX REAL problems, ready to import (see §5b)
step0/, step0b/                the probes that produced the gate results (keep, do not re-run)
```

Compose service block, for reference:

```yaml
  lnd4su:
    image: ghcr.io/crymises/lnd4su:latest
    container_name: lnd4su
    restart: unless-stopped
    privileged: true
    cgroup: host
    environment:
      LND4SU_PORT: "8080"
      LND4SU_DATA: /data
      LND4SU_WORKERS: "1"
    volumes:
      - ./judge-data:/data
      - ./dokuwiki-config/dokuwiki/conf:/wiki-conf:ro
      - /sys/fs/cgroup:/sys/fs/cgroup:rw
```

`privileged` + `cgroup: host` + the rw cgroup mount are **all three required** —
that is the shape Step 0 proved. No `networks:` line, no host port mapping.

---

## 5. Phase 3 — install on the box

**Status: NOT APPLIED.** The operator pasted the live file windows (below) but has
not run the edits. Everything in this section is unexecuted.

Observed live state, from operator output:

- `/opt/navidrome/docker-compose.override.yml`: line 1 is `services:`; services
  `dokuwiki`, `yubal`, `convertx`, `romm`, `romm-db`, `vtt`, `filebrowser`,
  `caddy`, `beszel` …; no `networks:` anywhere; caddy `ports:` is lines 54-61.
- `/opt/navidrome/Caddyfile`: 37 lines; `:8453` → `dokuwiki:80` is the last block
  (line 35); `:8454` appears **nowhere** — the door is free.
- Wiki conf dir `/opt/navidrome/dokuwiki-config/dokuwiki/conf` matches the
  `./dokuwiki-config:/config` mount, so `./dokuwiki-config/dokuwiki/conf` is the
  correct volume source.

First check ownership so you do not create root-owned files that break later runs:

```
ls -l /opt/navidrome/docker-compose.override.yml /opt/navidrome/Caddyfile
```

Use `sudo` **only** if the plain command is denied, and if you do, restore the
original owner afterwards.

### 5.1 Backups

```
cp /opt/navidrome/docker-compose.override.yml /opt/navidrome/docker-compose.override.yml.bak-lnd4su
```

```
cp /opt/navidrome/Caddyfile /opt/navidrome/Caddyfile.bak-lnd4su
```

### 5.2 Insert the service

```
printf '%s\n' '  lnd4su:' '    image: ghcr.io/crymises/lnd4su:latest' '    container_name: lnd4su' '    restart: unless-stopped' '    privileged: true' '    cgroup: host' '    environment:' '      LND4SU_PORT: "8080"' '      LND4SU_DATA: /data' '      LND4SU_WORKERS: "1"' '    volumes:' '      - ./judge-data:/data' '      - ./dokuwiki-config/dokuwiki/conf:/wiki-conf:ro' '      - /sys/fs/cgroup:/sys/fs/cgroup:rw' > /tmp/lnd4su-svc.yml
```

```
sed -i '1r /tmp/lnd4su-svc.yml' /opt/navidrome/docker-compose.override.yml
```

### 5.3 Data dir

```
mkdir -p /opt/navidrome/judge-data
```

### 5.4 Caddy door

```
printf '\n%s\n' 'crymises-music.centralindia.cloudapp.azure.com:8454 {' '    reverse_proxy lnd4su:8080' '}' | tee -a /opt/navidrome/Caddyfile > /dev/null
```

### 5.5 Caddy port

Anchor on the **last** entry actually present (`8443`), not on a line number:

```
sed -i 's|^      - "8443:8443"$|      - "8443:8443"\n      - "8454:8454"|' /opt/navidrome/docker-compose.override.yml
```

### 5.6 Validate before starting anything

```
cd /opt/navidrome && docker compose config --quiet && echo YAML-OK
```

```
sed -n '1,18p' /opt/navidrome/docker-compose.override.yml; echo ---; grep -n -A20 '^  caddy:' /opt/navidrome/docker-compose.override.yml
```

```
tail -8 /opt/navidrome/Caddyfile; echo ---; grep -c '8454' /opt/navidrome/Caddyfile /opt/navidrome/docker-compose.override.yml
```

Expect: `lnd4su:` immediately after `services:`; caddy's list ends
`… "8450:8450"`, `"8443:8443"`, `"8454:8454"`; both files report exactly one
`8454`. If `YAML-OK` does not print, restore both backups with `mv -f` and stop.

### 5.7 Start the judge, then caddy

Order matters — start `lnd4su` first so caddy can resolve it on boot.

```
cd /opt/navidrome && docker compose up -d lnd4su && docker compose ps lnd4su && docker logs --tail 40 lnd4su
```

**Then caddy.** This is the one place the house rule needs care: the Caddyfile
change alone would take `restart caddy`, but this change **also adds a port to
caddy's container config**, which a restart does not pick up. Use `up -d`, which
recreates:

```
cd /opt/navidrome && docker compose up -d caddy && docker compose ps caddy
```

Caddy recreating blips the other eight doors for a couple of seconds. That is
expected, not an outage — but say so before running it.

### 5.8 NSG (Azure Cloud Shell, never on the VM)

```
az network nsg rule list -g n8n-rg --nsg-name music-vmNSG -o table --query "[].{Name:name,Priority:priority,Port:destinationPortRange}"
```

Read the output and pick the next free priority. Priorities 1000-1010 were in use
on 2026-11-18, so 1011 is expected — **confirm from the output, do not assume**.
Then run the command in `deploy/nsg-command.txt` with the confirmed priority.

### 5.9 Verify with evidence

```
docker stats --no-stream lnd4su
```

```
curl -s -o /dev/null -w '%{http_code}\n' https://crymises-music.centralindia.cloudapp.azure.com:8454/healthz
```

```
curl -s https://crymises-music.centralindia.cloudapp.azure.com:8454/healthz
```

Pass: `/healthz` returns `ok`, the problem list renders with the empty state
(**Belum ada soal**) and no login required, and `docker stats` shows idle
**under 50 MB**. Record the real number. If it is over 50 MB, say so and shrink
before calling the deploy done.

---

## 6. Phase 4 — wiki links

All six problems carry `wiki_page_id = "algo:start"`, so each problem page's
"back to material" link already points at `algo:start`. Per question, one line
appended to the end of the `algo:start` page:

```
**Latihan ngoding:** [[https://crymises-music.centralindia.cloudapp.azure.com:8454/p/<slug>|<Problem title>]]
```

The six slugs:

| slug | title |
|---|---|
| `grade-report` | Grade Report |
| `palindrome-word-count` | Palindrome and Word Count |
| `recursive-digit-tools` | Recursive Digit Tools |
| `reverse-rotate-array` | Reverse and Rotate Array |
| `dynamic-sort-menu` | Dynamic Sorting with Menu |
| `student-records` | Student Records |

If the operator later moves a question to a different page, change that
problem's `wiki_page_id` in the admin form to match — the two must agree or the
"back to material" link lands in the wrong place.

Backup first:

```
cp -r /opt/navidrome/dokuwiki-config/dokuwiki/data/pages /opt/navidrome/dokuwiki-config/dokuwiki/data/pages.bak-lnd4su
```

Wiki-side edits need wiki login or SSH. If you cannot reach the wiki, **report the
edits as a manual step** with the exact page and exact line per question. Do not
claim a link exists without evidence — a fetched page showing it, or the
operator's pasted confirmation.

No ACL changes, no plugins, no template edits, no content invention. Link lines
only.

---

## 6b. The six problems (already verified — do not edit their test data)

`deploy/problems/*.zip`, each containing `problem.json` + `statement.pdf`.
All: exactly 10 test cases, subtasks `A` (1-5) and `B` (6-10), tolerant compare,
1000 ms / 65536 KB, `wiki_page_id = algo:start`.

| bundle | difficulty | tags |
|---|---|---|
| `grade-report.zip` | Mudah | selection, repetition, functions |
| `palindrome-word-count.zip` | Mudah | strings, chars, loops |
| `recursive-digit-tools.zip` | Sedang | recursion, macros, preprocessor |
| `reverse-rotate-array.zip` | Sedang | pointers, arrays, functions |
| `dynamic-sort-menu.zip` | Sedang | malloc, realloc, sorting, pointers |
| `student-records.zip` | Sedang | struct, arrays, files |

Each was verified by writing an independent reference C solution and running it
against all 10 cases through the judge's exact tolerant comparison: **all six
scored 10/10, zero mismatches**. So a correct solution exists for every problem
and the expected outputs are self-consistent.

Two facts worth knowing when reading submissions:

- `grade-report` — `-1` terminates input and is not a score; a score outside
  0..100 prints `Invalid score` and is not counted.
- `student-records` — `Highest:` is the **first** maximum-GPA student in input
  order (ties keep the earlier record); `Honors:` lists GPA >= 3.00 in input
  order.

---

## 7. Phase 5 — acceptance, in order, report per item

Import the six real problems from `deploy/problems/` as admin (each `.zip` is a
complete bundle: `problem.json` + `statement.pdf`, 10 cases, subtasks A and B).
Use `grade-report` — the simplest of the six — as the subject for the judging
checks below. The problems stay; nothing is deleted at the end.

1. **Sandbox matrix** — already passed in Phase 0. Re-confirm from the report
   above; re-run `step0` only if the image changed.
2. **Idle RAM under 50 MB** and `LND4SU_WORKERS=1` honoured: two quick
   submissions must **queue strictly**, never overlap. Prove with logs.
3. **Phone-viewport full flow** — submit a C solution → submit a C++ solution →
   verdicts with all 10 cases shown, a failed case showing input/expected/actual
   → `/me` history and stats.
4. **Wiki flow** — a question opens from its wiki topic page; the problem page
   links back; Run mode works against custom stdin; partial scoring works
   (subtasks A and B, fail one group → score shows the passing group only).
5. **Rejudge flips a verdict** (edit a case's expected output, rejudge, restore
   it) and **export/import round-trips** (export `grade-report`, delete,
   re-import, 10 cases intact).
6. **Anonymous cannot execute** — forms are replaced by a login prompt, and a
   direct POST returns 401, not a crash. **Sandbox has no network** — submit a
   program that fetches a URL; it must fail (RE or WA), never succeed.
7. **Recreate loses nothing** — `docker compose up -d --force-recreate lnd4su`,
   then problems, submissions and sessions still present.
8. **Neighbours healthy** — `docker ps` shows all services Up; spot-check the
   other doors; VM1 untouched at every point.

Then hand over with: what works, measured idle RAM, the Step 0 matrix, the
1-worker verdict and its numbers, the door URL, per-item acceptance pass/fail with
evidence, anything that did not pass, and the backup command:

```
cp /opt/navidrome/judge-data/lnd4su.db /opt/navidrome/judge-data/lnd4su.db.bak-$(date +%F)
```

---

## 8. Admin notes for the operator

- Accounts live in the **wiki**, not the judge. A new wiki account works on the
  judge immediately; a wiki password change applies immediately. Admins are
  members of the wiki `admin` group.
- Problems are created at `/admin/problems`. Each needs a PDF statement (uploaded,
  `%PDF` checked, 5 MB cap) and **exactly 10** test cases. A problem without a PDF
  shows a placeholder and cannot be submitted to.
- Import/export is a ZIP bundle (`problem.json` + `statement.pdf`); a bare `.json`
  import also works and lands the problem in the no-PDF placeholder state.
- Backup = copy one file (the command above). Everything — problems, cases,
  submissions, sessions, blocklist — is in that SQLite file; PDFs live beside it in
  `judge-data/statements/`.
- Blocking a user affects judging only; their wiki account is untouched.

---

## 9. Deviations from `lnd4su.md` you should know

1. **No `LND4SU_SESSION_SECRET`.** Sessions are random 32-byte tokens in SQLite.
   The spec's Phase 3 `openssl rand -hex 32` line is unnecessary — do not run it.
2. **Compose adds `cgroup: host` and a rw `/sys/fs/cgroup` mount** beside
   `privileged: true`. This is the exact shape Step 0 proved; `privileged` alone
   was not enough.
3. **`compile_log` column** added to `submissions` (compile errors are stored, not
   just counted).
4. **`internal/admin` merged into `internal/handlers`.** The spec called the layout
   "suggested".
5. **`up -d caddy` instead of `restart caddy`** for this deployment, because the
   change includes caddy's ports list. `restart` is still correct for a
   Caddyfile-only edit (see §0).

---

## 10. Stop and ask

- Any step needs a secret, password, token, or hash value.
- The compose or Caddyfile does not look like the state recorded in §5.
- `docker compose config` fails and `mv -f` from the backups does not resolve it.
- Sandbox behaviour changes in any way, or a verdict cannot be produced.
- A gate measurement is borderline — report the numbers and let the operator
  decide.
- Anything would touch VM1, the wiki ACL/plugins/templates, or any service beyond
  `lnd4su`, its caddy door, and its NSG rule.
- The spec is ambiguous on a point that changes the build.
