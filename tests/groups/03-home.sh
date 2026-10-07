# home: /home/hacker is a separate loop-mounted filesystem of the configured
# size, mounted nosuid and nodev, seeded from the skeleton with ~/project.
# Spec 3.1.

# Parse a size like 40G into bytes (approximate, base 1024).
_size_to_bytes() {
  local s="$1" n unit
  n="${s%[GgMmKk]*}"; unit="${s##*[0-9]}"
  case "$unit" in
    G|g) echo $(( n * 1024 * 1024 * 1024 )) ;;
    M|m) echo $(( n * 1024 * 1024 )) ;;
    K|k) echo $(( n * 1024 )) ;;
    *)   echo "$s" ;;
  esac
}

group_home() {
  need_linux_or_skip "home_mounted" || return
  local H="/home/$HACKER_USER"

  # Degraded fallback: if the image could not be built the box runs on a 4G
  # tmpfs home and raises /run/hackbox/degraded. That is a working box but not
  # the promised one, so it is a failure the operator must see.
  if [ -e /run/hackbox/degraded ]; then
    fail "home_not_degraded" "box is in degraded mode (/run/hackbox/degraded present): home is the tmpfs fallback, not the ext4 image"
  else
    pass "home_not_degraded"
  fi

  # It is a mount point in its own right (separate filesystem).
  if mountpoint -q "$H"; then
    pass "home_is_mountpoint"
  else
    fail "home_is_mountpoint" "$H is not a separate mount point"
    return
  fi

  # findmnt gives the options and source.
  local opts src fstype
  opts="$(findmnt -no OPTIONS "$H" 2>/dev/null)"
  src="$(findmnt -no SOURCE "$H" 2>/dev/null)"
  fstype="$(findmnt -no FSTYPE "$H" 2>/dev/null)"

  # nosuid.
  if printf '%s' "$opts" | grep -qw nosuid; then
    pass "home_nosuid"
  else
    fail "home_nosuid" "mount options lack nosuid: $opts"
  fi
  # nodev.
  if printf '%s' "$opts" | grep -qw nodev; then
    pass "home_nodev"
  else
    fail "home_nodev" "mount options lack nodev: $opts"
  fi

  # Backed by a loop device / image, not the root filesystem.
  if printf '%s' "$src" | grep -qiE 'loop|/dev/mapper|home\.img'; then
    pass "home_loop_backed"
  else
    # Root fs backing would mean the whole disposable-home design is missing.
    rootsrc="$(findmnt -no SOURCE / 2>/dev/null)"
    if [ "$src" = "$rootsrc" ]; then
      fail "home_loop_backed" "$H shares the root fs source ($src); not a separate image"
    else
      # Some other dedicated device is acceptable if it is not root.
      pass "home_loop_backed"
    fi
  fi

  # Size band. The image is fallocated to min(HOME_IMG_SIZE, free disk minus a
  # reserve), so on a small VM disk it is legitimately smaller than the config.
  # Assert: at most HOME_IMG_SIZE (with a little slack for fs overhead) and at
  # least 4G (below that is the degraded tmpfs fallback, caught above).
  local total_bytes want_bytes floor_bytes ceil_bytes
  total_bytes="$(df -B1 --output=size "$H" 2>/dev/null | tail -1 | tr -d ' ')"
  want_bytes="$(_size_to_bytes "$HOME_IMG_SIZE")"
  floor_bytes=$(( 4 * 1024 * 1024 * 1024 ))
  if [ -n "$total_bytes" ] && [ -n "$want_bytes" ]; then
    # df reports usable space, a few percent under the raw image size; allow the
    # ceiling a 5 percent margin above the configured size.
    ceil_bytes=$(( want_bytes * 105 / 100 ))
    if [ "$total_bytes" -le "$ceil_bytes" ] && [ "$total_bytes" -ge "$floor_bytes" ]; then
      pass "home_size_in_band"
    elif [ "$total_bytes" -gt "$ceil_bytes" ]; then
      fail "home_size_in_band" "home is ${total_bytes}B, above the configured ceiling ${HOME_IMG_SIZE}"
    else
      fail "home_size_in_band" "home is ${total_bytes}B, below the 4G floor (degraded?)"
    fi
  else
    skip "home_size_in_band" "could not read df size for $H"
  fi

  # Skeleton present: a real ext4 home starts with a small file set from the
  # skeleton. Assert the attendee owns their home and a couple of skeleton
  # dotfiles exist (bashrc). The exact skeleton is the session owner's, so keep
  # this to observable essentials.
  owner="$(stat -c '%U' "$H" 2>/dev/null)"
  if [ "$owner" = "$HACKER_USER" ]; then
    pass "home_owned_by_hacker"
  else
    fail "home_owned_by_hacker" "$H owned by '$owner', expected $HACKER_USER"
  fi

  if as_hacker_t 10 'test -f "$HOME/.bashrc"'; then
    pass "skeleton_dotfiles"
  else
    fail "skeleton_dotfiles" "~/.bashrc missing from the seeded home"
  fi

  # ~/project exists with starter files (spec 3.7/6: agents launch in ~/project,
  # CLAUDE.md and AGENTS.md are seeded).
  if as_hacker_t 10 'test -d "$HOME/project"'; then
    pass "project_dir_exists"
  else
    fail "project_dir_exists" "~/project missing"
  fi
  if as_hacker_t 10 'test -s "$HOME/project/CLAUDE.md" && test -s "$HOME/project/AGENTS.md"'; then
    pass "project_starter_files"
  else
    fail "project_starter_files" "~/project is missing CLAUDE.md and/or AGENTS.md"
  fi
}
