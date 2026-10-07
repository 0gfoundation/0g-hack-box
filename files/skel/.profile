# hack-box attendee login profile. Read by the X session and login shells.
export NPM_CONFIG_PREFIX="$HOME/.npm-global"
export EDITOR=nano
export BROWSER=chromium
for d in "$HOME/.npm-global/bin" "$HOME/.local/bin"; do
  case ":$PATH:" in *":$d:"*) ;; *) PATH="$d:$PATH" ;; esac
done
export PATH

if [ -n "$BASH_VERSION" ] && [ -f "$HOME/.bashrc" ]; then
  . "$HOME/.bashrc"
fi
