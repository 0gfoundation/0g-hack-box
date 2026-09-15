#!/usr/bin/env bash
# One non-interactive Claude Code session doing a fixed, representative task
# in a fresh directory. Haiku, hard turn cap, JSON result with token usage.
#   bench/session.sh <workdir> [max_turns]
set -euo pipefail

DIR=${1:?workdir}
MAX_TURNS=${2:-25}
mkdir -p "$DIR" && cd "$DIR"

export PATH="$HOME/.local/bin:$PATH"
export DISABLE_AUTOUPDATER=1

TASK='Scaffold a Vite + React + TypeScript app in the current directory, non-interactively: run `npm create vite@latest . -- --template react-ts` then `npm install`. Add a `PhotoGrid` component that renders a responsive grid of 12 placeholder photo cards (title, date, a coloured block instead of an image) read from a local `src/photos.json` you create. Wire it into `App.tsx` replacing the demo content. Run `npm run build` and fix any errors until it passes. Do not start the dev server. Stop as soon as the build passes.'

start=$(date +%s)
claude -p "$TASK" \
  --model haiku \
  --max-turns "$MAX_TURNS" \
  --output-format json \
  --dangerously-skip-permissions \
  > result.json 2> stderr.log || true
echo "$(( $(date +%s) - start ))" > wall_seconds
