#!/usr/bin/env bash
# One simulated attendee session: three phases chained with --continue in the
# same directory so context accumulates like it does for a real person.
# Haiku, low effort, hard turn cap per phase, JSON result per phase.
#   bench/session.sh <workdir> [max_turns_per_phase]
set -euo pipefail

DIR=${1:?workdir}
MAX_TURNS=${2:-30}
mkdir -p "$DIR" && cd "$DIR"

export PATH="$HOME/.local/bin:$PATH"
export DISABLE_AUTOUPDATER=1

PHASES=(
'Scaffold a Vite + React + TypeScript app in the current directory, non-interactively: run `npm create vite@latest . -- --template react-ts` then `npm install`. Add a `PhotoGrid` component that renders a responsive grid of 12 placeholder photo cards (title, date, a coloured block instead of an image) read from a local `src/photos.json` you create. Wire it into `App.tsx` replacing the demo content. Run `npm run build` and fix any errors until it passes. Do not start the dev server. Stop as soon as the build passes.'

'Add a search box above the grid that filters cards by title as you type, and a favourite toggle on each card whose state persists in localStorage. Add Vitest with React Testing Library and jsdom, write tests for the filtering and the favourite toggle, and run them until they pass. Then run the build and make sure it still passes.'

'Two bugs to fix. First, cards should be sorted by date, newest first, and they are not. Second, the grid breaks below 400px wide: cards overflow the viewport. Fix both, add a test for the sort order, then run the tests and the build until both pass.'
)

start=$(date +%s)
for i in "${!PHASES[@]}"; do
  n=$((i + 1))
  cont=(); [ "$n" -gt 1 ] && cont=(--continue)
  claude -p "${PHASES[$i]}" "${cont[@]}" \
    --model haiku \
    --effort low \
    --max-turns "$MAX_TURNS" \
    --output-format json \
    --dangerously-skip-permissions \
    > "result-$n.json" 2>> stderr.log || true
  echo "$(( $(date +%s) - start ))" > "wall_seconds-$n"
done
echo "$(( $(date +%s) - start ))" > wall_seconds
