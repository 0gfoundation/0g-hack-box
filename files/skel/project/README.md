# Welcome to the 0G hack box

This is a shared machine in the 0G hacker zone. You get **30 minutes** to build something
on 0G with an AI coding agent. The agent already knows 0G: networks, storage, compute and
contracts are described in `CLAUDE.md` / `AGENTS.md`, and working starters are in `examples/`.

**When the timer hits zero, this machine is wiped.** Everything in it is deleted. Save your
work before then (see below). Do not type personal passwords, seed phrases or private keys
with real funds into this machine. The agent will make a throwaway testnet wallet for you.

## Three things to ask the agent first

Copy one of these into the agent to get going:

```
Run the examples in examples/ that need no key, then make me a throwaway testnet wallet and tell me where to fund it.
```

```
Build the "AI note vault" idea from CLAUDE.md: a localhost web page where I type a note, 0G Compute summarises it and 0G Storage keeps it. Get a first version running in 10 minutes.
```

```
Help me build my own idea on 0G in 25 minutes: <describe it>. Pick the smallest version that shows 0G Chain, Storage or Compute, and tell me the plan before you start.
```

Free testnet tokens for your throwaway wallet: https://faucet.0g.ai (0.1 0G per day).

## Save your work

- **GitHub:** ask the agent "commit this and push it to a new repo on my GitHub". It will run
  `gh auth login`: you enter a short code at github.com/login/device, no password typed here.
- **USB stick:** plug it in and ask the agent to copy the project to it (without `node_modules`).
- When time runs out, a 6-character code is shown on screen. Note it down: the organisers
  may be able to recover a copy of `~/project` for a short time.

## Help

- Docs: https://docs.0g.ai
- Stuck, or the machine misbehaves: ask the organisers at the desk.
