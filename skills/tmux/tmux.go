// Package tmux provides the "tmux" skill documentation and helper scripts.
package tmux

import skills "github.com/hkuds/nanobot-go/skills"

func init() { skills.Register(Skill{}) }

// Skill documents tmux usage.
type Skill struct{}

// Name returns "tmux".
func (Skill) Name() string { return "tmux" }

// Files returns helper scripts.
func (Skill) Files() map[string]string {
	return map[string]string{
		"scripts/find-sessions.sh": `#!/usr/bin/env bash
# find-sessions.sh — list tmux sessions, defaulting to a private socket.
set -euo pipefail
SOCKET="${NANOBOT_TMUX_SOCKET:-/tmp/nanobot-tmux.sock}"
exec tmux -S "$SOCKET" ls 2>/dev/null || echo "(no sessions)"
`,
		"scripts/wait-for-text.sh": `#!/usr/bin/env bash
# wait-for-text.sh SESSION TEXT [TIMEOUT]
set -euo pipefail
SESSION="$1"; TEXT="$2"; TIMEOUT="${3:-30}"
SOCKET="${NANOBOT_TMUX_SOCKET:-/tmp/nanobot-tmux.sock}"
deadline=$(( $(date +%s) + TIMEOUT ))
while (( $(date +%s) < deadline )); do
  out=$(tmux -S "$SOCKET" capture-pane -p -t "$SESSION" 2>/dev/null || true)
  if grep -q -- "$TEXT" <<<"$out"; then
    echo "$out"
    exit 0
  fi
  sleep 1
done
echo "timeout waiting for: $TEXT" >&2
exit 1
`,
	}
}

// SkillMD returns the body.
func (Skill) SkillMD() string {
	return `---
name: tmux
description: Drive tmux on a dedicated socket, with capture-pane and wait helpers.
requires:
  bins:
    - tmux
    - bash
---

# Tmux skill

Control long-running programs via tmux on a dedicated socket so user sessions
are unaffected. Default socket: ` + "`${NANOBOT_TMUX_SOCKET:-/tmp/nanobot-tmux.sock}`" + `.

## Helper scripts

- ` + "`scripts/find-sessions.sh`" + ` — list existing sessions.
- ` + "`scripts/wait-for-text.sh SESSION TEXT [TIMEOUT]`" + ` — block until TEXT
  appears in the pane's output.

## Patterns

` + "```" + `bash
SOCKET=/tmp/nanobot-tmux.sock
tmux -S $SOCKET new-session -d -s build 'make watch'
tmux -S $SOCKET capture-pane -p -t build
` + "```" + `
`
}
