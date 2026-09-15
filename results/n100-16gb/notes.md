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
| browser-heavy | 2 / 2 / 25, 17 min | 7.10 GB | 8.7 GB | 98% | 5.05 | 554 MB | 7.7 GB* | 0 |

`browser-heavy` used `bench/tabs-heavy.txt` (Remix, Uniswap, Etherscan,
YouTube playing throughout, Twitch, GitHub PRs, Reddit, X, The Verge,
claude.ai, ChatGPT, Figma, Notion, the 0G sites, docs) with `HOLD=900` so
the two sessions looped five times each. Memory plateaued at ~5.8 GB from
minute 2 (Chromium's Memory Saver discarding background tabs). Load 5 on 4
cores during the tab-opening storm, then 1 to 3. All 10 sessions finished
in 140 to 206 s, no slower than with no browser.

\* summed RSS across 40 processes double-counts shared pages; `mem used` is
the honest figure.

Reading: the browser is the biggest consumer, not Claude Code. Even the
deliberately unfair browser-heavy run left 8.7 GB free and never swapped.
The N100's limit is burst CPU, not RAM: the desktop will feel sluggish for
the seconds a tab storm or a video decode pegs all four cores. 16 GB is the
right spec, 8 GB would not have been (5.8 GB steady state on a 1.4 GB idle floor).

Caveats: each session is 3 minutes, so Claude Code's context (and RSS)
stays small; expect 1 GB or more per process by the end of a real 30 minute
session. 4K roughly doubles what every tab and the compositor cost versus
1080p.
