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
