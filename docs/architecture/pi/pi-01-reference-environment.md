# PI-01 reference environment and evidence gates

Observation date: 2026-09-15
Owner: PI-01/R; future benchmarks: PI-03, PI-10 and PI-26

## Deployment evidence

Read-only SSH inspection used `uname -m`, `/etc/os-release`, `getconf`, `free`,
`systemd-detect-virt`, selected `systemctl show` properties and `df -T`.
No catalog GET was used for environment discovery because it can write metadata.

| Property | Observed value | Scope of conclusion |
| --- | --- | --- |
| Server OS | Debian GNU/Linux 13 (trixie), reported Debian version 13.6 | Existing host only; exact future base image must be pinned |
| Architecture | x86-64 | Initial reference target; ARM remains unverified |
| Allocation | 1 online CPU, 2048 MiB memory, 512 MiB swap | Resource envelope for initial performance evaluation |
| Virtualization | LXC | Existing Proxmox deployment reported by user; LXC observed |
| Content filesystem | ext4 | Local guest filesystem; physical disk/fsync behavior not tested |
| Service | systemd unit loaded and active | Process availability only; no playback or health certification |
| Installation provenance | TeddyCloud v0.7.0, Community Scripts | User-reported; installed executable version not revalidated |

Private host names, addresses, volume IDs, tag IDs and certificates are omitted.
This reference is not a production capacity guarantee. In particular, conversion
can compete with playback on one CPU; worker resource limits require measurement.

## Hardware and workflow evidence

| Workflow | Evidence level at R | Required follow-up |
| --- | --- | --- |
| Original playback, placement and Copy | User-reported existing success | PI-02 sanitized device/firmware record, PI-29 repeatable device test |
| TB1/TB2, TLS and RTNL coverage | Source paths exist; hardware unverified | PI-02 inventory; PI-38/47 cutover matrix |
| Set member identity, conflicting audio pairs | Source-reviewed rules; fixtures not yet built | F-03, PI-04/15/16 |
| NFC/MyTonies/YouTube workflows | User-reported existing behavior | PI-02 contracts, PI-31/32 worker fixtures |
| Backup, power loss and rollback | Unverified | F-06, PI-05/07/34/37/38 |
| UI performance and plugin isolation | Unverified | PI-03 pinned runner, PI-24/26 measurements |

## Reproducible benchmark target

Use a disposable Debian 13 x86-64 environment limited to one CPU and 2 GiB RAM
with local ext4, and a separate browser runner. Use a versioned synthetic dataset
of 10,000 tags, fixed sort order, protected Custom Cards and conflicting Set
candidates. External catalogs and cloud downloads must not affect timing.

PI-03 pins the browser automation package, bundled Chromium revision, base image
digest, kernel/runtime versions and viewport (1440 x 900, device scale 1).
No browser revision is invented here: that lockfile does not exist yet. F-01
remains open for this executable profile and actual device/firmware observations.

PI-10/26 measures cold and warm runs separately: at least 100 toggles per case,
timing user input to painted toggle feedback and to painted committed query
results. Record network latency and concurrent media work as separate cases.
Report p50/p95 and errors, with traces and the exact fixture/build IDs. The
100 ms/300 ms p95 goals remain provisional until that baseline is reviewed.
