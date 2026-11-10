# Sprint 1 memory benchmark — real deployment VPS

Measured 2026-11-10 against the deployed Sprint 1 daemon. This is a
**real VPS**, not a cgroup-simulated environment (per the agreed
acceptance approach, cgroup-limited soak tests complement but do not
replace measurements on real hardware).

## Environment

| Item        | Value                                             |
| ----------- | ------------------------------------------------- |
| Host        | development VPS, x86_64 KVM (AMD EPYC), 4 vCPU    |
| OS          | Ubuntu 26.04.1 LTS, kernel 7.0.0-38-generic       |
| RAM / swap  | 5.8 GiB total, 0 swap                             |
| init        | systemd 259 (hardened unit active)                |
| Build       | `0.1.0-sprint1`, static `linux/amd64`, stripped   |
| Deployment  | per `deploy/DEPLOY.md`; user `drevanta`, `MemoryMax=256M` |

## Methodology

1. Deploy, enable service, verify endpoints.
2. **Idle pass:** measure after start-up with no further requests.
3. **Load pass:** 300 sequential `GET /api/v1/server/metrics` over
   loopback (curl, single client) — completed in **1.39 s**
   (≈ 216 req/s end-to-end; the first request includes the one-time
   250 ms double-sample, steady-state is faster).
4. Post-load measurement after 30 s idle (service still `active`,
   `/readyz` still `ready`).
5. Sources: `systemctl show -p MemoryCurrent` (cgroup accounting),
   `/proc/PID/status` (`VmRSS`, `VmHWM`), `/proc/PID/smaps_rollup`.

## Results

| Measurement                          | Value                     | Source            |
| ------------------------------------ | ------------------------- | ----------------- |
| MemoryCurrent, idle                  | **3.65 MiB** (3,825,664 B)| systemd cgroup    |
| RSS, idle                            | **11.76 MiB** (12,040 kB) | `VmRSS`           |
| PSS, idle                            | 11.75 MiB (Pss_Dirty 11.75 MiB) | smaps_rollup |
| MemoryCurrent, after 300 requests    | **8.05 MiB** (8,445,952 B)| systemd cgroup    |
| RSS, after load + 30 s idle          | **15.49 MiB** (15,864 kB) | `VmRSS`           |
| Peak RSS (`VmHWM`)                   | **15.49 MiB** (15,864 kB) | `VmHWM`           |
| Anonymous heap (Pss_Anon) after load | 6.63 MiB                  | smaps_rollup      |
| Mapped file pages (Pss_File), idle   | 8.86 MiB                  | smaps_rollup      |
| Binary size (static, stripped)       | 9.15 MiB (9,592,984 B)    | file size         |
| Loopback throughput (sequential)     | ≈ 216 req/s end-to-end    | load loop         |

Reading the numbers: idle RSS is dominated by mapped binary pages
(Go runtime + embedded dashboard + SQLite engine ≈ 8.9 MiB of file-backed
pages). The working anonymous heap stays around 6.6 MiB after load; the
gap between cgroup `MemoryCurrent` and RSS reflects how the kernel
attributes shared/read-only pages to the cgroup.

## Fit against the memory budget

| Budget                       | Headroom                                   |
| ---------------------------- | ------------------------------------------ |
| `MemoryMax=256M` ceiling     | peak RSS ≈ **6.1 %** of the ceiling        |
| 512 MiB-class VPS RAM        | daemon peak ≈ **3.0 %** of total RAM       |

Conclusion: the Sprint 1 daemon consumes an order of magnitude less than
its hard ceiling and about 3 % of a 512 MiB box, leaving realistic
headroom for the OS, the audit growth, and later components.

## Caveats and next steps

- Single sequential loopback client; no concurrent-load or TLS-overhead
  scenarios in Sprint 1 (none exist yet — no TLS by design).
- Dashboard polling (5 s interval) is covered by the same request path.
- Planned follow-up per the testing strategy: continuous soak under a
  cgroup `MemoryMax=512M, MemorySwapMax=0` harness, plus long-run VmHWM
  tracking on the real VPS.

---

# Sprint 2 memory benchmark + cgroup-vs-RSS investigation

Measured 2026-11-10 on the development VPS (same environment as Sprint 1:
Ubuntu 26.04.1, kernel 7.0.0-38-generic, 4 vCPU / 5.8 GiB RAM / 0 swap,
systemd 259, hardened unit `MemoryMax=256M`). Release
`0.2.2-sprint2`, static linux/amd64, stripped.

## Results

