# N100 / 16 GB / 512 GB, Linux Mint 22.3 Cinnamon, 4K display

Box: `hackbox1`, Intel N100 (4 cores), 16 GB, Alder Lake-N UHD, 3840x2160.
Idle with the Cinnamon session logged in, after `setup/`: 1.43 GB used, 0% CPU, load 0.1.

Session unit: `bench/session.sh`, three chained Haiku 4.5 phases (scaffold
Vite+React+TS, add search/favourites with Vitest, fix two bugs). 36 to 50
turns, ~3 minutes, ~$0.25 at API rates.

`single-1` predates the three-phase session and had no browser. `typical`,
`messy` and `abuse` are `bench/load.sh` with a working Chromium
(`--password-store=basic --no-first-run`, otherwise a dialog blocks it).
5 s samples, so CPU peaks inside a sample are higher than shown.

| profile | claude / dev / tabs | mem used max | mem avail min | cpu max | load1 max | claude RSS max | chromium RSS max | swap |
|---|---|---|---|---|---|---|---|---|
| idle | 0 / 0 / 0 | 1.43 GB | 14.3 GB | 0% | 0.1 | 0 | 0 | 0 |
| typical | 1 / 1 / 3 | 3.85 GB | 11.9 GB | 44% | 0.96 | 273 MB | 1.7 GB | 0 |
| messy | 2 / 2 / 6 | 4.41 GB | 11.4 GB | 45% | 0.97 | 541 MB | 2.3 GB | 0 |
| abuse | 3 / 3 / 10 | 5.11 GB | 10.7 GB | 60% | 1.95 | 812 MB | 2.8 GB | 0 |

Reading: the browser is the biggest consumer, not Claude Code. Three
concurrent sessions plus three dev servers plus ten tabs leaves 10.7 GB
free and never touches swap. Load never reached 2 on 4 cores.

Caveats: sessions are 3 minutes, not 30, so Claude Code's context (and RSS)
stays small; expect 1 GB or more per process by the end of a real session.
Tabs were docs, GitHub and the dev app, not video. 4K roughly doubles what
every tab and the compositor cost versus 1080p.
