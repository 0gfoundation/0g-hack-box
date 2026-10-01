# hack-box test suite

Black-box tests written from `docs/SPEC.md` section 7. They check what the spec
promises, not how the build achieves it. Run them in the guest as the admin user
(the one with passwordless sudo), on a box that is between sessions (idle).

## Running

```bash
tests/run.sh                 # every group, non-destructive only
tests/run.sh install network # only the named groups
tests/run.sh --list          # print the group names
tests/run.sh --destructive   # also run the checks that reset or stress the box
tests/run.sh --live          # also run the opt-in live model-call checks
```

Output is one line per check:

```
PASS <group>/<name>
FAIL <group>/<name>: <what was observed>
SKIP <group>/<name>: <why>
```

then a summary line. The runner exits nonzero if anything failed. A SKIP never
fails the run; it means a precondition was not met (wrong host, no sudo, a tool
absent, or a destructive or live check left off).

The suite is safe to run more than once. Every check cleans up after itself, and
the state-changing checks return the box to `idle`. Nothing hangs: every network
or wait step has a timeout.

Two environment variables adjust a run:

- `HB_VERIFY_READONLY=1` (set by `setup/90-verify.sh`): the `states` group skips
  starting, extending and resetting a session, because a reset restarts LightDM
  and bootstrap may be running inside the admin's own desktop session.
- `HB_LIVE_CLAUDE_ARGS` (default empty): extra arguments for the live Claude Code
  call, only for a stand-in endpoint whose context window is smaller than Claude
  Code's full request, for example `HB_LIVE_CLAUDE_ARGS="--tools Bash Read Edit"`
  against the lab gateway (16K tokens). The runner prints a `# note:` line when it
  is set. Leave it empty against the real 0G Router or Anthropic.

Destructive groups (`reset`, `timer`, `limits`, `persistence`) only run with
`--destructive`. They reset the box or stress it on purpose, so run them on a lab
box, not on one mid-hackathon.

## What each group proves

- `install` (spec 4.1): the agents and toolchain are installed system-wide and are
  runnable by `hacker` (`claude`, `opencode`, Node 22, chromium), and none of the
  agents live only in the admin home.
- `accounts` (spec 1, 4d, 5): `hacker` exists with UID 2000 and a locked password,
  is in no privileged group, cannot sudo or su, cannot ssh in, and cannot use cron
  or at.
- `home` (spec 3.1): `/home/hacker` is a separate loop-mounted filesystem, mounted
  `nosuid,nodev`, sized within band (at most the configured size, at least 4G, and
  not the degraded tmpfs fallback), seeded from the skeleton with `~/project`.
- `states` (spec 3.2, 3.4, 3.8): the state file holds a valid word, `status --json`
  is valid JSON with every concept the spec lists, `hackbox start` moves to `active`
  and the countdown decreases, the admin can `extend`, and `hacker` can neither write
  the runtime files nor drive the units.
- `reset` (spec 3.3, destructive): a full teardown removes every session artifact and
  process, restores a fresh skeleton, returns to `idle` within the time limit, and
  brings the desktop back; plus reset from a full home, two concurrent resets, and a
  failing reset hook that does not abort the reset.
- `timer` (spec 3.2, 3.4, 3.5, destructive): a one minute session runs itself down
  through `ending` to `idle` with no operator action, and archives project content.
- `archive` (spec 3.5): staff can list and retrieve a tarball that contains the work
  but excludes `node_modules` and secret-looking files. The controlled content checks
  need `--destructive` (making a deterministic archive requires a reset).
- `network` (spec 5): the attendee reaches the public internet, DNS and loopback, but
  is refused fast on the private, link-local and tailnet ranges, while the admin is
  not filtered on the routable lab addresses.
- `limits` (spec 5, destructive): the slice caps are in force, and a fork bomb and a
  memory hog do not take down the admin session; the hog is killed or capped.
- `agents` (spec 4.2 to 4.5): `agents show`/`set` behave, `claude` with `claude-0g`
  is rejected, each valid combination yields consistent valid JSON configs, secret
  files have the right owner and mode, `secret list` never prints a value, and a fresh
  home has the onboarding seed. `--live` runs one real prompt through each enabled
  agent, through `hackbox-agent` (the command the welcome buttons run).
- `desktop` (spec 3.6, 5): the pinned dconf policy is in force and locked for the
  attendee, and the overlay guard respawns the overlay when it is killed.
- `persistence` (spec 3.3, destructive): one artifact per persistence vector is gone
  after a reset (home, tmp, shm, crontab, user units, linger, browser profile, agent
  history, SysV IPC).
- `cli` (spec 3.8): every documented subcommand exists, an unknown one prints the
  list, bad input prints usage and exits nonzero, read only subcommands work.

## Layout

- `run.sh` argument parsing, group dispatch, result accounting, exit code.
- `lib.sh` shared helpers: `as_hacker`, `wait_for`, JSON parsing (python3 first,
  jq fallback), config loading, result printers.
- `groups/NN-name.sh` one file per group, each defining `group_<name>`.
- `breakout.md` the human adversarial checklist.

`setup/90-verify.sh` runs the non-destructive groups at the end of bootstrap and
prints a banner; it never aborts the install on a FAIL.

## Dependencies in the guest

`bash`, `python3` (ships with Mint) or `jq`, `curl`, `systemd`, `nftables` and the
standard coreutils. Checks that need a tool which is absent SKIP with a reason.
