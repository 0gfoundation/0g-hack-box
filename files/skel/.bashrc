# hack-box attendee shell. This home is wiped at the end of every session.
case $- in *i*) ;; *) return ;; esac

HISTCONTROL=ignoreboth
HISTSIZE=5000
HISTFILESIZE=10000
shopt -s histappend checkwinsize

# Tools installed with `npm i -g` or pip --user land in the home.
for d in "$HOME/.npm-global/bin" "$HOME/.local/bin"; do
  case ":$PATH:" in *":$d:"*) ;; *) PATH="$d:$PATH" ;; esac
done
export PATH

[ -f /usr/share/bash-completion/bash_completion ] && . /usr/share/bash-completion/bash_completion
[ -f /usr/lib/git-core/git-sh-prompt ] && . /usr/lib/git-core/git-sh-prompt

alias ls='ls --color=auto'
alias ll='ls -alF'
alias la='ls -A'
alias grep='grep --color=auto'

# Time left in the session, from the root-owned clock.
__hb_left() {
  local end state now
  state=$(cat /run/hackbox/state 2>/dev/null)
  [ "$state" = active ] || return 0
  end=$(cat /run/hackbox/session-end 2>/dev/null) || return 0
  now=$(date +%s)
  [ "$end" -gt "$now" ] 2>/dev/null || { printf '[0:00] '; return 0; }
  printf '[%d:%02d] ' $(( (end - now) / 60 )) $(( (end - now) % 60 ))
}
__hb_git() { type __git_ps1 >/dev/null 2>&1 && __git_ps1 ' (%s)'; }

PS1='\[\e[33m\]$(__hb_left)\[\e[32m\]\u@\h\[\e[0m\]:\[\e[34m\]\w\[\e[35m\]$(__hb_git)\[\e[0m\]\$ '

# New terminals start in the project, unless opened somewhere specific.
[ "$PWD" = "$HOME" ] && [ -d "$HOME/project" ] && cd "$HOME/project"
