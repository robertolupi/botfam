#!/usr/bin/env python3
import subprocess
import sys
import re

PROTECTED_BRANCHES = {
    'main',
    'master',
    'botfam-next',
    'agent/agy',
    'agent/claude',
    'agent/codex',
    'human/rlupi',
}

def run_git(args):
    result = subprocess.run(['git'] + args, capture_output=True, text=True, check=True)
    return result.stdout.strip()

def get_active_worktree_branches():
    branches = set()
    output = run_git(['worktree', 'list'])
    # Output format: /path/to/worktree  sha [branch-name]
    for line in output.splitlines():
        match = re.search(r'\[([^\]]+)\]$', line)
        if match:
            branches.add(match.group(1))
    return branches

def main():
    tag = 'pre-sprint-2026-06-14'

    # 1. Get active worktree branches
    active_branches = get_active_worktree_branches()

    # 2. Get local merged branches
    local_output = run_git(['branch', '--merged', tag])
    local_branches = []
    for line in local_output.splitlines():
        # strip leading whitespace and the '*' or '+' if present
        clean_name = line.strip().lstrip('*+ ').strip()
        if not clean_name:
            continue
        # Check protection
        if clean_name in PROTECTED_BRANCHES or clean_name in active_branches:
            continue
        try:
            sha = run_git(['rev-parse', clean_name])
            local_branches.append((sha, clean_name))
        except Exception:
            pass

    # 3. Get remote merged branches
    remote_output = run_git(['branch', '-r', '--merged', tag])
    remote_branches = []
    for line in remote_output.splitlines():
        clean_name = line.strip().lstrip('*+ ').strip()
        if not clean_name:
            continue
        if '->' in clean_name:  # skip HEAD pointer references
            continue

        # Remote branches are usually named <remote>/<branch_name>
        parts = clean_name.split('/', 1)
        if len(parts) != 2:
            continue
        remote, branch_name = parts

        if branch_name in PROTECTED_BRANCHES or clean_name in active_branches or branch_name in active_branches:
            continue

        try:
            sha = run_git(['rev-parse', clean_name])
            remote_branches.append((sha, remote, branch_name, clean_name))
        except Exception:
            pass

    # Print markdown table of SHAs and branches
    print("### Merged Local Branches")
    print("| SHA | Branch Name |")
    print("| --- | --- |")
    for sha, name in local_branches:
        print(f"| `{sha[:8]}` | `{name}` |")

    print("\n### Merged Remote Branches")
    print("| SHA | Remote | Branch Name |")
    print("| --- | --- | --- |")
    for sha, remote, name, full_name in remote_branches:
        print(f"| `{sha[:8]}` | `{remote}` | `{name}` |")

    # Generate deletion commands
    print("\n### Deletion Commands (Local)")
    print("```bash")
    for _, name in local_branches:
        print(f"git branch -d \"{name}\"")
    print("```")

    print("\n### Deletion Commands (Remote)")
    print("```bash")
    for _, remote, name, _ in remote_branches:
        print(f"git push {remote} --delete \"{name}\"")
    print("```")

if __name__ == '__main__':
    main()