| Measurement                                    | Value                    | Source                     |
| ---------------------------------------------- | ------------------------ | -------------------------- |
| MemoryCurrent, fresh start (unclaimed)         | **2.17 MiB** (2,273,280 B) | systemd cgroup          |
| VmRSS, fresh start                             | **10.9 MiB** (11,172 kB) | `/proc/PID/status`         |
| Pss_File (mapped binary pages), fresh start    | 9.59 MiB (9,588 kB)      | `smaps_rollup`             |
| Pss_Anon, fresh start                          | 1.54 MiB (1,576 kB)      | `smaps_rollup`             |
| MemoryCurrent after 300 × /healthz (1.35 s)    | 3.87 MiB (4,059,136 B)   | systemd cgroup             |
| VmHWM after load                               | 12.2 MiB (12,480 kB)     | `VmHWM`                    |
| MemoryCurrent peak during argon2id login burst | **41.9 MiB** (43,970,560 B) | systemd cgroup          |
| VmHWM after 6 concurrent wrong-password logins | **50.3 MiB** (51,536 kB) | `VmHWM`                    |
| `/readyz` during/after bursts                  | ready throughout         | curl                       |
| Static binary size                             | 9.4 MiB                  | file size                  |
| systemd-analyze security                       | **3.7 (OK, low exposure)** | systemd-analyze          |

Reading the numbers:

- The idle daemon (fresh state, no sessions) sits at ~2 MiB charged to
  its cgroup and ~11 MiB process RSS — the difference is the mapped
  binary (see the accounting investigation below).
- Each argon2id password verification allocates a ~19 MiB workspace.
  With the concurrency bound (`argonConcurrency = 2` in
  `internal/httpapi`), a burst of 6 concurrent logins peaked at
  ~44 MiB cgroup / ~50 MiB VmHWM and stayed there (Go returns freed
  pages lazily via `MADV_FREE`; under cgroup memory pressure the pages
  are reclaimed). That is ~18 % of the 256 MiB ceiling and ~9 % of a
  512 MiB box — bounded by construction, since the third concurrent
  verification gets `503 busy`.
- `/readyz` remained `ready` through the load pass and both login
  bursts.

## Why cgroup `MemoryCurrent` ≪ process RSS

Sprint 1 recorded `MemoryCurrent = 3.65 MiB` against `VmRSS = 11.76 MiB`
and attributed the gap vaguely to "shared/read-only page attribution".
Sprint 2 measured it precisely (fresh process, release 0.2.1):

| Counter                                   | Value        |
| ----------------------------------------- | ------------ |
| cgroup `memory.current`                   | 2.17 MiB     |
| cgroup `memory.stat` anon                 | 1.54 MiB     |
| cgroup `memory.stat` file                 | **0.05 MiB** (52 kB) |
| cgroup `memory.stat` slab                 | 0.23 MiB     |
| process `VmRSS`                           | 10.9 MiB     |
| process `smaps_rollup` Pss_File           | **9.59 MiB** |
| process `smaps_rollup` Pss_Anon           | 1.54 MiB     |

The process has 9.59 MiB of file-backed pages resident (the mapped
`drevantad` binary), but the service cgroup's `file` counter holds only
52 kB — essentially none of them. Explanation: **a cgroup-v2 page is
charged to exactly one cgroup — the one that first faults it in**. The
binary file was written to disk by the deployment process (root's
session/cgroup), which pulled it into the page cache charged to *that*
cgroup. When `drevantad` later maps and faults those pages, the kernel
finds them already present and charged elsewhere, so mapping costs the
service cgroup ~nothing. Per-process RSS counts every resident page
regardless of which cgroup is charged — hence the ~9 MiB gap, which
matches `Pss_File` almost exactly (9.59 vs 11.76 − 2.17 ≈ 9.6).

Practical consequences:

- `MemoryCurrent` is the right number for capacity planning *of the
  service's own allocations* (it is what `MemoryMax` bounds); it will
  jump by the full binary size (~9 MiB) if the page cache happens to be
  evicted and re-faulted from within the service cgroup (e.g. after
  `drop_caches` or a copy from a cold page cache), and after argon2id
  verifications (see above).
- `VmRSS`/`VmHWM` remain the right numbers for "how much RAM does this
  process touch".

## Fit against the memory budget (Sprint 2)

| Budget                       | Headroom                                            |
| ---------------------------- | --------------------------------------------------- |
| `MemoryMax=256M` ceiling     | bounded burst peak ~44 MiB ≈ **17 %** of the ceiling |
| 512 MiB-class VPS RAM        | bounded burst peak ≈ **8.6 %** of total RAM          |

The argon2id concurrency bound (2 slots) is what makes the worst case
predictable; without it, N concurrent logins would allocate N × 19 MiB.
