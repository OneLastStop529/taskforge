#!/usr/bin/env zsh

# Load taskforge completion definitions from this repository only.
typeset repo_root completion_dir
repo_root=${${(%):-%N}:A:h:h}
completion_dir="$repo_root/completions/zsh"

if [[ ! -d "$completion_dir" ]]; then
  print -u2 "taskforge completion: missing directory $completion_dir"
  return 1
fi

fpath=("$completion_dir" $fpath)

autoload -Uz compinit
if ! whence -w compdef >/dev/null 2>&1; then
  compinit
fi

autoload -Uz _taskforge
compdef _taskforge taskforge
compdef _taskforge ./bin/taskforge
compdef _taskforge bin/taskforge
